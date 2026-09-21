package main

//go:generate templ generate

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
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
	EventKey    string          `json:"event_key"`
	MatchNum    int             `json:"match_num"`
	ScouterName string          `json:"scouter_name"`
	Teams       []TeamScoutData `json:"teams"`
}

type TeamScoutData struct {
	TeamNumber    string `json:"team_number"`
	Notes         string `json:"notes"`
	HasChecklist  bool   `json:"has_checklist"` // only one-robot mode sends a checklist
	Broke         bool   `json:"broke"`
	PlayedDefense bool   `json:"played_defense"`
	WasDefended   bool   `json:"was_defended"`
	AutoType      string `json:"auto_type"`
}

// hasScoutDataSQL matches submissions with something in them: written notes or
// a checklist.
const hasScoutDataSQL = `(TRIM(notes) != '' OR COALESCE(has_checklist, 0) = 1)`

// scoutNoteColumns selects a submission's notes plus its checklist, for scanScoutNote.
const scoutNoteColumns = `notes, COALESCE(has_checklist, 0), COALESCE(broke, 0), COALESCE(played_defense, 0), COALESCE(was_defended, 0), COALESCE(auto_type, '')`

// scanScoutNote scans scoutNoteColumns and returns the text Gemini sees for one
// submission. Rows without a checklist return just their notes, so older cached
// analyses stay valid.
func scanScoutNote(rows *sql.Rows) (string, error) {
	var notes, autoType string
	var hasChecklist, broke, playedDefense, wasDefended bool
	if err := rows.Scan(&notes, &hasChecklist, &broke, &playedDefense, &wasDefended, &autoType); err != nil {
		return "", err
	}
	if !hasChecklist {
		return notes, nil
	}
	yesNo := func(b bool) string {
		if b {
			return "yes"
		}
		return "no"
	}
	auto := strings.TrimSpace(autoType)
	if auto == "" {
		auto = "not recorded"
	}
	checklist := fmt.Sprintf("[Auto: %s; Broke: %s; Played defense: %s; Was defended: %s]",
		auto, yesNo(broke), yesNo(playedDefense), yesNo(wasDefended))
	if strings.TrimSpace(notes) == "" {
		return checklist, nil
	}
	return notes + " " + checklist, nil
}

const ourTeam = templates.OurTeam

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

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		homeHandler(w, r)
	})
	http.HandleFunc("/field-scout", fieldScoutHandler)
	http.HandleFunc("/scout", scoutHandler)
	http.HandleFunc("/api/match-teams", apiMatchTeamsHandler)
	http.HandleFunc("/api/match-alliances", apiMatchAlliancesHandler)
	http.HandleFunc("/api/save-scout", saveScoutDataHandler)
	http.HandleFunc("/api/voice-scout", voiceScoutWSHandler)
	http.HandleFunc("/api/sort-notes", sortNotesHandler)
	http.HandleFunc("/static/voice-scout.js", voiceScoutJSHandler)
	http.HandleFunc("/pit-scout", pitScoutPageHandler)
	http.HandleFunc("/api/save-pit-scout", savePitScoutHandler)
	http.HandleFunc("/api/pit-teams", apiPitTeamsHandler)
	http.HandleFunc("/api/pit-note", apiPitNoteHandler)
	http.HandleFunc("/analysis", geminiAnalysisPageHandler)
	http.HandleFunc("/api/run-analysis", apiRunAnalysisHandler)
	http.HandleFunc("/api/analyze-team", apiAnalyzeTeamHandler)
	http.HandleFunc("/api/next-match", apiNextMatchHandler)
	http.HandleFunc("/api/team-notes", apiTeamNotesHandler)
	http.HandleFunc("/api/team-pit-notes", apiTeamPitNotesHandler)
	http.HandleFunc("/api/search-teams", apiSearchTeamsHandler)
	http.HandleFunc("/match-planner", matchPlannerPageHandler)
	http.HandleFunc("/api/match-plan", apiMatchPlanHandler)
	http.HandleFunc("/510c53c3", adminHandler)
	http.HandleFunc("/api/admin/clear-event", clearEventHandler)
	http.HandleFunc("/api/admin/clear-all", clearAllHandler)
	http.HandleFunc("/api/admin/seed-test", seedTestHandler)
	http.HandleFunc("/api/admin/fill-ai-scout", apiFillAIScoutHandler)
	http.HandleFunc("/api/admin/fill-ai-scout-team", apiFillAIScoutTeamHandler)

	go func() {
		if _, err := getEventsCached("2026"); err != nil {
			log.Printf("prefetch events: %v", err)
		}
	}()

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	fmt.Printf("Vibe Scout v2 running on http://localhost:%s\n", port)
	log.Fatal(http.ListenAndServe(":"+port, nil))
}

