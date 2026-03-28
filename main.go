package main

//go:generate templ generate

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"text/template"
	"time"

	"vibe-scout/templates"

	"github.com/a-h/templ"
	"github.com/joho/godotenv"

	_ "github.com/glebarez/go-sqlite"
)

//go:embed prompts/team_analysis.prompt
var teamAnalysisPromptTmpl string

//go:embed prompts/match_plan.prompt
var matchPlanPromptTmpl string

//go:embed prompts/video_scout.prompt
var videoScoutPromptTmpl string

type ScoutSubmission struct {
	EventKey  string          `json:"event_key"`
	MatchNum  int             `json:"match_num"`
	ScouterID int             `json:"scouter_id"`
	Teams     []TeamScoutData `json:"teams"`
}

type TeamScoutData struct {
	TeamNumber string `json:"team_number"`
	Notes      string `json:"notes"`
}

// Event struct matches the TBA 'simple' model
type Event struct {
	Key       string `json:"key"`
	Name      string `json:"name"`
	StartDate string `json:"start_date"`
}

func main() {
	if err := godotenv.Load(); err != nil && !os.IsNotExist(err) {
		log.Fatalf("Error loading .env file: %s", err)
	}

	initDB()

	http.Handle("/", http.HandlerFunc(homeHandler))
	http.HandleFunc("/scout", scoutHandler)
	http.HandleFunc("/api/save-scout", saveScoutDataHandler)
	http.HandleFunc("/analysis", geminiAnalysisPageHandler)
	http.HandleFunc("/api/run-analysis", apiRunAnalysisHandler)
	http.HandleFunc("/api/analyze-team", apiAnalyzeTeamHandler)
	http.HandleFunc("/api/team-notes", apiTeamNotesHandler)
	http.HandleFunc("/match-planner", matchPlannerPageHandler)
	http.HandleFunc("/api/match-plan", apiMatchPlanHandler)
	http.HandleFunc("/admin", adminHandler)
	http.HandleFunc("/api/admin/clear-event", clearEventHandler)
	http.HandleFunc("/api/admin/clear-all", clearAllHandler)
	http.HandleFunc("/api/admin/seed-test", seedTestHandler)
	http.HandleFunc("/api/admin/fill-ai-scout", apiFillAIScoutHandler)
	http.HandleFunc("/api/admin/fill-ai-scout-team", apiFillAIScoutTeamHandler)

	fmt.Println("Vibe Scout v2 running on http://localhost:8080")
	http.ListenAndServe(":8080", nil)
}

func homeHandler(w http.ResponseWriter, r *http.Request) {
	eventMap, err := currentEventMap()
	if err != nil {
		http.Error(w, "Could not load events", 500)
		return
	}

	component := templates.Home(eventMap)
	templ.Handler(component).ServeHTTP(w, r)
}

func scoutHandler(w http.ResponseWriter, r *http.Request) {
	eventKey := r.URL.Query().Get("event_key")
	matchNum, _ := strconv.Atoi(r.URL.Query().Get("match_num"))
	scouterID, _ := strconv.Atoi(r.URL.Query().Get("scouter_id"))
	allianceOverride := r.URL.Query().Get("alliance")

	matches, err := getMatchesCached(eventKey)
	if err != nil {
		http.Error(w, "Failed to fetch schedule", 500)
		return
	}

	var currentMatch Match
	found := false
	for _, m := range matches {
		if m.CompLevel == "qm" && m.MatchNumber == matchNum {
			currentMatch = m
			found = true
			break
		}
	}

	if !found {
		http.Error(w, fmt.Sprintf("Match %d not found", matchNum), 404)
		return
	}

	allianceName := "Red"
	var teamKeys []string

	if allianceOverride == "Red" {
		allianceName = "Red"
		teamKeys = currentMatch.Alliances.Red.TeamKeys
	} else if allianceOverride == "Blue" {
		allianceName = "Blue"
		teamKeys = currentMatch.Alliances.Blue.TeamKeys
	} else {
		isEvenMatch := matchNum%2 == 0
		isEvenScouter := scouterID%2 == 0
		if isEvenMatch != isEvenScouter {
			allianceName = "Blue"
			teamKeys = currentMatch.Alliances.Blue.TeamKeys
		} else {
			teamKeys = currentMatch.Alliances.Red.TeamKeys
		}
	}

	teams := []string{}
	for _, tk := range teamKeys {
		if len(tk) > 3 {
			teams = append(teams, tk[3:])
		}
	}

	templates.ScoutPage(eventKey, strconv.Itoa(matchNum), strconv.Itoa(scouterID), allianceName, teams).Render(r.Context(), w)
}

func saveScoutDataHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Only POST allowed", http.StatusMethodNotAllowed)
		return
	}

	var sub ScoutSubmission
	if err := json.NewDecoder(r.Body).Decode(&sub); err != nil {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}

	for _, teamData := range sub.Teams {
		db.Exec(`
			INSERT INTO scout_submissions (event_key, match_num, scouter_id, team_number, notes)
			VALUES (?, ?, ?, ?, ?)`,
			sub.EventKey, sub.MatchNum, sub.ScouterID, teamData.TeamNumber, teamData.Notes)

		// Bust team analysis cache
		db.Exec(`DELETE FROM analysis_cache WHERE event_key = ? AND team_number = ?`,
			sub.EventKey, teamData.TeamNumber)
	}

	fmt.Printf("Saved match %d, scouter %d, %d teams\n", sub.MatchNum, sub.ScouterID, len(sub.Teams))
	w.WriteHeader(http.StatusOK)
}

// currentEventMap returns events within ±7 days of today, always including the
// test event so it's easy to find during development.
func currentEventMap() (map[string]string, error) {
	events, err := getEventsCached("2026")
	if err != nil {
		return nil, err
	}

	now := time.Now()
	start := now.AddDate(0, 0, -7)
	end := now.AddDate(0, 0, 7)

	m := map[string]string{
		testEventKey: "★ " + testEventName, // always first-ish and easy to spot
	}
	for _, e := range events {
		if e.Key == testEventKey {
			continue
		}
		t, err := time.Parse("2006-01-02", e.StartDate)
		if err != nil {
			continue
		}
		if t.After(start) && t.Before(end) {
			m[e.Key] = e.Name
		}
	}
	return m, nil
}

// ── Analysis ──────────────────────────────────────────────────────────────────

func geminiAnalysisPageHandler(w http.ResponseWriter, r *http.Request) {
	eventMap, err := currentEventMap()
	if err != nil {
		http.Error(w, "Could not load events", 500)
		return
	}

	data := templates.GeminiAnalysisPageData{
		Events:        eventMap,
		SelectedEvent: r.URL.Query().Get("event_key"),
	}
	templ.Handler(templates.GeminiAnalysisPage(data)).ServeHTTP(w, r)
}

func apiRunAnalysisHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Only POST allowed", http.StatusMethodNotAllowed)
		return
	}

	eventKey := r.FormValue("event_key")
	if eventKey == "" || eventKey == "none" {
		http.Error(w, "Event required", http.StatusBadRequest)
		return
	}

	rows, err := db.Query(`
		SELECT DISTINCT team_number FROM scout_submissions
		WHERE event_key = ?
		ORDER BY team_number`, eventKey)
	if err != nil {
		http.Error(w, "DB error", 500)
		return
	}
	var teams []string
	for rows.Next() {
		var t string
		rows.Scan(&t)
		teams = append(teams, t)
	}
	rows.Close()

	templates.GeminiAnalysisProgressContainer(teams, eventKey).Render(r.Context(), w)
}

func apiAnalyzeTeamHandler(w http.ResponseWriter, r *http.Request) {
	eventKey := r.URL.Query().Get("event_key")
	teamNum := r.URL.Query().Get("team_number")
	if eventKey == "" || teamNum == "" {
		http.Error(w, "event_key and team_number required", http.StatusBadRequest)
		return
	}

	card, err := getOrGenerateAnalysis(eventKey, teamNum)
	if err != nil {
		card = templates.TeamAnalysisCard{
			EventKey:   eventKey,
			TeamNumber: teamNum,
			Summary:    "Error generating analysis: " + err.Error(),
		}
	}

	templates.SingleTeamAnalysisCard(card).Render(r.Context(), w)
}

