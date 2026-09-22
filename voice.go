package main

import (
	"bytes"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"text/template"
	"time"

	"github.com/gorilla/websocket"
)

//go:embed prompts/sort_notes.prompt
var sortNotesPromptTmpl string

//go:embed static/voice-scout.js
var voiceScoutJS []byte

//go:embed static/offline-sync.js
var offlineSyncJS []byte

const geminiLiveWS = "wss://generativelanguage.googleapis.com/ws/google.ai.generativelanguage.v1beta.GenerativeService.BidiGenerateContent"

const transcribeLiveModel = "models/gemini-3.5-transcribe-live"

var (
	teamPrefixedRe = regexp.MustCompile(`(?i)\bteam\s+(\d{1,5})(?:['’]s)?\b`)
	bareTeamNumRe  = regexp.MustCompile(`\b(\d{3,5})\b`)
	collapseSpace  = regexp.MustCompile(`\s+`)
)

var noteCategories = []string{"auto", "scoring", "driving", "defense", "reliability", "other"}

var noteCategoryLabels = map[string]string{
	"auto":        "Auto",
	"scoring":     "Scoring",
	"driving":     "Driving",
	"defense":     "Defense",
	"reliability": "Reliability",
	"other":       "Other",
}

var frcVocab = []string{
	"auto", "autonomous", "teleop", "defense", "defended", "broke", "broken",
	"disabled", "intake", "outtake", "shooter", "climb", "endgame", "cycle",
	"cycles", "foul", "mobility", "jammed", "tipped", "brownout",
}

var wsUpgrader = websocket.Upgrader{
	ReadBufferSize:  16 * 1024,
	WriteBufferSize: 16 * 1024,
	CheckOrigin:     func(r *http.Request) bool { return true },
}

type SortedNote struct {
	Team     string `json:"team"`
	Category string `json:"category"`
	Text     string `json:"text"`
}

type SortNotesRequest struct {
	Transcript string          `json:"transcript"`
	Teams      []sortTeamInput `json:"teams"`
	FocusTeam  string          `json:"focus_team"`
}

type sortTeamInput struct {
	Number   string `json:"number"`
	Alliance string `json:"alliance"`
}

type SortNotesResponse struct {
	CurrentTeam string            `json:"current_team"`
	Notes       map[string]string `json:"notes"`
	Items       []SortedNote      `json:"items"`
}

type sortNotesPromptData struct {
	TeamList   string
	FocusTeam  string
	Transcript string
}

type sortNotesJSON struct {
	CurrentTeam string       `json:"current_team"`
	Notes       []SortedNote `json:"notes"`
}

type voiceClientMsg struct {
	Type string `json:"type"`
	Team string `json:"team,omitempty"`
	Text string `json:"text,omitempty"`
}

type geminiLiveMsg struct {
	SetupComplete json.RawMessage `json:"setupComplete"`
	Error         *struct {
		Message string `json:"message"`
	} `json:"error"`
	ServerContent struct {
		InterimInputTranscription *struct {
			Text string `json:"text"`
		} `json:"interimInputTranscription"`
		InputTranscription *struct {
			Text string `json:"text"`
		} `json:"inputTranscription"`
	} `json:"serverContent"`
}

func voiceScoutJSHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Write(voiceScoutJS)
}

func offlineSyncJSHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Write(offlineSyncJS)
}

func sortNotesHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Only POST allowed", http.StatusMethodNotAllowed)
		return
	}

	var req SortNotesRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}

	resp, err := sortVoiceNotes(req)
	if err != nil {
		log.Printf("sort notes: %v", err)
		http.Error(w, "Failed to sort notes: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func sortVoiceNotes(req SortNotesRequest) (SortNotesResponse, error) {
	allowed := map[string]bool{}
	var teamList []string
	for _, t := range req.Teams {
		n := strings.TrimSpace(t.Number)
		if n == "" {
			continue
		}
		allowed[n] = true
		alliance := t.Alliance
		if alliance == "" {
			alliance = "?"
		}
		teamList = append(teamList, fmt.Sprintf("- %s (%s)", n, alliance))
	}

	empty := SortNotesResponse{Notes: map[string]string{}, Items: []SortedNote{}}
	if strings.TrimSpace(req.Transcript) == "" || len(allowed) == 0 {
		return empty, nil
	}

	tmpl, err := template.New("sort_notes").Parse(sortNotesPromptTmpl)
	if err != nil {
		return empty, fmt.Errorf("parse sort notes prompt: %w", err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, sortNotesPromptData{
		TeamList:   strings.Join(teamList, "\n"),
		FocusTeam:  strings.TrimSpace(req.FocusTeam),
		Transcript: strings.TrimSpace(req.Transcript),
	}); err != nil {
		return empty, fmt.Errorf("render sort notes prompt: %w", err)
	}

	raw, err := geminiPost(buf.String())
	if err != nil {
		return empty, err
	}
	raw = strings.TrimSpace(raw)
	raw = strings.TrimPrefix(raw, "```json")
	raw = strings.TrimPrefix(raw, "```")
	raw = strings.TrimSuffix(raw, "```")
	raw = strings.TrimSpace(raw)

	var parsed sortNotesJSON
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return empty, fmt.Errorf("parse sort JSON: %v — raw: %s", err, raw)
	}

	focus := strings.TrimSpace(req.FocusTeam)
	var items []SortedNote
	for _, n := range parsed.Notes {
		n.Team = strings.TrimSpace(n.Team)
		n.Category = strings.ToLower(strings.TrimSpace(n.Category))
		n.Text = strings.TrimSpace(n.Text)
		if n.Text == "" {
			continue
		}
		if !allowed[n.Team] {
			if allowed[focus] {
				n.Team = focus
			} else {
				continue
			}
		}
		if noteCategoryLabels[n.Category] == "" {
			n.Category = "other"
		}
		if focus != "" && isOpponentOnly(req.Transcript, n.Team, mapsKeys(allowed)) {
			if beingDefendedRe.MatchString(n.Text) || !playedDefenseRe.MatchString(n.Text) {
				n.Team = focus
			}
		}
		n.Text = cleanNoteText(n.Text, n.Team)
		if n.Text == "" {
			continue
		}
		items = append(items, n)
	}
	items = mergeRelatedNotes(items)

	current := strings.TrimSpace(parsed.CurrentTeam)
	if !allowed[current] || isOpponentOnly(req.Transcript, current, mapsKeys(allowed)) {
		current = detectCurrentTeam(req.Transcript, mapsKeys(allowed))
	}
	if current == "" && allowed[focus] {
		current = focus
	}

	return SortNotesResponse{
		CurrentTeam: current,
		Notes:       formatNotesByTeam(items),
		Items:       items,
	}, nil
}

func formatNotesByTeam(items []SortedNote) map[string]string {
	grouped := map[string][]SortedNote{}
	for _, n := range items {
		grouped[n.Team] = append(grouped[n.Team], n)
	}
	out := map[string]string{}
	for team, notes := range grouped {
		out[team] = formatTeamNotes(notes)
	}
	return out
}

func formatTeamNotes(items []SortedNote) string {
	byCat := map[string][]string{}
	for _, n := range items {
		cat := n.Category
		if noteCategoryLabels[cat] == "" {
			cat = "other"
		}
		text := strings.TrimSpace(n.Text)
		if text == "" {
			continue
		}
		byCat[cat] = append(byCat[cat], text)
	}
	var parts []string
	for _, cat := range noteCategories {
		lines := byCat[cat]
		if len(lines) == 0 {
			continue
		}
		parts = append(parts, noteCategoryLabels[cat]+": "+strings.Join(uniqueKeepOrder(lines), "; "))
	}
	return strings.Join(parts, "\n")
}

func uniqueKeepOrder(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		key := strings.ToLower(strings.TrimSpace(s))
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, s)
	}
	return out
}

func mapsKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

var opponentPrep = map[string]bool{
	"from":     true,
	"against":  true,
	"by":       true,
	"vs":       true,
	"versus":   true,
	"off":      true,
	"avoid":    true,
	"avoiding": true,
	"dodge":    true,
	"dodging":  true,
	"around":   true,
	"past":     true,
	"than":     true,
	"facing":   true,
}