func homeHandler(w http.ResponseWriter, r *http.Request) {
	eventMap, err := currentEventMap()
	if err != nil {
		log.Printf("home events: %v", err)
		eventMap = map[string]string{testEventKey: "★ " + testEventName}
	}

	component := templates.Home(eventMap, r.URL.Query().Get("event_key"))
	templ.Handler(component).ServeHTTP(w, r)
}

func fieldScoutHandler(w http.ResponseWriter, r *http.Request) {
	eventKey, ok := requireEvent(w, r)
	if !ok {
		return
	}

	matchNum, _ := strconv.Atoi(r.URL.Query().Get("match_num"))
	if matchNum < 1 {
		matchNum = 1
	}
	mode := r.URL.Query().Get("mode")
	if mode != "one" {
		mode = "three"
	}

	templ.Handler(templates.FieldScout(templates.FieldScoutData{
		EventKey:    eventKey,
		EventName:   eventNameFor(eventKey),
		MatchNum:    matchNum,
		ScouterName: normalizeScouterName(r.URL.Query().Get("scouter")),
		Scouters:    pastScouters(),
		Mode:        mode,
	})).ServeHTTP(w, r)
}

const maxScouterNameLen = 40

// normalizeScouterName trims and collapses whitespace and caps the length.
func normalizeScouterName(name string) string {
	name = strings.Join(strings.Fields(name), " ")
	if r := []rune(name); len(r) > maxScouterNameLen {
		name = string(r[:maxScouterNameLen])
	}
	return name
}

// canonicalScouterName reuses an existing scouter's spelling when the name only
// differs by capitalization, so "alex" and "Alex" count as the same scouter.
func canonicalScouterName(name string) string {
	name = normalizeScouterName(name)
	if name == "" {
		return ""
	}
	var existing string
	err := db.QueryRow(`
		SELECT scouter_name FROM scout_submissions
		WHERE scouter_name = ? COLLATE NOCASE
		ORDER BY created_at DESC LIMIT 1`, name).Scan(&existing)
	if err == nil && existing != "" {
		return existing
	}
	return name
}

// pastScouters lists every scouter name used before, most recently active first.
func pastScouters() []string {
	rows, err := db.Query(`
		SELECT scouter_name FROM scout_submissions
		WHERE scouter_name != ''
		GROUP BY scouter_name
		ORDER BY MAX(created_at) DESC`)
	if err != nil {
		log.Printf("past scouters: %v", err)
		return nil
	}
	defer rows.Close()

	var names []string
	for rows.Next() {
		var n string
		rows.Scan(&n)
		names = append(names, n)
	}
	return names
}

// requireEvent returns the event_key query param. The event is only chosen on the
// home page, so if it's missing the user is sent back there and ok is false.
func requireEvent(w http.ResponseWriter, r *http.Request) (eventKey string, ok bool) {
	eventKey = r.URL.Query().Get("event_key")
	if eventKey == "" {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return "", false
	}
	return eventKey, true
}

// eventNameFor returns the display name for an event, falling back to its key.
func eventNameFor(eventKey string) string {
	if eventMap, err := currentEventMap(); err == nil {
		if name, ok := eventMap[eventKey]; ok {
			return name
		}
	}
	return eventKey
}

func scoutHandler(w http.ResponseWriter, r *http.Request) {
	eventKey := r.URL.Query().Get("event_key")
	matchNum, _ := strconv.Atoi(r.URL.Query().Get("match_num"))
	scouterName := normalizeScouterName(r.URL.Query().Get("scouter"))
	pickedTeam := r.URL.Query().Get("team")         // set in one-robot mode
	pickedAlliance := r.URL.Query().Get("alliance") // set in three-robot mode

	matches, err := getMatchesCached(eventKey)
	if err != nil {
		http.Error(w, "Failed to fetch schedule", 500)
		return
	}

	currentMatch, found := findQualMatch(matches, matchNum)
	if !found {
		http.Error(w, fmt.Sprintf("Match %d not found", matchNum), 404)
		return
	}

	red := stripFRC(currentMatch.Alliances.Red.TeamKeys)
	blue := stripFRC(currentMatch.Alliances.Blue.TeamKeys)

	var teams []templates.ScoutTeam
	for _, t := range red {
		teams = append(teams, templates.ScoutTeam{Number: t, Alliance: "Red"})
	}
	for _, t := range blue {
		teams = append(teams, templates.ScoutTeam{Number: t, Alliance: "Blue"})
	}

	if pickedTeam != "" {
		var picked []templates.ScoutTeam
		for _, t := range teams {
			if t.Number == pickedTeam {
				picked = append(picked, t)
				break
			}
		}
		if len(picked) == 0 {
			http.Error(w, fmt.Sprintf("Team %s is not in match %d", pickedTeam, matchNum), 404)
			return
		}
		teams = picked
	} else if pickedAlliance != "" {
		var picked []templates.ScoutTeam
		for _, t := range teams {
			if t.Alliance == pickedAlliance {
				picked = append(picked, t)
			}
		}
		if len(picked) == 0 {
			http.Error(w, fmt.Sprintf("Alliance %q is not in match %d", pickedAlliance, matchNum), 404)
			return
		}
		teams = picked
	}

	for i := range teams {
		db.QueryRow(`SELECT COUNT(*) FROM scout_submissions WHERE event_key = ? AND team_number = ? AND `+hasScoutDataSQL,
			eventKey, teams[i].Number).Scan(&teams[i].DataCount)
	}

	templates.ScoutPage(eventKey, strconv.Itoa(matchNum), scouterName, pickedTeam != "", teams).Render(r.Context(), w)
}