func apiTeamNotesHandler(w http.ResponseWriter, r *http.Request) {
	eventKey := r.URL.Query().Get("event_key")
	teamNum := r.URL.Query().Get("team_number")
	if eventKey == "" || teamNum == "" {
		http.Error(w, "event_key and team_number required", http.StatusBadRequest)
		return
	}

	rows, err := db.Query(`
		SELECT match_num, notes FROM scout_submissions
		WHERE event_key = ? AND team_number = ?
		ORDER BY match_num ASC`, eventKey, teamNum)
	if err != nil {
		http.Error(w, "db error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var notes []templates.TeamNote
	for rows.Next() {
		var n templates.TeamNote
		rows.Scan(&n.MatchNum, &n.Notes)
		notes = append(notes, n)
	}

	templates.TeamNotesPanel(notes).Render(r.Context(), w)
}

// teamAnalysisJSON is the structured response Gemini returns for team analysis.
type teamAnalysisJSON struct {
	Summary     string `json:"summary"`
	Scoring     int    `json:"scoring"`
	Reliability int    `json:"reliability"`
	Defense     int    `json:"defense"` // 0 = N/A
}

func getOrGenerateAnalysis(eventKey, teamNum string) (templates.TeamAnalysisCard, error) {
	rows, err := db.Query(`
		SELECT notes FROM scout_submissions
		WHERE event_key = ? AND team_number = ?
		ORDER BY match_num ASC`, eventKey, teamNum)
	if err != nil {
		return templates.TeamAnalysisCard{}, err
	}
	var notesList []string
	for rows.Next() {
		var n string
		rows.Scan(&n)
		notesList = append(notesList, n)
	}
	rows.Close()

	combined := strings.Join(notesList, "\n")
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(combined)))

	// Check cache
	var cachedJSON, cachedHash string
	err = db.QueryRow(`
		SELECT analysis, notes_hash FROM analysis_cache
		WHERE event_key = ? AND team_number = ?`,
		eventKey, teamNum).Scan(&cachedJSON, &cachedHash)

	if err == nil && cachedHash == hash {
		var result teamAnalysisJSON
		if jsonErr := json.Unmarshal([]byte(cachedJSON), &result); jsonErr == nil {
			return templates.TeamAnalysisCard{
				EventKey:    eventKey,
				TeamNumber:  teamNum,
				Summary:     result.Summary,
				Scoring:     result.Scoring,
				Reliability: result.Reliability,
				Defense:     result.Defense,
				FromCache:   true,
			}, nil
		}
		// If JSON parse fails, fall through to regenerate
	}

	result, err := callGeminiTeamAnalysis(teamNum, eventKey, combined)
	if err != nil {
		return templates.TeamAnalysisCard{}, err
	}

	resultJSON, _ := json.Marshal(result)
	db.Exec(`
		INSERT INTO analysis_cache (event_key, team_number, analysis, notes_hash)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(event_key, team_number) DO UPDATE SET
			analysis = excluded.analysis,
			notes_hash = excluded.notes_hash,
			created_at = CURRENT_TIMESTAMP`,
		eventKey, teamNum, string(resultJSON), hash)

	return templates.TeamAnalysisCard{
		EventKey:    eventKey,
		TeamNumber:  teamNum,
		Summary:     result.Summary,
		Scoring:     result.Scoring,
		Reliability: result.Reliability,
		Defense:     result.Defense,
		FromCache:   false,
	}, nil
}

// ── Match Planner ─────────────────────────────────────────────────────────────

func matchPlannerPageHandler(w http.ResponseWriter, r *http.Request) {
	eventMap, err := currentEventMap()
	if err != nil {
		http.Error(w, "Could not load events", 500)
		return
	}

	data := templates.MatchPlannerPageData{Events: eventMap}
	templ.Handler(templates.MatchPlannerPage(data)).ServeHTTP(w, r)
}

func apiMatchPlanHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Only POST allowed", http.StatusMethodNotAllowed)
		return
	}

	eventKey := r.FormValue("event_key")
	teamNumber := strings.TrimSpace(r.FormValue("team_number"))
	matchNum, _ := strconv.Atoi(r.FormValue("match_num"))
	if eventKey == "" || eventKey == "none" || teamNumber == "" || matchNum == 0 {
		http.Error(w, "Event, team number, and match number required", http.StatusBadRequest)
		return
	}

	matches, err := getMatchesCached(eventKey)
	if err != nil {
		http.Error(w, "Failed to fetch schedule", 500)
		return
	}

	// Find the specific quals match
	frcTeam := "frc" + teamNumber
	var targetMatch Match
	found := false
	for _, m := range matches {
		if m.CompLevel == "qm" && m.MatchNumber == matchNum {
			targetMatch = m
			found = true
			break
		}
	}

	if !found {
		templates.MatchPlannerResults(nil, teamNumber).Render(r.Context(), w)
		return
	}

	// Verify the team is actually in this match
	inMatch := false
	for _, tk := range append(targetMatch.Alliances.Red.TeamKeys, targetMatch.Alliances.Blue.TeamKeys...) {
		if tk == frcTeam {
			inMatch = true
			break
		}
	}
	if !inMatch {
		templates.MatchPlannerResults(nil, teamNumber).Render(r.Context(), w)
		return
	}

	card, err := getOrGenerateMatchPlan(eventKey, teamNumber, targetMatch)
	if err != nil {
		redTeams := stripFRC(targetMatch.Alliances.Red.TeamKeys)
		blueTeams := stripFRC(targetMatch.Alliances.Blue.TeamKeys)
		ourAlliance := "Red"
		for _, tk := range targetMatch.Alliances.Blue.TeamKeys {
			if tk == frcTeam {
				ourAlliance = "Blue"
				break
			}
		}
		card = templates.MatchPlanCard{
			MatchNum:    targetMatch.MatchNumber,
			OurAlliance: ourAlliance,
			RedTeams:    redTeams,
			BlueTeams:   blueTeams,
			Strategy:    "Error generating strategy: " + err.Error(),
		}
	}

	templates.MatchPlannerResults([]templates.MatchPlanCard{card}, teamNumber).Render(r.Context(), w)
}

func getOrGenerateMatchPlan(eventKey, teamNumber string, m Match) (templates.MatchPlanCard, error) {
	frcTeam := "frc" + teamNumber
	redTeams := stripFRC(m.Alliances.Red.TeamKeys)
	blueTeams := stripFRC(m.Alliances.Blue.TeamKeys)

	ourAlliance := "Red"
	for _, tk := range m.Alliances.Blue.TeamKeys {
		if tk == frcTeam {
			ourAlliance = "Blue"
			break
		}
	}

	// Collect notes for all 5 other teams, build hash
	allTeams := append(redTeams, blueTeams...)
	sort.Strings(allTeams)

	var noteParts []string      // for hash (scouting notes only)
	var contextParts []string   // for prompt (notes + EPA)
	for _, t := range allTeams {
		if t == teamNumber {
			continue
		}
		rows, _ := db.Query(`
			SELECT notes FROM scout_submissions
			WHERE event_key = ? AND team_number = ?
			ORDER BY match_num ASC`, eventKey, t)
		var notes []string
		for rows.Next() {
			var n string
			rows.Scan(&n)
			notes = append(notes, n)
		}
		rows.Close()
		noteLine := fmt.Sprintf("Team %s: %s", t, strings.Join(notes, " | "))
		noteParts = append(noteParts, noteLine)
		contextParts = append(contextParts, fmt.Sprintf("Team %s:\n  EPA:\n%s\n  Notes: %s",
			t, fetchStatboticsEPA(t), strings.Join(notes, " | ")))
	}

	combinedNotes := strings.Join(noteParts, "\n")
	notesContext := strings.Join(contextParts, "\n\n")
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(combinedNotes)))

	// Check cache
	var cachedStrategy, cachedHash string
	err := db.QueryRow(`
		SELECT strategy, notes_hash FROM match_plan_cache
		WHERE event_key = ? AND team_number = ? AND match_num = ?`,
		eventKey, teamNumber, m.MatchNumber).Scan(&cachedStrategy, &cachedHash)

	if err == nil && cachedHash == hash {
		return templates.MatchPlanCard{
			MatchNum:    m.MatchNumber,
			OurAlliance: ourAlliance,
			RedTeams:    redTeams,
			BlueTeams:   blueTeams,
			Strategy:    cachedStrategy,
			FromCache:   true,
		}, nil
	}

	strategy, err := callGeminiMatchPlan(teamNumber, eventKey, m.MatchNumber, ourAlliance, redTeams, blueTeams, notesContext)
	if err != nil {
		return templates.MatchPlanCard{}, err
	}

	db.Exec(`
		INSERT INTO match_plan_cache (event_key, team_number, match_num, strategy, notes_hash)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(event_key, team_number, match_num) DO UPDATE SET
			strategy = excluded.strategy,
			notes_hash = excluded.notes_hash,
			created_at = CURRENT_TIMESTAMP`,
		eventKey, teamNumber, m.MatchNumber, strategy, hash)

	return templates.MatchPlanCard{
		MatchNum:    m.MatchNumber,
		OurAlliance: ourAlliance,
		RedTeams:    redTeams,
		BlueTeams:   blueTeams,
		Strategy:    strategy,
		FromCache:   false,
	}, nil
}