func isUtteranceStart(text string, idx int) bool {
	if idx <= 0 {
		return true
	}
	return strings.TrimSpace(strings.Trim(text[:idx], ".,;:!?\"'")) == ""
}

func isPossessiveSpan(text string, start, end int) bool {
	if start < 0 || end < start || end > len(text) {
		return false
	}
	span := strings.ToLower(text[start:end])
	if strings.Contains(span, "'s") || strings.Contains(span, "’s") {
		return true
	}
	if end >= len(text) {
		return false
	}
	rest := text[end:]
	return strings.HasPrefix(rest, "'s") || strings.HasPrefix(rest, "’s")
}

func isOpponentContext(text string, idx int) bool {
	if idx <= 0 {
		return false
	}
	fields := strings.Fields(strings.ToLower(strings.TrimSpace(text[:idx])))
	if len(fields) == 0 {
		return false
	}
	w := strings.Trim(fields[len(fields)-1], ".,;:!?")
	if opponentPrep[w] {
		return true
	}
	// "from team 1006" — the digits sit after the word "team"
	if w == "team" && len(fields) >= 2 {
		return opponentPrep[strings.Trim(fields[len(fields)-2], ".,;:!?")]
	}
	return false
}

func isOpponentMention(text string, idx, end int) bool {
	if isOpponentContext(text, idx) {
		return true
	}
	// Mid-sentence "team 1006's defense" is the opponent, not a subject switch.
	// Possessive at the start ("Team 1006's auto") can still be the subject.
	if isPossessiveSpan(text, idx, end) && !isUtteranceStart(text, idx) {
		return true
	}
	return false
}

// detectCurrentTeam returns the last match-team mentioned as the subject of
// the speech. "from team 1006" / "against 1006" / "avoid team 1006's defense"
// does not switch the speaker's focus — that is the opponent.
func detectCurrentTeam(text string, teams []string) string {
	allowed := map[string]bool{}
	for _, t := range teams {
		allowed[t] = true
	}
	lastIdx := -1
	last := ""
	consider := func(idx, end int, team string) {
		if !allowed[team] || isOpponentMention(text, idx, end) {
			return
		}
		if idx >= lastIdx {
			lastIdx = idx
			last = team
		}
	}
	for _, m := range teamPrefixedRe.FindAllStringSubmatchIndex(text, -1) {
		consider(m[0], m[1], text[m[2]:m[3]])
	}
	for _, m := range bareTeamNumRe.FindAllStringSubmatchIndex(text, -1) {
		consider(m[0], m[1], text[m[2]:m[3]])
	}
	return last
}

func isOpponentOnly(text, team string, teams []string) bool {
	allowed := false
	for _, t := range teams {
		if t == team {
			allowed = true
			break
		}
	}
	if !allowed || team == "" {
		return false
	}
	saw := false
	asSubject := false
	check := func(idx, end int, found string) {
		if found != team {
			return
		}
		saw = true
		if !isOpponentMention(text, idx, end) {
			asSubject = true
		}
	}
	for _, m := range teamPrefixedRe.FindAllStringSubmatchIndex(text, -1) {
		check(m[0], m[1], text[m[2]:m[3]])
	}
	if len(team) >= 3 {
		for _, m := range bareTeamNumRe.FindAllStringSubmatchIndex(text, -1) {
			check(m[0], m[1], text[m[2]:m[3]])
		}
	}
	return saw && !asSubject
}

func normalizeWS(s string) string {
	return strings.TrimSpace(collapseSpace.ReplaceAllString(s, " "))
}

// leftoverIfRewrite reports whether newText is the previous utterance with extra
// wrapping (Gemini SMART often prepends "Team 254" onto the last sentence).
// leftover is only the new words; the original sentence should stay committed.
func leftoverIfRewrite(old, new string) (string, bool) {
	o := normalizeWS(old)
	n := normalizeWS(new)
	if o == "" || n == "" {
		return n, false
	}
	if strings.EqualFold(o, n) {
		return "", true
	}
	idx := strings.Index(strings.ToLower(n), strings.ToLower(o))
	if idx < 0 {
		return n, false
	}
	leftover := normalizeWS(n[:idx] + " " + n[idx+len(o):])
	return leftover, true
}