// scoutingCoverage returns how many different matches a team has been scouted in
// at the event, and how many of its quals come before matchNum.
func scoutingCoverage(eventKey string, matches []Match, matchNum int, team string) (scouted, playedBefore int) {
	db.QueryRow(`SELECT COUNT(DISTINCT match_num) FROM scout_submissions
		WHERE event_key = ? AND team_number = ? AND `+hasScoutDataSQL, eventKey, team).Scan(&scouted)

	key := "frc" + team
	for _, m := range matches {
		if m.CompLevel != "qm" || m.MatchNumber >= matchNum {
			continue
		}
		for _, k := range append(append([]string{}, m.Alliances.Red.TeamKeys...), m.Alliances.Blue.TeamKeys...) {
			if k == key {
				playedBefore++
				break
			}
		}
	}
	return scouted, playedBefore
}

func findQualMatch(matches []Match, matchNum int) (Match, bool) {
	for _, m := range matches {
		if m.CompLevel == "qm" && m.MatchNumber == matchNum {
			return m, true
		}
	}
	return Match{}, false
}

// nextQualWith returns the number of the first qual match after `after` in which
// both teams play (as partners or opponents), or 0 if there isn't one.
func nextQualWith(matches []Match, after int, team, other string) int {
	next := 0
	for _, m := range matches {
		if m.CompLevel != "qm" || m.MatchNumber <= after || (next != 0 && m.MatchNumber >= next) {
			continue
		}
		keys := append(append([]string{}, m.Alliances.Red.TeamKeys...), m.Alliances.Blue.TeamKeys...)
		hasTeam, hasOther := false, false
		for _, k := range keys {
			hasTeam = hasTeam || k == "frc"+team
			hasOther = hasOther || k == "frc"+other
		}
		if hasTeam && hasOther {
			next = m.MatchNumber
		}
	}
	return next
}

// apiMatchTeamsHandler renders the one-robot picker for a match: all six teams,
// with the ones that play with or against our team soonest highlighted.
func apiMatchTeamsHandler(w http.ResponseWriter, r *http.Request) {
	eventKey := r.URL.Query().Get("event_key")
	matchNum, _ := strconv.Atoi(r.URL.Query().Get("match_num"))

	matches, err := getMatchesCached(eventKey)
	if err != nil {
		templates.MatchTeamPicker(nil, matchNum, "Couldn't load the match schedule.").Render(r.Context(), w)
		return
	}
	m, found := findQualMatch(matches, matchNum)
	if !found {
		templates.MatchTeamPicker(nil, matchNum, fmt.Sprintf("Match %d isn't in the schedule.", matchNum)).Render(r.Context(), w)
		return
	}

	var picks []templates.PickTeam
	soonest := 0
	add := func(keys []string, alliance string) {
		for _, t := range stripFRC(keys) {
			p := templates.PickTeam{Number: t, Alliance: alliance, IsUs: t == ourTeam}
			if !p.IsUs {
				p.Scouted, p.PlayedBefore = scoutingCoverage(eventKey, matches, matchNum, t)
				p.NeedsData = p.Scouted*2 < p.PlayedBefore
				p.NextWithUs = nextQualWith(matches, matchNum, t, ourTeam)
				if p.NextWithUs != 0 && (soonest == 0 || p.NextWithUs < soonest) {
					soonest = p.NextWithUs
				}
			}
			picks = append(picks, p)
		}
	}
	add(m.Alliances.Red.TeamKeys, "Red")
	add(m.Alliances.Blue.TeamKeys, "Blue")

	for i := range picks {
		picks[i].Soonest = soonest != 0 && picks[i].NextWithUs == soonest
	}
	templates.MatchTeamPicker(picks, matchNum, "").Render(r.Context(), w)
}