func stripFRC(keys []string) []string {
	result := make([]string, 0, len(keys))
	for _, k := range keys {
		if len(k) > 3 {
			result = append(result, k[3:])
		}
	}
	return result
}

// ── Statbotics EPA ────────────────────────────────────────────────────────────

var (
	epaCache   = map[string]string{}
	epaCacheMu sync.RWMutex
	httpClient = &http.Client{Timeout: 5 * time.Second}
)

func fetchStatboticsEPA(teamNum string) string {
	epaCacheMu.RLock()
	if v, ok := epaCache[teamNum]; ok {
		epaCacheMu.RUnlock()
		return v
	}
	epaCacheMu.RUnlock()

	year := time.Now().Year()
	url := fmt.Sprintf("https://api.statbotics.io/v3/team_year/%s/%d", teamNum, year)
	resp, err := httpClient.Get(url)
	if err != nil || resp.StatusCode != 200 {
		return "unavailable"
	}
	defer resp.Body.Close()

	var data struct {
		EPA struct {
			Breakdown map[string]float64 `json:"breakdown"`
		} `json:"epa"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil || len(data.EPA.Breakdown) == 0 {
		return "unavailable"
	}

	keys := make([]string, 0, len(data.EPA.Breakdown))
	for k := range data.EPA.Breakdown {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var lines []string
	for _, k := range keys {
		lines = append(lines, fmt.Sprintf("  %s: %.2f", k, data.EPA.Breakdown[k]))
	}
	result := strings.Join(lines, "\n")

	epaCacheMu.Lock()
	epaCache[teamNum] = result
	epaCacheMu.Unlock()

	return result
}

// ── Gemini helpers ────────────────────────────────────────────────────────────

const geminiURL = "https://generativelanguage.googleapis.com/v1beta/models/gemini-3.1-flash-lite-preview:generateContent"

func geminiPost(prompt string) (string, error) {
	apiKey := os.Getenv("GEMINI_API_KEY")
	if apiKey == "" {
		return "", fmt.Errorf("GEMINI_API_KEY not set")
	}

	payload := map[string]interface{}{
		"contents": []map[string]interface{}{
			{"parts": []map[string]string{{"text": prompt}}},
		},
	}

	body, _ := json.Marshal(payload)
	resp, err := http.Post(
		fmt.Sprintf("%s?key=%s", geminiURL, apiKey),
		"application/json",
		bytes.NewBuffer(body),
	)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	var geminiResp struct {
		Candidates []struct {
			FinishReason string `json:"finishReason"`
			Content      struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
		PromptFeedback struct {
			BlockReason string `json:"blockReason"`
		} `json:"promptFeedback"`
	}
	if err := json.Unmarshal(respBody, &geminiResp); err != nil {
		return "", fmt.Errorf("gemini parse error: %v — body: %s", err, string(respBody))
	}
	if geminiResp.PromptFeedback.BlockReason != "" {
		return "", fmt.Errorf("gemini blocked prompt: %s", geminiResp.PromptFeedback.BlockReason)
	}
	if len(geminiResp.Candidates) == 0 {
		return "", fmt.Errorf("gemini returned no candidates — body: %s", string(respBody))
	}
	c := geminiResp.Candidates[0]
	if len(c.Content.Parts) == 0 {
		return "", fmt.Errorf("gemini candidate has no content (finishReason: %s)", c.FinishReason)
	}
	return c.Content.Parts[0].Text, nil
}

type teamAnalysisPromptData struct {
	TeamNum      string
	EventKey     string
	Notes        string
	EPABreakdown string
}

func callGeminiTeamAnalysis(teamNum, eventKey, notes string) (teamAnalysisJSON, error) {
	tmpl, err := template.New("team_analysis").Parse(teamAnalysisPromptTmpl)
	if err != nil {
		return teamAnalysisJSON{}, fmt.Errorf("failed to parse team analysis prompt: %w", err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, teamAnalysisPromptData{
		TeamNum:      teamNum,
		EventKey:     eventKey,
		Notes:        notes,
		EPABreakdown: fetchStatboticsEPA(teamNum),
	}); err != nil {
		return teamAnalysisJSON{}, fmt.Errorf("failed to render team analysis prompt: %w", err)
	}

	raw, err := geminiPost(buf.String())
	if err != nil {
		return teamAnalysisJSON{}, err
	}

	// Strip markdown code fences if present
	raw = strings.TrimSpace(raw)
	raw = strings.TrimPrefix(raw, "```json")
	raw = strings.TrimPrefix(raw, "```")
	raw = strings.TrimSuffix(raw, "```")
	raw = strings.TrimSpace(raw)

	var result teamAnalysisJSON
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		return teamAnalysisJSON{}, fmt.Errorf("failed to parse analysis JSON: %v — raw: %s", err, raw)
	}
	return result, nil
}

