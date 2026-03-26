package main

//go:generate templ generate

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"vibe-scout/templates"

	"github.com/a-h/templ"

	_ "github.com/glebarez/go-sqlite"
)

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
	initDB()

	http.Handle("/", http.HandlerFunc(homeHandler))
	http.HandleFunc("/scout", scoutHandler)
	http.HandleFunc("/api/save-scout", saveScoutDataHandler)
	http.HandleFunc("/analysis", geminiAnalysisPageHandler)
	http.HandleFunc("/api/run-analysis", apiRunAnalysisHandler)
	http.HandleFunc("/match-planner", matchPlannerPageHandler)
	http.HandleFunc("/api/match-plan", apiMatchPlanHandler)
	http.HandleFunc("/admin", adminHandler)
	http.HandleFunc("/api/admin/clear-event", clearEventHandler)
	http.HandleFunc("/api/admin/clear-all", clearAllHandler)

	fmt.Println("Vibe Scout v2 running on http://localhost:8080")
	http.ListenAndServe(":8080", nil)
}

func homeHandler(w http.ResponseWriter, r *http.Request) {
	events, err := getEventsCached("2026")
	if err != nil {
		http.Error(w, "Could not load events", 500)
		return
	}

	eventMap := make(map[string]string)
	now := time.Now()
	startOfWindow := now.AddDate(0, 0, -7)
	endOfWindow := now.AddDate(0, 0, 7)

	for _, e := range events {
		eventTime, err := time.Parse("2006-01-02", e.StartDate)
		if err != nil {
			continue
		}
		if eventTime.After(startOfWindow) && eventTime.Before(endOfWindow) {
			eventMap[e.Key] = e.Name
		}
	}

	if len(eventMap) == 0 {
		eventMap["none"] = "No events found for this week"
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

// ── Analysis ──────────────────────────────────────────────────────────────────

func geminiAnalysisPageHandler(w http.ResponseWriter, r *http.Request) {
	events, err := getEventsCached("2026")
	if err != nil {
		http.Error(w, "Could not load events", 500)
		return
	}

	eventMap := make(map[string]string)
	for _, e := range events {
		eventMap[e.Key] = e.Name
	}
	if len(eventMap) == 0 {
		eventMap["none"] = "No events found"
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

	var cards []templates.TeamAnalysisCard
	for _, teamNum := range teams {
		card, err := getOrGenerateAnalysis(eventKey, teamNum)
		if err != nil {
			cards = append(cards, templates.TeamAnalysisCard{
				TeamNumber: teamNum,
				Summary:    "Error generating analysis: " + err.Error(),
			})
			continue
		}
		cards = append(cards, card)
	}

	templates.GeminiAnalysisResults(cards).Render(r.Context(), w)
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
	events, err := getEventsCached("2026")
	if err != nil {
		http.Error(w, "Could not load events", 500)
		return
	}

	eventMap := make(map[string]string)
	for _, e := range events {
		eventMap[e.Key] = e.Name
	}
	if len(eventMap) == 0 {
		eventMap["none"] = "No events found"
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
	if eventKey == "" || eventKey == "none" || teamNumber == "" {
		http.Error(w, "Event and team number required", http.StatusBadRequest)
		return
	}

	matches, err := getMatchesCached(eventKey)
	if err != nil {
		http.Error(w, "Failed to fetch schedule", 500)
		return
	}

	// Find all quals matches involving this team
	frcTeam := "frc" + teamNumber
	var teamMatches []Match
	for _, m := range matches {
		if m.CompLevel != "qm" {
			continue
		}
		for _, tk := range m.Alliances.Red.TeamKeys {
			if tk == frcTeam {
				teamMatches = append(teamMatches, m)
				break
			}
		}
		for _, tk := range m.Alliances.Blue.TeamKeys {
			if tk == frcTeam {
				teamMatches = append(teamMatches, m)
				break
			}
		}
	}

	// Sort by match number
	sort.Slice(teamMatches, func(i, j int) bool {
		return teamMatches[i].MatchNumber < teamMatches[j].MatchNumber
	})

	var cards []templates.MatchPlanCard
	for _, m := range teamMatches {
		card, err := getOrGenerateMatchPlan(eventKey, teamNumber, m)
		if err != nil {
			redTeams := stripFRC(m.Alliances.Red.TeamKeys)
			blueTeams := stripFRC(m.Alliances.Blue.TeamKeys)
			ourAlliance := "Red"
			for _, tk := range m.Alliances.Blue.TeamKeys {
				if tk == frcTeam {
					ourAlliance = "Blue"
					break
				}
			}
			cards = append(cards, templates.MatchPlanCard{
				MatchNum:    m.MatchNumber,
				OurAlliance: ourAlliance,
				RedTeams:    redTeams,
				BlueTeams:   blueTeams,
				Strategy:    "Error generating strategy: " + err.Error(),
			})
			continue
		}
		cards = append(cards, card)
	}

	templates.MatchPlannerResults(cards, teamNumber).Render(r.Context(), w)
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

	var noteParts []string
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
		noteParts = append(noteParts, fmt.Sprintf("Team %s: %s", t, strings.Join(notes, " | ")))
	}

	combinedNotes := strings.Join(noteParts, "\n")
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

	strategy, err := callGeminiMatchPlan(teamNumber, eventKey, m.MatchNumber, ourAlliance, redTeams, blueTeams, combinedNotes)
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

// ── Gemini helpers ────────────────────────────────────────────────────────────

const geminiURL = "https://generativelanguage.googleapis.com/v1beta/models/gemini-2.0-flash-lite:generateContent"

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
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
	}
	if err := json.Unmarshal(respBody, &geminiResp); err != nil {
		return "", fmt.Errorf("gemini parse error: %v — body: %s", err, string(respBody))
	}
	if len(geminiResp.Candidates) == 0 || len(geminiResp.Candidates[0].Content.Parts) == 0 {
		return "", fmt.Errorf("gemini returned empty response")
	}
	return geminiResp.Candidates[0].Content.Parts[0].Text, nil
}

func callGeminiTeamAnalysis(teamNum, eventKey, notes string) (teamAnalysisJSON, error) {
	prompt := fmt.Sprintf(
		`You are a FIRST Robotics scouting analyst. Here are scouting notes for Team %s at %s.

Respond ONLY with a JSON object in exactly this format (no markdown, no explanation):
{"summary":"<2-3 sentence analysis>","scoring":<1-10>,"reliability":<1-10>,"defense":<0-10>}

scoring = offensive scoring ability (1=poor, 10=excellent)
reliability = consistency and mechanical reliability (1=very unreliable, 10=very reliable)
defense = defensive ability (0=no defense observed/N/A, 1-10 if they played defense)

Notes:
%s`,
		teamNum, eventKey, notes,
	)

	raw, err := geminiPost(prompt)
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

func callGeminiMatchPlan(teamNum, eventKey string, matchNum int, ourAlliance string, redTeams, blueTeams []string, notesContext string) (string, error) {
	alliancePartners := redTeams
	opponents := blueTeams
	if ourAlliance == "Blue" {
		alliancePartners = blueTeams
		opponents = redTeams
	}

	// Remove our team from partners list
	var partners []string
	for _, t := range alliancePartners {
		if t != teamNum {
			partners = append(partners, t)
		}
	}

	partnersStr := strings.Join(partners, ", ")
	opponentsStr := strings.Join(opponents, ", ")

	prompt := fmt.Sprintf(
		`You are a FIRST Robotics strategy analyst helping Team %s prepare for Match %d at %s.

%s Alliance (our side): %s, %s
%s Alliance (opponents): %s

Scouted data on other teams:
%s

Provide a concise match strategy for Team %s (3-5 bullet points covering: recommended role, key threats from opponents, coordination with partners, and any specific tactical notes).`,
		teamNum, matchNum, eventKey,
		ourAlliance, teamNum, partnersStr,
		func() string {
			if ourAlliance == "Red" {
				return "Blue"
			}
			return "Red"
		}(),
		opponentsStr,
		notesContext,
		teamNum,
	)

	return geminiPost(prompt)
}

// ── Admin ─────────────────────────────────────────────────────────────────────

func adminHandler(w http.ResponseWriter, r *http.Request) {
	rows, _ := db.Query("SELECT DISTINCT event_key FROM scout_submissions")
	var events []string
	for rows.Next() {
		var eventKey string
		rows.Scan(&eventKey)
		events = append(events, eventKey)
	}
	rows.Close()

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