// apiMatchAlliancesHandler renders the three-robot picker for a match: the red
// and blue alliance's team numbers.
func apiMatchAlliancesHandler(w http.ResponseWriter, r *http.Request) {
	eventKey := r.URL.Query().Get("event_key")
	matchNum, _ := strconv.Atoi(r.URL.Query().Get("match_num"))

	matches, err := getMatchesCached(eventKey)
	if err != nil {
		templates.AlliancePicker(nil, nil, matchNum, "Couldn't load the match schedule.").Render(r.Context(), w)
		return
	}
	m, found := findQualMatch(matches, matchNum)
	if !found {
		templates.AlliancePicker(nil, nil, matchNum, fmt.Sprintf("Match %d isn't in the schedule.", matchNum)).Render(r.Context(), w)
		return
	}

	templates.AlliancePicker(stripFRC(m.Alliances.Red.TeamKeys), stripFRC(m.Alliances.Blue.TeamKeys), matchNum, "").Render(r.Context(), w)
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

	scouterName := canonicalScouterName(sub.ScouterName)
	for _, teamData := range sub.Teams {
		db.Exec(`
			INSERT INTO scout_submissions (event_key, match_num, scouter_name, team_number, notes,
				has_checklist, broke, played_defense, was_defended, auto_type)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			sub.EventKey, sub.MatchNum, scouterName, teamData.TeamNumber, teamData.Notes,
			teamData.HasChecklist, teamData.Broke, teamData.PlayedDefense, teamData.WasDefended, strings.TrimSpace(teamData.AutoType))

		// Bust team analysis cache
		db.Exec(`DELETE FROM analysis_cache WHERE event_key = ? AND team_number = ?`,
			sub.EventKey, teamData.TeamNumber)
	}

	fmt.Printf("Saved match %d, scouter %q, %d teams\n", sub.MatchNum, scouterName, len(sub.Teams))
	w.WriteHeader(http.StatusOK)
}

// ── Pit Scouting ──────────────────────────────────────────────────────────────

func pitScoutPageHandler(w http.ResponseWriter, r *http.Request) {
	eventKey, ok := requireEvent(w, r)
	if !ok {
		return
	}
	templ.Handler(templates.PitScoutPage(eventKey, eventNameFor(eventKey))).ServeHTTP(w, r)
}

// apiPitTeamsHandler renders the event's team list, marking teams that already
// have pit scouting notes.
func apiPitTeamsHandler(w http.ResponseWriter, r *http.Request) {
	eventKey := r.URL.Query().Get("event_key")
	if eventKey == "" {
		http.Error(w, "event_key required", http.StatusBadRequest)
		return
	}

	teams, err := getEventTeamsCached(eventKey)
	if err != nil {
		log.Printf("pit teams %s: %v", eventKey, err)
		templates.PitTeamList(nil, "Couldn't load teams for this event.").Render(r.Context(), w)
		return
	}

	scouted := map[string]bool{}
	rows, err := db.Query(`SELECT DISTINCT team_number FROM pit_scouting`)
	if err == nil {
		for rows.Next() {
			var t string
			rows.Scan(&t)
			scouted[t] = true
		}
		rows.Close()
	}

	list := make([]templates.PitTeam, len(teams))
	for i, t := range teams {
		list[i] = templates.PitTeam{Number: t, Scouted: scouted[t]}
	}
	templates.PitTeamList(list, "").Render(r.Context(), w)
}

func savePitScoutHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Only POST allowed", http.StatusMethodNotAllowed)
		return
	}

	teamNum := strings.TrimSpace(r.FormValue("team_number"))
	summary := strings.TrimSpace(r.FormValue("summary"))
	if teamNum == "" || summary == "" {
		http.Error(w, "Team number and summary are required", http.StatusBadRequest)
		return
	}

	// A team keeps one pit scouting entry: edit the latest one if it exists.
	res, err := db.Exec(`
		UPDATE pit_scouting SET summary = ?, created_at = CURRENT_TIMESTAMP
		WHERE id = (SELECT id FROM pit_scouting WHERE team_number = ? ORDER BY created_at DESC, id DESC LIMIT 1)`,
		summary, teamNum)
	if err != nil {
		log.Printf("update pit scout: %v", err)
		http.Error(w, "Failed to save", http.StatusInternalServerError)
		return
	}
	verb := "Updated"
	if n, _ := res.RowsAffected(); n == 0 {
		verb = "Saved"
		if _, err := db.Exec(`INSERT INTO pit_scouting (team_number, summary) VALUES (?, ?)`, teamNum, summary); err != nil {
			log.Printf("save pit scout: %v", err)
			http.Error(w, "Failed to save", http.StatusInternalServerError)
			return
		}
	}

	fmt.Printf("%s pit scouting for team %s\n", verb, teamNum)
	w.Header().Set("HX-Trigger", "pitSaved") // refresh the team list
	fmt.Fprintf(w, "%s pit scouting for team %s ✓", verb, template.HTMLEscapeString(teamNum))
}

// apiPitNoteHandler returns a team's current pit scouting summary so it can be edited.
func apiPitNoteHandler(w http.ResponseWriter, r *http.Request) {
	teamNum := strings.TrimSpace(r.URL.Query().Get("team_number"))
	if teamNum == "" {
		http.Error(w, "team_number required", http.StatusBadRequest)
		return
	}

	var summary string
	err := db.QueryRow(`
		SELECT summary FROM pit_scouting WHERE team_number = ?
		ORDER BY created_at DESC, id DESC LIMIT 1`, teamNum).Scan(&summary)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"exists": err == nil, "summary": summary})
}

// pitNotesFor returns all pit scouting summaries for a team, oldest first,
// joined into one block of text. Returns "" if the team hasn't been pit scouted.
func pitNotesFor(teamNum string) string {
	rows, err := db.Query(`
		SELECT summary FROM pit_scouting
		WHERE team_number = ?
		ORDER BY created_at ASC`, teamNum)
	if err != nil {
		log.Printf("pit notes for %s: %v", teamNum, err)
		return ""
	}
	defer rows.Close()

	var summaries []string
	for rows.Next() {
		var s string
		rows.Scan(&s)
		summaries = append(summaries, s)
	}
	return strings.Join(summaries, "\n---\n")
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
	eventKey, ok := requireEvent(w, r)
	if !ok {
		return
	}

	data := templates.GeminiAnalysisPageData{
		EventKey:  eventKey,
		EventName: eventNameFor(eventKey),
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

	// The pit profile only depends on the pit notes, so fetch it alongside the
	// match analysis. A failure just leaves the pit row off the card.
	pitCh := make(chan templates.PitProfile, 1)
	go func() {
		p, err := getPitProfile(teamNum)
		if err != nil {
			log.Printf("pit profile for %s: %v", teamNum, err)
		}
		pitCh <- p
	}()

	card, err := getOrGenerateAnalysis(eventKey, teamNum)
	if err != nil {
		card = templates.TeamAnalysisCard{
			EventKey:   eventKey,
			TeamNumber: teamNum,
			Error:      "Error generating analysis: " + err.Error(),
		}
	}
	card.Pit = <-pitCh

	db.QueryRow(`SELECT EXISTS(SELECT 1 FROM scout_submissions WHERE event_key = ? AND team_number = ? AND `+hasScoutDataSQL+`)`,
		eventKey, teamNum).Scan(&card.HasNotes)
	db.QueryRow(`SELECT EXISTS(SELECT 1 FROM pit_scouting WHERE team_number = ?)`, teamNum).Scan(&card.HasPitNotes)

	// Cards in the next-match section share team numbers with the full list, so
	// their element ids carry a prefix to stay unique.
	card.Section = r.URL.Query().Get("section")
	card.EPA, card.HasEPA = teamTotalEPA(eventKey, teamNum)
	addTrustInfo(&card)

	if rankings, err := getEventRankingsCached(eventKey); err == nil {
		card.Rank, card.HasRank = rankings[teamNum]
	} else {
		log.Printf("rankings for %s at %s: %v", teamNum, eventKey, err)
	}

	// The test event's matches don't exist on TBA, so links to them would be dead.
	if matches, err := getMatchesCached(eventKey); err == nil && eventKey != testEventKey {
		for _, m := range latestPlayedMatches(matches, teamNum, 2) {
			card.RecentMatches = append(card.RecentMatches, templates.MatchLink{
				Label: m.ShortLabel(),
				URL:   "https://www.thebluealliance.com/match/" + m.Key,
			})
		}
	} else {
		log.Printf("recent matches for %s at %s: %v", teamNum, eventKey, err)
	}

	templates.SingleTeamAnalysisCard(card).Render(r.Context(), w)
}

// nextQualFor returns our team's first unplayed qual match.
func nextQualFor(matches []Match, team string) (Match, bool) {
	key := "frc" + team
	var next Match
	found := false
	for _, m := range matches {
		if m.CompLevel != "qm" || m.Played() || (found && m.MatchNumber >= next.MatchNumber) {
			continue
		}
		for _, k := range append(append([]string{}, m.Alliances.Red.TeamKeys...), m.Alliances.Blue.TeamKeys...) {
			if k == key {
				next, found = m, true
				break
			}
		}
	}
	return next, found
}

// apiNextMatchHandler renders the next-match section: our next qual, the
// strategy for it, and (via the page) a summary card for each team in it.
func apiNextMatchHandler(w http.ResponseWriter, r *http.Request) {
	eventKey := r.URL.Query().Get("event_key")
	if eventKey == "" || eventKey == "none" {
		http.Error(w, "event_key required", http.StatusBadRequest)
		return
	}

	data := templates.NextMatchData{EventKey: eventKey}
	matches, err := getMatchesCached(eventKey)
	if err != nil {
		log.Printf("next match at %s: %v", eventKey, err)
		data.PlanError = "Couldn't load the match schedule."
		templates.NextMatchSection(data).Render(r.Context(), w)
		return
	}

	m, ok := nextQualFor(matches, ourTeam)
	if !ok {
		templates.NextMatchSection(data).Render(r.Context(), w)
		return
	}

	data.Found = true
	data.MatchNum = m.MatchNumber
	data.Label = m.ShortLabel()

	red, blue := stripFRC(m.Alliances.Red.TeamKeys), stripFRC(m.Alliances.Blue.TeamKeys)
	ours, theirs := red, blue
	weAreRed := true
	for _, t := range blue {
		if t == ourTeam {
			ours, theirs = blue, red
			weAreRed = false
		}
	}
	if p, ok := statboticsMatchPrediction(m.Key); ok {
		data.HasPrediction = true
		data.WinPct = int(p.RedWinProb*100 + 0.5)
		data.OurScore, data.TheirScore = p.RedScore, p.BlueScore
		if !weAreRed {
			data.WinPct = 100 - data.WinPct
			data.OurScore, data.TheirScore = p.BlueScore, p.RedScore
		}
		data.Outlook = outlook(data.WinPct)
	}
	for _, t := range ours {
		if t != ourTeam {
			data.Partners = append(data.Partners, t)
		}
	}
	data.Opponents = theirs

	plan, err := getOrGenerateMatchPlan(eventKey, ourTeam, m)
	if err != nil {
		data.PlanError = "Error generating strategy: " + err.Error()
	} else {
		data.Plan = plan
	}

	templates.NextMatchSection(data).Render(r.Context(), w)
}

func apiTeamNotesHandler(w http.ResponseWriter, r *http.Request) {
	eventKey := r.URL.Query().Get("event_key")
	teamNum := r.URL.Query().Get("team_number")
	if eventKey == "" || teamNum == "" {
		http.Error(w, "event_key and team_number required", http.StatusBadRequest)
		return
	}

	rows, err := db.Query(`
		SELECT match_num, notes, COALESCE(scouter_name, ''), COALESCE(ai_generated, 0),
			COALESCE(has_checklist, 0), COALESCE(broke, 0), COALESCE(played_defense, 0), COALESCE(was_defended, 0), COALESCE(auto_type, '')
		FROM scout_submissions
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
		rows.Scan(&n.MatchNum, &n.Notes, &n.ScouterName, &n.AIGenerated,
			&n.HasChecklist, &n.Broke, &n.PlayedDefense, &n.WasDefended, &n.AutoType)
		notes = append(notes, n)
	}

	templates.TeamNotesPanel(notes).Render(r.Context(), w)
}

func apiTeamPitNotesHandler(w http.ResponseWriter, r *http.Request) {
	teamNum := r.URL.Query().Get("team_number")
	if teamNum == "" {
		http.Error(w, "team_number required", http.StatusBadRequest)
		return
	}

	rows, err := db.Query(`
		SELECT created_at, summary FROM pit_scouting
		WHERE team_number = ?
		ORDER BY created_at DESC`, teamNum)
	if err != nil {
		http.Error(w, "db error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var notes []templates.PitNote
	for rows.Next() {
		var n templates.PitNote
		var createdAt time.Time
		rows.Scan(&createdAt, &n.Summary)
		n.CreatedAt = createdAt.Format("Jan 2, 3:04 PM") + " UTC"
		notes = append(notes, n)
	}

	templates.TeamPitNotesPanel(notes).Render(r.Context(), w)
}

// apiSearchTeamsHandler returns a JSON array of team numbers at the event whose
// number contains q, or whose match scouting or pit scouting notes contain q.
func apiSearchTeamsHandler(w http.ResponseWriter, r *http.Request) {
	eventKey := r.URL.Query().Get("event_key")
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if eventKey == "" || q == "" {
		http.Error(w, "event_key and q required", http.StatusBadRequest)
		return
	}

	escaped := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(q)
	like := "%" + escaped + "%"

	rows, err := db.Query(`
		SELECT DISTINCT team_number FROM scout_submissions
		WHERE event_key = ? AND (
			team_number LIKE ? ESCAPE '\'
			OR notes LIKE ? ESCAPE '\'
			OR auto_type LIKE ? ESCAPE '\'
			OR team_number IN (SELECT team_number FROM pit_scouting WHERE summary LIKE ? ESCAPE '\')
		)`, eventKey, like, like, like, like)
	if err != nil {
		http.Error(w, "db error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	teams := []string{}
	for rows.Next() {
		var t string
		rows.Scan(&t)
		teams = append(teams, t)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(teams)
}

// teamAnalysisJSON is the structured response Gemini returns for team analysis.
type teamAnalysisJSON struct {
	Verdict        string `json:"verdict"` // Elite Pick, Strong Pick, Average, Below Average, Avoid
	Shooting       string `json:"shooting"`
	Driving        string `json:"driving"`
	Failures       string `json:"failures"`
	Auto           string `json:"auto"`
	Recommendation string `json:"recommendation"`
	Scoring        int    `json:"scoring"`
	Reliability    int    `json:"reliability"`
	Defense        int    `json:"defense"` // 0 = N/A
}

// analysisPromptVersion is mixed into the cache key so edits to the prompt's
// output shape invalidate previously cached analyses.
const analysisPromptVersion = "v6"

func getOrGenerateAnalysis(eventKey, teamNum string) (templates.TeamAnalysisCard, error) {
	rows, err := db.Query(`
		SELECT `+scoutNoteColumns+` FROM scout_submissions
		WHERE event_key = ? AND team_number = ?
		ORDER BY match_num ASC`, eventKey, teamNum)
	if err != nil {
		return templates.TeamAnalysisCard{}, err
	}
	var notesList []string
	for rows.Next() {
		n, _ := scanScoutNote(rows)
		notesList = append(notesList, n)
	}
	rows.Close()

	combined := strings.Join(notesList, "\n")
	pitNotes := pitNotesFor(teamNum)
	hashInput := analysisPromptVersion + "\n" + combined
	if pitNotes != "" {
		hashInput += "\n[pit]\n" + pitNotes
	}
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(hashInput)))

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
				EventKey:       eventKey,
				TeamNumber:     teamNum,
				Verdict:        result.Verdict,
				Shooting:       result.Shooting,
				Driving:        result.Driving,
				Failures:       result.Failures,
				Auto:           result.Auto,
				Recommendation: result.Recommendation,
				Scoring:        result.Scoring,
				Reliability:    result.Reliability,
				Defense:        result.Defense,
				FromCache:      true,
			}, nil
		}
		// If JSON parse fails, fall through to regenerate
	}

	result, err := callGeminiTeamAnalysis(teamNum, eventKey, combined, pitNotes, scoutStatsFor(eventKey, teamNum))
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
		EventKey:       eventKey,
		TeamNumber:     teamNum,
		Verdict:        result.Verdict,
		Shooting:       result.Shooting,
		Driving:        result.Driving,
		Failures:       result.Failures,
		Auto:           result.Auto,
		Recommendation: result.Recommendation,
		Scoring:        result.Scoring,
		Reliability:    result.Reliability,
		Defense:        result.Defense,
		FromCache:      false,
	}, nil
}