type matchPlanPromptData struct {
	TeamNum          string
	MatchNum         int
	EventKey         string
	OurAlliance      string
	OpponentAlliance string
	Partners         string
	Opponents        string
	OurEPA           string
	NotesContext     string
}

func callGeminiMatchPlan(teamNum, eventKey string, matchNum int, ourAlliance string, redTeams, blueTeams []string, notesContext string) (string, error) {
	alliancePartners := redTeams
	opponents := blueTeams
	if ourAlliance == "Blue" {
		alliancePartners = blueTeams
		opponents = redTeams
	}

	var partners []string
	for _, t := range alliancePartners {
		if t != teamNum {
			partners = append(partners, t)
		}
	}

	opponentAlliance := "Blue"
	if ourAlliance == "Blue" {
		opponentAlliance = "Red"
	}

	tmpl, err := template.New("match_plan").Parse(matchPlanPromptTmpl)
	if err != nil {
		return "", fmt.Errorf("failed to parse match plan prompt: %w", err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, matchPlanPromptData{
		TeamNum:          teamNum,
		MatchNum:         matchNum,
		EventKey:         eventKey,
		OurAlliance:      ourAlliance,
		OpponentAlliance: opponentAlliance,
		Partners:         strings.Join(partners, ", "),
		Opponents:        strings.Join(opponents, ", "),
		OurEPA:           fetchStatboticsEPA(teamNum),
		NotesContext:     notesContext,
	}); err != nil {
		return "", fmt.Errorf("failed to render match plan prompt: %w", err)
	}

	return geminiPost(buf.String())
}

// ── AI Fill-in Scout ──────────────────────────────────────────────────────────

const geminiVideoURL = "https://generativelanguage.googleapis.com/v1beta/models/gemini-2.0-flash:generateContent"

func geminiVideoPost(videoURI, prompt string) (string, error) {
	apiKey := os.Getenv("GEMINI_API_KEY")
	if apiKey == "" {
		return "", fmt.Errorf("GEMINI_API_KEY not set")
	}

	payload := map[string]interface{}{
		"contents": []map[string]interface{}{
			{
				"parts": []map[string]interface{}{
					{
						"fileData": map[string]string{
							"mimeType": "video/mp4",
							"fileUri":  videoURI,
						},
					},
					{"text": prompt},
				},
			},
		},
	}

	body, _ := json.Marshal(payload)
	resp, err := http.Post(
		fmt.Sprintf("%s?key=%s", geminiVideoURL, apiKey),
		"application/json",
		bytes.NewBuffer(body),
	)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	var geminiResp struct {
		Candidates []struct {
			FinishReason string `json:"finishReason"`
			Content      struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
		PromptFeedback struct {
			BlockReason string `json:"blockReason"`
		} `json:"promptFeedback"`
	}
	if err := json.Unmarshal(respBody, &geminiResp); err != nil {
		return "", fmt.Errorf("gemini parse error: %v — body: %s", err, string(respBody))
	}
	if geminiResp.PromptFeedback.BlockReason != "" {
		return "", fmt.Errorf("gemini blocked prompt: %s", geminiResp.PromptFeedback.BlockReason)
	}
	if len(geminiResp.Candidates) == 0 {
		return "", fmt.Errorf("gemini returned no candidates — body: %s", string(respBody))
	}
	c := geminiResp.Candidates[0]
	if len(c.Content.Parts) == 0 {
		return "", fmt.Errorf("gemini candidate has no content (finishReason: %s)", c.FinishReason)
	}
	return c.Content.Parts[0].Text, nil
}

type videoScoutPromptData struct {
	TeamNum  string
	MatchNum int
	EventKey string
}

func callGeminiVideoScout(teamNum, eventKey string, matchNum int, videoURI string) (string, error) {
	tmpl, err := template.New("video_scout").Parse(videoScoutPromptTmpl)
	if err != nil {
		return "", fmt.Errorf("failed to parse video scout prompt: %w", err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, videoScoutPromptData{
		TeamNum:  teamNum,
		MatchNum: matchNum,
		EventKey: eventKey,
	}); err != nil {
		return "", fmt.Errorf("failed to render video scout prompt: %w", err)
	}
	return geminiVideoPost(videoURI, buf.String())
}

// apiFillAIScoutHandler receives event_key, match_num, youtube_url and returns
// a progress container with per-team htmx slots.
func apiFillAIScoutHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Only POST allowed", http.StatusMethodNotAllowed)
		return
	}

	eventKey := r.FormValue("event_key")
	matchNum, _ := strconv.Atoi(r.FormValue("match_num"))
	youtubeURL := strings.TrimSpace(r.FormValue("youtube_url"))

	if eventKey == "" || matchNum == 0 || youtubeURL == "" {
		http.Error(w, "event_key, match_num, and youtube_url are required", http.StatusBadRequest)
		return
	}

	matches, err := getMatchesCached(eventKey)
	if err != nil {
		http.Error(w, "Failed to fetch match schedule: "+err.Error(), http.StatusInternalServerError)
		return
	}

	var targetMatch Match
	found := false
	for _, m := range matches {
		if m.CompLevel == "qm" && m.MatchNumber == matchNum {
			targetMatch = m
			found = true
			break
		}
	}
	if !found {
		http.Error(w, fmt.Sprintf("Match %d not found in event %s", matchNum, eventKey), http.StatusNotFound)
		return
	}

	allTeamKeys := append(targetMatch.Alliances.Red.TeamKeys, targetMatch.Alliances.Blue.TeamKeys...)
	allTeams := stripFRC(allTeamKeys)

	encodedURL := url.QueryEscape(youtubeURL)
	var slots []templates.AiFillSlot
	for _, team := range allTeams {
		slots = append(slots, templates.AiFillSlot{
			Team: team,
			HXURL: fmt.Sprintf(
				"/api/admin/fill-ai-scout-team?event_key=%s&match_num=%d&team_number=%s&youtube_url=%s",
				url.QueryEscape(eventKey), matchNum, url.QueryEscape(team), encodedURL,
			),
		})
	}

	templates.AiFillProgressContainer(slots).Render(r.Context(), w)
}

// apiFillAIScoutTeamHandler processes one team: checks for existing data, calls
// Gemini video analysis, saves with ai_generated=1 if no prior data exists.
func apiFillAIScoutTeamHandler(w http.ResponseWriter, r *http.Request) {
	eventKey := r.URL.Query().Get("event_key")
	matchNum, _ := strconv.Atoi(r.URL.Query().Get("match_num"))
	teamNum := r.URL.Query().Get("team_number")
	youtubeURL := r.URL.Query().Get("youtube_url")

	if eventKey == "" || matchNum == 0 || teamNum == "" || youtubeURL == "" {
		http.Error(w, "missing parameters", http.StatusBadRequest)
		return
	}

	// Check if this team already has a human scout entry for this match
	var existing int
	db.QueryRow(`
		SELECT COUNT(*) FROM scout_submissions
		WHERE event_key = ? AND match_num = ? AND team_number = ? AND (ai_generated = 0 OR ai_generated IS NULL)`,
		eventKey, matchNum, teamNum).Scan(&existing)

	if existing > 0 {
		templates.AiFillTeamResult(templates.AiFillTeamResultData{Team: teamNum, Skipped: true}).Render(r.Context(), w)
		return
	}

	notes, err := callGeminiVideoScout(teamNum, eventKey, matchNum, youtubeURL)
	if err != nil {
		templates.AiFillTeamResult(templates.AiFillTeamResultData{Team: teamNum, Notes: err.Error(), Success: false}).Render(r.Context(), w)
		return
	}

	db.Exec(`
		INSERT INTO scout_submissions (event_key, match_num, scouter_id, team_number, notes, ai_generated)
		VALUES (?, ?, ?, ?, ?, 1)`,
		eventKey, matchNum, 0, teamNum, strings.TrimSpace(notes))

	// Bust analysis cache so this team gets re-analyzed with new data
	db.Exec(`DELETE FROM analysis_cache WHERE event_key = ? AND team_number = ?`, eventKey, teamNum)

	templates.AiFillTeamResult(templates.AiFillTeamResultData{Team: teamNum, Notes: notes, Success: true}).Render(r.Context(), w)
}

// ── Admin ─────────────────────────────────────────────────────────────────────

func adminHandler(w http.ResponseWriter, r *http.Request) {
	var events []string
	if rows, err := db.Query("SELECT DISTINCT event_key FROM scout_submissions"); err == nil {
		for rows.Next() {
			var eventKey string
			rows.Scan(&eventKey)
			events = append(events, eventKey)
		}
		rows.Close()
	}

	component := templates.AdminPage(events)
	templ.Handler(component).ServeHTTP(w, r)
}

func clearEventHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Only POST allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		EventKey string `json:"event_key"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	if req.EventKey == "" {
		http.Error(w, "Event key required", http.StatusBadRequest)
		return
	}

	db.Exec("DELETE FROM scout_submissions WHERE event_key = ?", req.EventKey)
	db.Exec("DELETE FROM analysis_cache WHERE event_key = ?", req.EventKey)
	db.Exec("DELETE FROM match_plan_cache WHERE event_key = ?", req.EventKey)

	fmt.Fprintf(w, "Deleted all data for event: %s", req.EventKey)
}

func seedTestHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Only POST allowed", http.StatusMethodNotAllowed)
		return
	}
	seedTestData()
	fmt.Fprintf(w, "Test event seeded: %s (%d observations across 9 teams)", testEventKey, len(testObservations))
}

func clearAllHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Only POST allowed", http.StatusMethodNotAllowed)
		return
	}

	db.Exec("DELETE FROM scout_submissions")
	db.Exec("DELETE FROM analysis_cache")
	db.Exec("DELETE FROM match_plan_cache")

	fmt.Fprintf(w, "Deleted all data from database")
}