func stripTeamMentions(text string, teams []string) string {
	t := teamPrefixedRe.ReplaceAllString(text, " ")
	for _, team := range teams {
		if len(team) < 3 {
			continue
		}
		t = regexp.MustCompile(`\b`+regexp.QuoteMeta(team)+`\b`).ReplaceAllString(t, " ")
	}
	return normalizeWS(strings.Trim(t, " \t.,;:-"))
}

func cleanNoteText(text, subject string) string {
	t := strings.TrimSpace(text)
	t = strings.TrimPrefix(t, "-")
	t = strings.TrimSpace(t)
	// Strip a leading "Team {subject}" prefix only. Keep other match teams in
	// the body — "struggled against 1006" is useful on 1002's card.
	if subject != "" {
		if loc := teamPrefixedRe.FindStringSubmatchIndex(t); loc != nil && loc[0] == 0 && t[loc[2]:loc[3]] == subject {
			t = strings.TrimSpace(t[loc[1]:])
		}
	}
	return strings.Trim(t, " \t.,;:-")
}

var teleopTopicRe = regexp.MustCompile(`(?i)\b(teleop|tele-op|driver|driving|cycle|cycles|climb|endgame|defense|defended|broke|broken|disabled|jammed)\b`)
var continuationRe = regexp.MustCompile(`(?i)^(where|which|and they|then they|behind|after that|went out)\b`)
var beingDefendedRe = regexp.MustCompile(`(?i)\b(struggling|against (some )?defense|being defended|was defended|got defended)\b`)
var playedDefenseRe = regexp.MustCompile(`(?i)\b(played defense|playing defense|on defense)\b`)

// mergeRelatedNotes keeps a follow-up clause with the observation it continues.
// "ran a second bot auto" + "went out behind their teammate" stays one auto note.
func mergeRelatedNotes(items []SortedNote) []SortedNote {
	if len(items) < 2 {
		return items
	}
	out := []SortedNote{items[0]}
	for _, n := range items[1:] {
		prev := &out[len(out)-1]
		if prev.Team == n.Team && shouldMergeNotes(*prev, n) {
			prev.Text = strings.TrimSpace(prev.Text + "; " + n.Text)
			continue
		}
		out = append(out, n)
	}
	return out
}

func shouldMergeNotes(prev, next SortedNote) bool {
	if continuationRe.MatchString(next.Text) {
		return true
	}
	if prev.Category == "auto" && next.Category == "driving" && !teleopTopicRe.MatchString(next.Text) {
		return true
	}
	return false
}

func appendNotes(dst []SortedNote, extra []SortedNote) []SortedNote {
	if len(extra) == 0 {
		return dst
	}
	seen := map[string]bool{}
	for _, n := range dst {
		seen[strings.ToLower(n.Team+"|"+n.Category+"|"+n.Text)] = true
	}
	for _, n := range extra {
		key := strings.ToLower(n.Team + "|" + n.Category + "|" + n.Text)
		if seen[key] {
			continue
		}
		seen[key] = true
		dst = append(dst, n)
	}
	return dst
}

func voiceScoutWSHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Only GET allowed", http.StatusMethodNotAllowed)
		return
	}

	apiKey := os.Getenv("GEMINI_API_KEY")
	if apiKey == "" {
		http.Error(w, "GEMINI_API_KEY not set", http.StatusInternalServerError)
		return
	}

	teams := splitCSV(r.URL.Query().Get("teams"))
	focus := strings.TrimSpace(r.URL.Query().Get("focus_team"))
	alliances := splitCSV(r.URL.Query().Get("alliances"))
	sortTeams := make([]sortTeamInput, 0, len(teams))
	for i, t := range teams {
		a := ""
		if i < len(alliances) {
			a = alliances[i]
		}
		sortTeams = append(sortTeams, sortTeamInput{Number: t, Alliance: a})
	}

	client, err := wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("voice ws upgrade: %v", err)
		return
	}
	defer client.Close()

	geminiURL := geminiLiveWS + "?key=" + url.QueryEscape(apiKey)
	dialer := websocket.Dialer{HandshakeTimeout: 15 * time.Second}
	gemini, _, err := dialer.Dial(geminiURL, nil)
	if err != nil {
		writeClientJSON(client, voiceClientMsg{Type: "error", Text: "Could not reach Gemini Live: " + err.Error()})
		return
	}
	defer gemini.Close()

	vocab := append([]string{}, teams...)
	vocab = append(vocab, frcVocab...)

	setup := map[string]any{
		"setup": map[string]any{
			"model": transcribeLiveModel,
			"generationConfig": map[string]any{
				"responseModalities": []string{"TEXT"},
			},
			"inputAudioTranscription": map[string]any{
				"languageCodes":    []string{"en-US"},
				"mode":             "SMART",
				"customVocabulary": vocab,
			},
		},
	}
	if err := gemini.WriteJSON(setup); err != nil {
		writeClientJSON(client, voiceClientMsg{Type: "error", Text: "Gemini setup failed: " + err.Error()})
		return
	}

	var (
		clientMu    sync.Mutex
		geminiMu    sync.Mutex
		sortMu      sync.Mutex
		sorting     bool
		dirty       bool
		unsorted    strings.Builder
		committed   []SortedNote
		lastFinal   string
		currentTeam = focus
		readyOnce   sync.Once
	)

	writeClient := func(msg any) {
		clientMu.Lock()
		defer clientMu.Unlock()
		client.SetWriteDeadline(time.Now().Add(10 * time.Second))
		if err := client.WriteJSON(msg); err != nil {
			log.Printf("voice client write: %v", err)
		}
	}

	writeGemini := func(msg any) error {
		geminiMu.Lock()
		defer geminiMu.Unlock()
		gemini.SetWriteDeadline(time.Now().Add(10 * time.Second))
		return gemini.WriteJSON(msg)
	}

	var runSort func()
	runSort = func() {
		sortMu.Lock()
		text := strings.TrimSpace(unsorted.String())
		focusNow := currentTeam
		if text == "" || sorting {
			if text != "" {
				dirty = true
			}
			sortMu.Unlock()
			return
		}
		unsorted.Reset()
		sorting = true
		dirty = false
		sortMu.Unlock()

		go func() {
			resp, err := sortVoiceNotes(SortNotesRequest{
				Transcript: text,
				Teams:      sortTeams,
				FocusTeam:  focusNow,
			})
			if err != nil {
				log.Printf("live sort notes: %v", err)
			} else {
				sortMu.Lock()
				committed = appendNotes(committed, resp.Items)
				items := append([]SortedNote(nil), committed...)
				cur := currentTeam
				sortMu.Unlock()
				writeClient(map[string]any{
					"type":         "notes",
					"current_team": cur,
					"notes":        formatNotesByTeam(items),
					"items":        items,
				})
				if cur != "" {
					writeClient(voiceClientMsg{Type: "talking_about", Team: cur})
				}
			}

			sortMu.Lock()
			sorting = false
			again := dirty
			sortMu.Unlock()
			if again {
				runSort()
			}
		}()
	}

	done := make(chan struct{})
	var closeOnce sync.Once
	closeAll := func() {
		closeOnce.Do(func() { close(done) })
	}

	go func() {
		defer closeAll()
		for {
			_, data, err := gemini.ReadMessage()
			if err != nil {
				return
			}
			var msg geminiLiveMsg
			if err := json.Unmarshal(data, &msg); err != nil {
				continue
			}
			if msg.Error != nil && msg.Error.Message != "" {
				writeClient(voiceClientMsg{Type: "error", Text: msg.Error.Message})
				return
			}
			if len(msg.SetupComplete) > 0 {
				readyOnce.Do(func() {
					writeClient(voiceClientMsg{Type: "ready"})
				})
			}
			sc := msg.ServerContent
			if sc.InterimInputTranscription != nil && sc.InterimInputTranscription.Text != "" {
				text := sc.InterimInputTranscription.Text
				writeClient(voiceClientMsg{Type: "interim", Text: text})
				team := detectCurrentTeam(text, teams)
				if team != "" && focus == "" {
					sortMu.Lock()
					currentTeam = team
					sortMu.Unlock()
					writeClient(voiceClientMsg{Type: "talking_about", Team: team})
				}
			}
			if sc.InputTranscription != nil && sc.InputTranscription.Text != "" {
				raw := strings.TrimSpace(sc.InputTranscription.Text)
				sortMu.Lock()
				chunk := raw
				if lastFinal != "" {
					if leftover, ok := leftoverIfRewrite(lastFinal, raw); ok {
						chunk = leftover
					}
				}
				lastFinal = raw
				if focus == "" {
					if t := detectCurrentTeam(raw, teams); t != "" {
						currentTeam = t
					} else if t := detectCurrentTeam(chunk, teams); t != "" {
						currentTeam = t
					}
				}
				cur := currentTeam
				hasNote := stripTeamMentions(chunk, teams) != ""
				if hasNote {
					if unsorted.Len() > 0 {
						unsorted.WriteByte(' ')
					}
					unsorted.WriteString(chunk)
				}
				sortMu.Unlock()

				if chunk != "" {
					writeClient(voiceClientMsg{Type: "final", Text: chunk})
				}
				if cur != "" {
					writeClient(voiceClientMsg{Type: "talking_about", Team: cur})
				}
				if hasNote {
					runSort()
				}
			}
		}
	}()

	go func() {
		defer closeAll()
		for {
			mt, data, err := client.ReadMessage()
			if err != nil {
				return
			}
			if mt == websocket.TextMessage {
				var ctrl struct {
					Type string `json:"type"`
					Team string `json:"team"`
				}
				if json.Unmarshal(data, &ctrl) != nil {
					continue
				}
				if ctrl.Type == "stop" {
					_ = writeGemini(map[string]any{
						"realtimeInput": map[string]any{"audioStreamEnd": true},
					})
					runSort()
					return
				}
				if ctrl.Type == "focus" {
					team := strings.TrimSpace(ctrl.Team)
					ok := false
					for _, t := range teams {
						if t == team {
							ok = true
							break
						}
					}
					if !ok {
						continue
					}
					sortMu.Lock()
					pending := strings.TrimSpace(unsorted.String())
					old := currentTeam
					unsorted.Reset()
					currentTeam = team
					sortMu.Unlock()
					writeClient(voiceClientMsg{Type: "talking_about", Team: team})
					if pending != "" && old != "" {
						go func(text, focusNow string) {
							resp, err := sortVoiceNotes(SortNotesRequest{
								Transcript: text,
								Teams:      sortTeams,
								FocusTeam:  focusNow,
							})
							if err != nil {
								log.Printf("live sort notes: %v", err)
								return
							}
							sortMu.Lock()
							committed = appendNotes(committed, resp.Items)
							items := append([]SortedNote(nil), committed...)
							cur := currentTeam
							sortMu.Unlock()
							writeClient(map[string]any{
								"type":         "notes",
								"current_team": cur,
								"notes":        formatNotesByTeam(items),
								"items":        items,
							})
						}(pending, old)
					}
				}
				continue
			}
			if mt != websocket.BinaryMessage || len(data) == 0 {
				continue
			}
			payload := map[string]any{
				"realtimeInput": map[string]any{
					"audio": map[string]any{
						"data":     base64.StdEncoding.EncodeToString(data),
						"mimeType": "audio/pcm;rate=16000",
					},
				},
			}
			if err := writeGemini(payload); err != nil {
				writeClient(voiceClientMsg{Type: "error", Text: "Gemini audio send failed: " + err.Error()})
				return
			}
		}
	}()

	<-done
}

func writeClientJSON(conn *websocket.Conn, msg any) {
	conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	_ = conn.WriteJSON(msg)
}

func splitCSV(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	var out []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
