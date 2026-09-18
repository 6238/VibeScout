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

const geminiLiveWS = "wss://generativelanguage.googleapis.com/ws/google.ai.generativelanguage.v1beta.GenerativeService.BidiGenerateContent"

const transcribeLiveModel = "models/gemini-3.5-transcribe-live"

var (
	teamPrefixedRe = regexp.MustCompile(`(?i)\bteam\s+(\d{1,5})\b`)
	bareTeamNumRe  = regexp.MustCompile(`\b(\d{3,5})\b`)
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
		items = append(items, n)
	}

	current := strings.TrimSpace(parsed.CurrentTeam)
	if !allowed[current] {
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

// detectCurrentTeam returns the last match-team mentioned in text. A "team 8"
// prefix matches any length; a bare number only counts if it is 3+ digits so
// "scored 2" does not steal team 2.
func detectCurrentTeam(text string, teams []string) string {
	allowed := map[string]bool{}
	for _, t := range teams {
		allowed[t] = true
	}
	lastIdx := -1
	last := ""
	consider := func(idx int, team string) {
		if allowed[team] && idx >= lastIdx {
			lastIdx = idx
			last = team
		}
	}
	for _, m := range teamPrefixedRe.FindAllStringSubmatchIndex(text, -1) {
		consider(m[0], text[m[2]:m[3]])
	}
	for _, m := range bareTeamNumRe.FindAllStringSubmatchIndex(text, -1) {
		consider(m[0], text[m[2]:m[3]])
	}
	return last
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
		clientMu   sync.Mutex
		geminiMu   sync.Mutex
		sortMu     sync.Mutex
		sorting    bool
		dirty      bool
		transcript strings.Builder
		readyOnce  sync.Once
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
		text := transcript.String()
		if text == "" || sorting {
			if text != "" {
				dirty = true
			}
			sortMu.Unlock()
			return
		}
		sorting = true
		dirty = false
		sortMu.Unlock()

		go func() {
			resp, err := sortVoiceNotes(SortNotesRequest{
				Transcript: text,
				Teams:      sortTeams,
				FocusTeam:  focus,
			})
			if err != nil {
				log.Printf("live sort notes: %v", err)
			} else {
				writeClient(map[string]any{
					"type":         "notes",
					"current_team": resp.CurrentTeam,
					"notes":        resp.Notes,
					"items":        resp.Items,
				})
				if resp.CurrentTeam != "" {
					writeClient(voiceClientMsg{Type: "talking_about", Team: resp.CurrentTeam})
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
				if team == "" && focus != "" {
					team = focus
				}
				if team != "" {
					writeClient(voiceClientMsg{Type: "talking_about", Team: team})
				}
			}
			if sc.InputTranscription != nil && sc.InputTranscription.Text != "" {
				text := strings.TrimSpace(sc.InputTranscription.Text)
				writeClient(voiceClientMsg{Type: "final", Text: text})
				sortMu.Lock()
				if transcript.Len() > 0 {
					transcript.WriteByte(' ')
				}
				transcript.WriteString(text)
				full := transcript.String()
				sortMu.Unlock()
				team := detectCurrentTeam(full, teams)
				if team == "" && focus != "" {
					team = focus
				}
				if team != "" {
					writeClient(voiceClientMsg{Type: "talking_about", Team: team})
				}
				runSort()
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
				}
				if json.Unmarshal(data, &ctrl) == nil && ctrl.Type == "stop" {
					_ = writeGemini(map[string]any{
						"realtimeInput": map[string]any{"audioStreamEnd": true},
					})
					runSort()
					return
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