// ── Match Planner ─────────────────────────────────────────────────────────────

func matchPlannerPageHandler(w http.ResponseWriter, r *http.Request) {
	eventKey, ok := requireEvent(w, r)
	if !ok {
		return
	}

	data := templates.MatchPlannerPageData{EventKey: eventKey, EventName: eventNameFor(eventKey)}
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

// matchPlanPromptVersion is mixed into the cache key so edits to the match plan
// prompt invalidate previously cached strategies.
const matchPlanPromptVersion = "v5"

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

	// Everything the model sees about all six teams, including our own robot. The
	// cache key covers all of it, so the plan regenerates when any of it changes.
	ourSide, theirSide := redTeams, blueTeams
	if ourAlliance == "Blue" {
		ourSide, theirSide = blueTeams, redTeams
	}
	notesContext, lineups := matchPlanContext(eventKey, teamNumber, ourSide, theirSide)
	forecast := forecastText(m, ourAlliance == "Red")
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(matchPlanPromptVersion+"\n"+lineups+"\n"+forecast+"\n"+notesContext)))

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

	strategy, err := callGeminiMatchPlan(teamNumber, eventKey, m.MatchNumber, ourAlliance, redTeams, blueTeams, notesContext, lineups, forecast)
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
	epaCache    = map[string]map[string]float64{}
	epaFailedAt = map[string]time.Time{}
	epaCacheMu  sync.RWMutex
	httpClient  = &http.Client{Timeout: 5 * time.Second}
)

