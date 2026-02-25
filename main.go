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
	EventKey  string       `json:"event_key"`
	MatchNum  int          `json:"match_num"`
	ScouterID int          `json:"scouter_id"`
	Data      []TierResult `json:"data"`
}

type TierResult struct {
	Category string   `json:"category"`
	Tier     string   `json:"tier"`
	Teams    []string `json:"teams"`
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

	// 1. Define the numerical weight for each tier
	tierWeights := map[string]int{
		"TOP": 3,
		"MID": 2,
		"LOW": 1,
	}

	// 2. Group teams by category
	// This maps Category Name -> List of {TeamNumber, Weight}
	type teamRating struct {
		number string
		weight int
	}
	categoryGroups := make(map[string][]teamRating)

	for _, res := range sub.Data {
		weight := tierWeights[res.Tier]
		for _, teamNum := range res.Teams {
			categoryGroups[res.Category] = append(categoryGroups[res.Category], teamRating{
				number: teamNum,
				weight: weight,
			})
		}
	}

	// 3. Perform Pairwise Comparisons within each category
	count := 0
	for category, teams := range categoryGroups {
		// Compare every team against every other team in the same category
		for i := 0; i < len(teams); i++ {
			for j := i + 1; j < len(teams); j++ {
				teamA := teams[i]
				teamB := teams[j]

				// Calculate the difference: (Rank of A) - (Rank of B)
				// If A is Top (3) and B is Low (1), diff is 2.
				// If both are Top, diff is 0.
				diff := teamA.weight - teamB.weight

				_, err := db.Exec(`
                    INSERT INTO pairwise_scouting (
                        event_key, 
                        match_num, 
                        scouter_id, 
                        category, 
                        team_a, 
                        team_b, 
                        difference
                    ) VALUES (?, ?, ?, ?, ?, ?, ?)`,
					sub.EventKey,
					sub.MatchNum,
					sub.ScouterID,
					category,
					teamA.number,
					teamB.number,
					diff,
				)

				if err != nil {
					fmt.Printf("❌ DB Insert Error: %v\n", err)
				}
				count++
			}
		}
	}

	fmt.Printf("✅ Successfully saved %d pairwise records for Match %d (Scouter %d)\n", count, sub.MatchNum, sub.ScouterID)
	// Persist full scout payload for audit / analysis
	payloadBytes, err := json.Marshal(sub)
	if err != nil {
		fmt.Printf("❌ Scout payload marshal error: %v\n", err)
	} else {
		_, err = db.Exec(`INSERT INTO scout_submissions (event_key, match_num, scouter_id, payload) VALUES (?, ?, ?, ?)`,
			sub.EventKey, sub.MatchNum, sub.ScouterID, string(payloadBytes))
		if err != nil {
			fmt.Printf("❌ Scout payload DB insert error: %v\n", err)
		}
	}
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
	Categories: []string{"Field Awareness", "Defense"},
}

func main() {
	initDB()

	// Route for the Home Page
	http.Handle("/", http.HandlerFunc(homeHandler))

	// Route for when the "Scout" button is pressed
	http.HandleFunc("/scout", http.HandlerFunc(scoutHandler))

	http.HandleFunc("/api/save-scout", saveScoutDataHandler)

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
