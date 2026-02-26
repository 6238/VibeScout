package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"

	"vibe-scout/templates"

	"github.com/a-h/templ"

	_ "github.com/glebarez/go-sqlite"
)

type ScoutSubmission struct {
	EventKey  string                         `json:"event_key"`
	MatchNum  int                            `json:"match_num"`
	ScouterID int                            `json:"scouter_id"`
	Teams     []TeamScoutData                `json:"teams"`
	Rankings  map[string]map[string][]string `json:"rankings"`
}

type TeamScoutData struct {
	TeamNumber         string `json:"team_number"`
	AutoPath           string `json:"auto_path"`
	AutoStartPos       string `json:"auto_start_pos"`
	AutoClimb          string `json:"auto_climb"`
	TeleopClimb        string `json:"teleop_climb"`
	DefensePct         int    `json:"defense_pct"`
	DefendedAgainstPct int    `json:"defended_against_pct"`
	Notes              string `json:"notes"`
}

func saveScoutDataHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Only POST allowed", http.StatusMethodNotAllowed)
		return
	}

	var sub ScoutSubmission
	if err := json.NewDecoder(r.Body).Decode(&sub); err != nil {
		fmt.Printf("❌ JSON Decode Error: %v\n", err)
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}

	tierWeights := map[string]int{"HIGH": 3, "MID": 2, "LOW": 1}

	fmt.Printf("📝 Received scout data - Teams: %d, Rankings: %v\n", len(sub.Teams), sub.Rankings)

	// Save each team's scout data
	for _, teamData := range sub.Teams {
		db.Exec(`
			INSERT INTO scout_submissions (event_key, match_num, scouter_id, team_number, auto_path, auto_start_pos, auto_climb, teleop_climb, defense_pct, defended_against_pct, notes)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			sub.EventKey, sub.MatchNum, sub.ScouterID, teamData.TeamNumber, teamData.AutoPath, teamData.AutoStartPos, teamData.AutoClimb, teamData.TeleopClimb, teamData.DefensePct, teamData.DefendedAgainstPct, teamData.Notes)
	}

	// Generate pairwise comparisons from shared rankings
	if sub.Rankings != nil {
		for category, tiers := range sub.Rankings {
			highTeams := tiers["HIGH"]
			midTeams := tiers["MID"]
			lowTeams := tiers["LOW"]

			for _, teamA := range highTeams {
				for _, teamB := range midTeams {
					db.Exec(`INSERT INTO pairwise_scouting (event_key, match_num, scouter_id, category, team_a, team_b, difference) VALUES (?, ?, ?, ?, ?, ?, ?)`,
						sub.EventKey, sub.MatchNum, sub.ScouterID, category, teamA, teamB, tierWeights["HIGH"]-tierWeights["MID"])
				}
				for _, teamB := range lowTeams {
					db.Exec(`INSERT INTO pairwise_scouting (event_key, match_num, scouter_id, category, team_a, team_b, difference) VALUES (?, ?, ?, ?, ?, ?, ?)`,
						sub.EventKey, sub.MatchNum, sub.ScouterID, category, teamA, teamB, tierWeights["HIGH"]-tierWeights["LOW"])
				}
			}
			for _, teamA := range midTeams {
				for _, teamB := range lowTeams {
					db.Exec(`INSERT INTO pairwise_scouting (event_key, match_num, scouter_id, category, team_a, team_b, difference) VALUES (?, ?, ?, ?, ?, ?, ?)`,
						sub.EventKey, sub.MatchNum, sub.ScouterID, category, teamA, teamB, tierWeights["MID"]-tierWeights["LOW"])
				}
			}
		}
	}

	fmt.Printf("✅ Saved scout data for Match %d, Scouter %d, %d teams\n", sub.MatchNum, sub.ScouterID, len(sub.Teams))
	w.WriteHeader(http.StatusOK)
}

type Comparison struct {
	TeamA int `json:"teamA"`
	TeamB int `json:"teamB"`
	Diff  int `json:"diff"`
}

type AnalysisRequest struct {
	Comparisons []Comparison `json:"comparisons"`
}

type AnalysisResponse struct {
	Rankings   []TeamRanking `json:"rankings"`
	Stats      Stats         `json:"stats"`
	Svd        SVD           `json:"svd"`
	Validation Validation    `json:"validation"`
}

type TeamRanking struct {
	Rank  int     `json:"rank"`
	Score float64 `json:"score"`
	Team  int     `json:"team"`
}

type Stats struct {
	ConditionNumber  float64 `json:"condition_number"`
	ConsistencyRatio float64 `json:"consistency_ratio"`
	MatrixRank       int     `json:"matrix_rank"`
	NumComparisons   int     `json:"num_comparisons"`
	NumTeams         int     `json:"num_teams"`
}

type SVD struct {
	U              [][]float64 `json:"U"`
	Vh             [][]float64 `json:"Vh"`
	OriginalMatrix [][]float64 `json:"original_matrix"`
	S              []float64   `json:"s"`
}

type Validation struct {
	Messages    []string `json:"messages"`
	Suggestions []string `json:"suggestions"`
	Warnings    []string `json:"warnings"`
}

type TeamVariability struct {
	Team         int     `json:"team"`
	RawVariation float64 `json:"raw_variation"`
	Normalized   float64 `json:"normalized_variation"`
	Rank         int     `json:"rank"`
	RankingScore float64 `json:"ranking_score"`
}

type AnalysisSummary struct {
	Variabilities []TeamVariability `json:"variabilities"`
	Stability     float64           `json:"stability"` // condition-number-style metric
	Stats         Stats             `json:"stats"`
}

// Event struct matches the TBA 'simple' model
type Event struct {
	Key       string `json:"key"`
	Name      string `json:"name"`
	StartDate string `json:"start_date"`
}

type ComparisonConfig struct {
	Categories []string // e.g., "Auto Reliability", "Teleop Scoring", "Defensive Vibe"
}

// Current setup for the app
var Config = ComparisonConfig{
	Categories: []string{"Match Efficency", "Intake Efficency"},
}

func main() {
	initDB()

	// Route for the Home Page
	http.Handle("/", http.HandlerFunc(homeHandler))

	// Route for when the "Scout" button is pressed
	http.HandleFunc("/scout", http.HandlerFunc(scoutHandler))

	http.HandleFunc("/api/save-scout", saveScoutDataHandler)

	// Analysis routes
	http.HandleFunc("/analysis", analysisPageHandler)
	http.HandleFunc("/api/run-analysis", runAnalysisHandler)
	http.HandleFunc("/epa", epaPageHandler)
	http.HandleFunc("/api/run-epa", runEPAHandler)

	fmt.Println("🎨 Vibe Scout is running on http://localhost:8080")
	http.ListenAndServe(":8080", nil)
}

var (
	scouterCounter int
	scouterMutex   sync.Mutex
)

func scoutHandler(w http.ResponseWriter, r *http.Request) {
	eventKey := r.URL.Query().Get("event_key")
	matchNum, _ := strconv.Atoi(r.URL.Query().Get("match_num"))
	scouterID, _ := strconv.Atoi(r.URL.Query().Get("scouter_id"))

	matches, err := getMatchesCached(eventKey)
	if err != nil {
		http.Error(w, "Failed to fetch schedule", 500)
		return
	}

	// Filter for Quals by default, or handle Playoffs if specified
	var currentMatch Match
	found := false
	for _, m := range matches {
		// Most scouting happens in Quals ("qm")
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

	// Toggle: ensure Scouter 1 and 2 are always opposite
	isEvenMatch := matchNum%2 == 0
	isEvenScouter := scouterID%2 == 0

	allianceName := "Red"
	var teamKeys []string

	if isEvenMatch != isEvenScouter {
		allianceName = "Blue"
		teamKeys = currentMatch.Alliances.Blue.TeamKeys
	} else {
		teamKeys = currentMatch.Alliances.Red.TeamKeys
	}

	// CLEANING: "frc254" -> "254"
	teams := []string{}
	for _, tk := range teamKeys {
		if len(tk) > 3 {
			teams = append(teams, tk[3:])
		}
	}

	templates.ScoutPage(eventKey, strconv.Itoa(matchNum), strconv.Itoa(scouterID), allianceName, teams, Config.Categories).Render(r.Context(), w)
}

func homeHandler(w http.ResponseWriter, r *http.Request) {
	// 2026 is the current season
	events, err := getEventsCached("2026")
	if err != nil {
		http.Error(w, "Could not load events", 500)
		return
	}

	eventMap := make(map[string]string)

	now := time.Now()
	// Define "This Week" as anything starting 3 days ago through 4 days from now
	// (Adjust these offsets if you want a stricter Monday-Sunday window)
	startOfWindow := now.AddDate(0, 0, -5)
	endOfWindow := now.AddDate(0, 0, 5)

	for _, e := range events {
		// Parse the TBA date string "YYYY-MM-DD"
		eventTime, err := time.Parse("2006-01-02", e.StartDate)
		if err != nil {
			continue
		}

		// Only add to the map if it falls in our 7-day vibe window
		if eventTime.After(startOfWindow) && eventTime.Before(endOfWindow) {
			eventMap[e.Key] = e.Name
		}
	}

	if len(eventMap) == 0 {
		// Fallback so the dropdown isn't just empty
		eventMap["none"] = "No events found for this week"
	}

	component := templates.Home(eventMap)
	templ.Handler(component).ServeHTTP(w, r)
}

func analysisPageHandler(w http.ResponseWriter, r *http.Request) {
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

	data := templates.AnalysisPageData{
		Events:     eventMap,
		Categories: Config.Categories,
	}
	component := templates.AnalysisPage(data)
	templ.Handler(component).ServeHTTP(w, r)
}

func runAnalysisHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Only POST allowed", http.StatusMethodNotAllowed)
		return
	}

	eventKey := r.FormValue("event_key")
	if eventKey == "" || eventKey == "none" {
		http.Error(w, "Event required", http.StatusBadRequest)
		return
	}

	var categoryAnalyses []templates.CategoryAnalysis
	var totalStability float64

	for _, category := range Config.Categories {
		// Debug: check what's in the database
		rows2, _ := db.Query("SELECT COUNT(*), category FROM pairwise_scouting WHERE event_key = ? GROUP BY category", eventKey)
		fmt.Printf("🔍 Database check for event %s:\n", eventKey)
		for rows2.Next() {
			var count int
			var cat string
			rows2.Scan(&count, &cat)
			fmt.Printf("   Category: %s, Count: %d\n", cat, count)
		}
		rows2.Close()

		comps, err := getComparisonsForEvent(eventKey, category)
		if err != nil {
			continue
		}
		fmt.Printf("📊 Analysis for %s - Event: %s, Category: %s, Comparisons found: %d\n", time.Now().Format("15:04:05"), eventKey, category, len(comps))

		summary, err := analyzeEventCategory(eventKey, category)
		if err != nil {
			continue
		}

		// Normalize scores to -100 to +100 scale (centered at 0)
		var minScore, maxScore float64
		if len(summary.Variabilities) > 0 {
			minScore = summary.Variabilities[0].RankingScore
			maxScore = summary.Variabilities[0].RankingScore
			for _, v := range summary.Variabilities {
				if v.RankingScore < minScore {
					minScore = v.RankingScore
				}
				if v.RankingScore > maxScore {
					maxScore = v.RankingScore
				}
			}
		}
		scoreRange := maxScore - minScore
		if scoreRange == 0 {
			scoreRange = 1
		}

		variabilities := make([]templates.TeamVariability, len(summary.Variabilities))
		for i, v := range summary.Variabilities {
			// Normalize to -100 to +100 scale (centered at 0)
			normalizedScore := (((v.RankingScore - minScore) / scoreRange) * 200) - 100
			variabilities[i] = templates.TeamVariability{
				Team:         v.Team,
				RawVariation: v.RawVariation,
				Normalized:   v.Normalized,
				Rank:         v.Rank,
				RankingScore: normalizedScore,
			}
		}

		categoryAnalyses = append(categoryAnalyses, templates.CategoryAnalysis{
			Category:      category,
			Variabilities: variabilities,
			Stability:     summary.Stability,
		})
		totalStability += summary.Stability
	}

	var stabilityScore float64
	if len(categoryAnalyses) > 0 {
		stabilityScore = totalStability / float64(len(categoryAnalyses))
	}

	component := templates.AnalysisResults(categoryAnalyses, stabilityScore)
	component.Render(r.Context(), w)
}

func epaPageHandler(w http.ResponseWriter, r *http.Request) {
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

	data := templates.EPAPageData{
		Events: eventMap,
	}
	component := templates.EPAPage(data)
	templ.Handler(component).ServeHTTP(w, r)
}

func runEPAHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Only POST allowed", http.StatusMethodNotAllowed)
		return
	}

	eventKey := r.FormValue("event_key")
	if eventKey == "" || eventKey == "none" {
		http.Error(w, "Event required", http.StatusBadRequest)
		return
	}

	teams, err := calculateEPA(eventKey)
	if err != nil {
		http.Error(w, "EPA calculation failed", 500)
		return
	}

	component := templates.EPAResults(teams)
	component.Render(r.Context(), w)
}

func calculateEPA(eventKey string) ([]templates.TeamEPA, error) {
	fmt.Printf("🔍 Starting EPA calculation for event: %s\n", eventKey)

	// Get matches with score breakdown from TBA
	matches, err := getMatchesWithBreakdown(eventKey)
	if err != nil {
		fmt.Printf("❌ Error getting matches: %v\n", err)
		return nil, err
	}
	fmt.Printf("📊 Found %d matches\n", len(matches))

	// Get teams that have scouting data
	scoutRows, err := db.Query(`
		SELECT DISTINCT team_number FROM scout_submissions WHERE event_key = ?`, eventKey)
	if err != nil {
		return nil, err
	}

	scoutedTeams := make(map[string]bool)
	for scoutRows.Next() {
		var teamNum string
		scoutRows.Scan(&teamNum)
		scoutedTeams[teamNum] = true
	}
	scoutRows.Close()

	// Initialize EPA store with default values for scouted teams
	type teamEPA struct {
		offenseEPA float64
		defenseEPA float64
		foulEPA    float64
	}

	// Start with base EPA for all scouted teams
	epaStore := make(map[string]*teamEPA)
	for team := range scoutedTeams {
		epaStore[team] = &teamEPA{offenseEPA: 20.0, defenseEPA: 0.0, foulEPA: 0.0}
	}

	// Constants (matching Python code)
	const K float64 = 0.2
	const DEF_K float64 = 0.2
	const FOUL_K float64 = 0.1

	// Process each match
	for _, match := range matches {
		if match.CompLevel != "qm" {
			continue
		}

		// Get score breakdown
		blueScore := getHubScore(match.ScoreBreakdown.Blue, "blue")
		redScore := getHubScore(match.ScoreBreakdown.Red, "red")
		if blueScore == 0 && redScore == 0 {
			continue // No score breakdown available
		}

		// Get team numbers (remove "frc" prefix)
		blueTeams := make([]string, 0)
		for _, tk := range match.Alliances.Blue.TeamKeys {
			if len(tk) > 3 {
				blueTeams = append(blueTeams, tk[3:])
			}
		}
		redTeams := make([]string, 0)
		for _, tk := range match.Alliances.Red.TeamKeys {
			if len(tk) > 3 {
				redTeams = append(redTeams, tk[3:])
			}
		}

		// Get scores
		blueActual := getHubScore(match.ScoreBreakdown.Blue, "blue")
		redActual := getHubScore(match.ScoreBreakdown.Red, "red")
		blueFoulPoints := getFoulPoints(match.ScoreBreakdown.Blue)
		redFoulPoints := getFoulPoints(match.ScoreBreakdown.Red)

		// 1. Calculate Raw Potentials
		blueRawOffense := 0.0
		redRawOffense := 0.0
		blueDefStrength := 0.0
		redDefStrength := 0.0

		for _, r := range blueTeams {
			if e, ok := epaStore[r]; ok {
				blueRawOffense += e.offenseEPA
				blueDefStrength += e.defenseEPA
			} else {
				blueRawOffense += 100.0 // Default for unknown teams
			}
		}
		for _, r := range redTeams {
			if e, ok := epaStore[r]; ok {
				redRawOffense += e.offenseEPA
				redDefStrength += e.defenseEPA
			} else {
				redRawOffense += 100.0
			}
		}

		// 2. Calculate Context-Aware Expectations
		blueExpected := mathMax(0, blueRawOffense-redDefStrength)
		redExpected := mathMax(0, redRawOffense-blueDefStrength)

		// 3. Offense Deltas
		blueOffDelta := (float64(blueActual) - blueExpected) * K
		redOffDelta := (float64(redActual) - redExpected) * K

		// 4. Defense Deltas
		blueDefDelta := (redRawOffense - float64(redActual)) * DEF_K
		redDefDelta := (blueRawOffense - float64(blueActual)) * DEF_K

		// 5. Foul Deltas
		blueFoulPred := 0.0
		redFoulPred := 0.0
		for _, r := range blueTeams {
			if e, ok := epaStore[r]; ok {
				blueFoulPred += e.foulEPA
			}
		}
		for _, r := range redTeams {
			if e, ok := epaStore[r]; ok {
				redFoulPred += e.foulEPA
			}
		}
		blueFoulDelta := (float64(redFoulPoints) - blueFoulPred) * FOUL_K
		redFoulDelta := (float64(blueFoulPoints) - redFoulPred) * FOUL_K

		// 6. Apply all updates
		for _, r := range blueTeams {
			if e, ok := epaStore[r]; ok {
				e.offenseEPA += blueOffDelta / 3.0
				e.defenseEPA += blueDefDelta / 3.0
				e.foulEPA += blueFoulDelta / 3.0
			}
		}
		for _, r := range redTeams {
			if e, ok := epaStore[r]; ok {
				e.offenseEPA += redOffDelta / 3.0
				e.defenseEPA += redDefDelta / 3.0
				e.foulEPA += redFoulDelta / 3.0
			}
		}
	}

	// Convert to output format
	var teams []templates.TeamEPA
	for team, e := range epaStore {
		teams = append(teams, templates.TeamEPA{
			Team:       team,
			EPA:        e.offenseEPA,
			DefenseEPA: e.defenseEPA,
			FoulEPA:    e.foulEPA,
		})
	}

	// Sort by EPA descending
	for i := 0; i < len(teams)-1; i++ {
		for j := i + 1; j < len(teams); j++ {
			if teams[j].EPA > teams[i].EPA {
				teams[i], teams[j] = teams[j], teams[i]
			}
		}
	}

	return teams, nil
}

func mathMax(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}