// epaBreakdown returns a team's EPA breakdown for the given event. The test
// event uses made-up numbers (see seed.go); every other event uses Statbotics.
func epaBreakdown(eventKey, teamNum string) (map[string]float64, bool) {
	if eventKey == testEventKey {
		b, ok := demoEPA[teamNum]
		return b, ok
	}
	return statboticsEPABreakdown(teamNum)
}

// fetchStatboticsEPA returns the team's current-season EPA breakdown as text for
// Gemini prompts, or "unavailable".
func fetchStatboticsEPA(eventKey, teamNum string) string {
	breakdown, ok := epaBreakdown(eventKey, teamNum)
	if !ok {
		return "unavailable"
	}

	keys := make([]string, 0, len(breakdown))
	for k := range breakdown {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var lines []string
	for _, k := range keys {
		lines = append(lines, fmt.Sprintf("  %s: %.2f", k, breakdown[k]))
	}
	return strings.Join(lines, "\n")
}

// teamTotalEPA returns the team's current-season total points EPA.
func teamTotalEPA(eventKey, teamNum string) (float64, bool) {
	breakdown, ok := epaBreakdown(eventKey, teamNum)
	if !ok {
		return 0, false
	}
	total, ok := breakdown["total_points"]
	return total, ok
}

// statboticsEPABreakdown fetches (and caches) the team's current-season EPA breakdown.
func statboticsEPABreakdown(teamNum string) (map[string]float64, bool) {
	epaCacheMu.RLock()
	if v, ok := epaCache[teamNum]; ok {
		epaCacheMu.RUnlock()
		return v, true
	}
	failedAt, failed := epaFailedAt[teamNum]
	epaCacheMu.RUnlock()
	// Don't retry a failed lookup on every card load while Statbotics is down
	if failed && time.Since(failedAt) < 2*time.Minute {
		return nil, false
	}

	breakdown, ok := fetchEPABreakdown(teamNum)
	epaCacheMu.Lock()
	if ok {
		epaCache[teamNum] = breakdown
		delete(epaFailedAt, teamNum)
	} else {
		epaFailedAt[teamNum] = time.Now()
	}
	epaCacheMu.Unlock()
	return breakdown, ok
}

func fetchEPABreakdown(teamNum string) (map[string]float64, bool) {
	year := time.Now().Year()
	url := fmt.Sprintf("https://api.statbotics.io/v3/team_year/%s/%d", teamNum, year)
	resp, err := httpClient.Get(url)
	if err != nil {
		return nil, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, false
	}

	var data struct {
		EPA struct {
			Breakdown map[string]float64 `json:"breakdown"`
		} `json:"epa"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil || len(data.EPA.Breakdown) == 0 {
		return nil, false
	}
	return data.EPA.Breakdown, true
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
	PitNotes     string
	EPABreakdown string
	Stats        string
}

func callGeminiTeamAnalysis(teamNum, eventKey, notes, pitNotes string, stats scoutStats) (teamAnalysisJSON, error) {
	tmpl, err := template.New("team_analysis").Parse(teamAnalysisPromptTmpl)
	if err != nil {
		return teamAnalysisJSON{}, fmt.Errorf("failed to parse team analysis prompt: %w", err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, teamAnalysisPromptData{
		TeamNum:      teamNum,
		EventKey:     eventKey,
		Notes:        notes,
		PitNotes:     pitNotes,
		EPABreakdown: fetchStatboticsEPA(eventKey, teamNum),
		Stats:        stats.promptText(),
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
	Forecast         string
	Lineups          string
	NotesContext     string
}

func callGeminiMatchPlan(teamNum, eventKey string, matchNum int, ourAlliance string, redTeams, blueTeams []string, notesContext, lineups, forecast string) (string, error) {
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
		Forecast:         forecast,
		Lineups:          lineups,
		NotesContext:     notesContext,
	}); err != nil {
		return "", fmt.Errorf("failed to render match plan prompt: %w", err)
	}

	return geminiPost(buf.String())
}

// ── AI Fill-in Scout ──────────────────────────────────────────────────────────

const geminiVideoURL = "https://generativelanguage.googleapis.com/v1beta/models/gemini-3.1-flash-lite-preview:generateContent"

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
	if req.EventKey == testEventKey {
		clearDemoPitNotes()
	}

	fmt.Fprintf(w, "Deleted all data for event: %s", req.EventKey)
}

func seedTestHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Only POST allowed", http.StatusMethodNotAllowed)
		return
	}
	seedTestData()
	fmt.Fprintf(w, "Test event seeded: %s (%d observations across %d teams, demo EPAs, %d played + %d upcoming matches)",
		testEventKey, len(testObservations), len(demoTotalEPA), 9, len(testMatches)-9)
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
