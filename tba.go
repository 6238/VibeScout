package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const TBA_BASE = "https://www.thebluealliance.com/api/v3"

// Temporary hardcoded key so Railway can load events. Rotate after setting TBA_API_KEY on Railway.
const tbaFallbackKey = "ViapHIbD2P8avX3ztBkJsXCG5f5H2N9XzJ8LwRzJjEzUtomxk70yROk3t8hejrli"

var tbaHTTP = &http.Client{Timeout: 15 * time.Second}

func tbaKey() string {
	k := strings.TrimSpace(os.Getenv("TBA_API_KEY"))
	if k != "" && k != "your_tba_api_key_here" {
		return k
	}
	return tbaFallbackKey
}

func tbaDo(path string) (*http.Response, error) {
	resp, err := tbaDoWithKey(path, tbaKey())
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusUnauthorized && tbaKey() != tbaFallbackKey {
		resp.Body.Close()
		log.Printf("tba: env key rejected, retrying fallback key for %s", path)
		return tbaDoWithKey(path, tbaFallbackKey)
	}
	return resp, nil
}

func tbaDoWithKey(path, key string) (*http.Response, error) {
	req, err := http.NewRequest("GET", TBA_BASE+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-TBA-Auth-Key", key)
	req.Header.Set("User-Agent", "VibeScout/2 (FRC 6238; https://github.com/6238/VibeScout)")
	return tbaHTTP.Do(req)
}

func decodeTBAList[T any](resp *http.Response, dest *[]T) error {
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("tba %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	return json.NewDecoder(resp.Body).Decode(dest)
}

// decodeTBAObject is decodeTBAList for a single JSON object response instead
// of a list.
func decodeTBAObject(resp *http.Response, dest any) error {
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("tba %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	return json.NewDecoder(resp.Body).Decode(dest)
}

type Match struct {
	Key         string `json:"key"`
	MatchNumber int    `json:"match_number"`
	SetNumber   int    `json:"set_number"`
	CompLevel   string `json:"comp_level"`
	ActualTime  int64  `json:"actual_time"` // 0 until the match is played
	Alliances   struct {
		Red  Alliance `json:"red"`
		Blue Alliance `json:"blue"`
	} `json:"alliances"`
}

type Alliance struct {
	TeamKeys []string `json:"team_keys"`
	Score    *int     `json:"score"` // nil or -1 until the match is played
}

// Played reports whether TBA has results for the match.
func (m Match) Played() bool {
	s := m.Alliances.Red.Score
	return m.ActualTime > 0 || (s != nil && *s >= 0)
}

// ShortLabel is a compact name for the match, e.g. "Q12", "SF3-1", "F2".
func (m Match) ShortLabel() string {
	switch m.CompLevel {
	case "qm":
		return fmt.Sprintf("Q%d", m.MatchNumber)
	case "f":
		return fmt.Sprintf("F%d", m.MatchNumber)
	default:
		return fmt.Sprintf("%s%d-%d", strings.ToUpper(m.CompLevel), m.SetNumber, m.MatchNumber)
	}
}

var compLevelOrder = map[string]int{"qm": 0, "ef": 1, "qf": 2, "sf": 3, "f": 4}

// latestPlayedMatches returns up to n of the team's played matches at the event,
// most recent first.
func latestPlayedMatches(matches []Match, teamNum string, n int) []Match {
	key := "frc" + teamNum
	var played []Match
	for _, m := range matches {
		if !m.Played() {
			continue
		}
		for _, k := range append(append([]string{}, m.Alliances.Red.TeamKeys...), m.Alliances.Blue.TeamKeys...) {
			if k == key {
				played = append(played, m)
				break
			}
		}
	}
	sort.Slice(played, func(i, j int) bool {
		a, b := played[i], played[j]
		if a.ActualTime != b.ActualTime && a.ActualTime > 0 && b.ActualTime > 0 {
			return a.ActualTime > b.ActualTime
		}
		if a.CompLevel != b.CompLevel {
			return compLevelOrder[a.CompLevel] > compLevelOrder[b.CompLevel]
		}
		if a.SetNumber != b.SetNumber {
			return a.SetNumber > b.SetNumber
		}
		return a.MatchNumber > b.MatchNumber
	})
	if len(played) > n {
		played = played[:n]
	}
	return played
}

// Cache variables
var (
	eventCache     []Event
	cacheTimestamp time.Time
	cacheMutex     sync.Mutex
)

var (
	matchCache     = make(map[string][]Match)
	matchTimestamp = make(map[string]time.Time)
	matchMutex     sync.Mutex
)

func getMatchesCached(eventKey string) ([]Match, error) {
	if eventKey == testEventKey {
		return testMatches, nil
	}

	matchMutex.Lock()
	defer matchMutex.Unlock()

	if m, ok := matchCache[eventKey]; ok && time.Since(matchTimestamp[eventKey]) < 10*time.Minute {
		return m, nil
	}

	resp, err := tbaDo(fmt.Sprintf("/event/%s/matches/simple", eventKey))
	if err != nil {
		return nil, err
	}

	var matches []Match
	if err := decodeTBAList(resp, &matches); err != nil {
		return nil, err
	}

	matchCache[eventKey] = matches
	matchTimestamp[eventKey] = time.Now()
	return matches, nil
}

var (
	eventTeamsCache     = make(map[string][]string)
	eventTeamsTimestamp = make(map[string]time.Time)
	eventTeamsMutex     sync.Mutex
)

// getEventTeamsCached returns the team numbers (without "frc") registered for an
// event, sorted numerically.
func getEventTeamsCached(eventKey string) ([]string, error) {
	if eventKey == testEventKey {
		seen := map[string]bool{}
		var teams []string
		for _, m := range testMatches {
			for _, t := range stripFRC(append(m.Alliances.Red.TeamKeys, m.Alliances.Blue.TeamKeys...)) {
				if !seen[t] {
					seen[t] = true
					teams = append(teams, t)
				}
			}
		}
		sortTeamNumbers(teams)
		return teams, nil
	}

	eventTeamsMutex.Lock()
	defer eventTeamsMutex.Unlock()

	if t, ok := eventTeamsCache[eventKey]; ok && time.Since(eventTeamsTimestamp[eventKey]) < 10*time.Minute {
		return t, nil
	}

	resp, err := tbaDo(fmt.Sprintf("/event/%s/teams/keys", eventKey))
	if err != nil {
		return nil, err
	}

	var keys []string
	if err := decodeTBAList(resp, &keys); err != nil {
		return nil, err
	}

	teams := stripFRC(keys)
	sortTeamNumbers(teams)
	eventTeamsCache[eventKey] = teams
	eventTeamsTimestamp[eventKey] = time.Now()
	return teams, nil
}

var (
	rankingsCache     = make(map[string]map[string]int)
	rankingsTimestamp = make(map[string]time.Time)
	rankingsMutex     sync.Mutex
)

// getEventRankingsCached returns the event's current rankings as a map of team
// number (without "frc") to rank.
func getEventRankingsCached(eventKey string) (map[string]int, error) {
	if eventKey == testEventKey {
		return demoRankings(), nil
	}

	rankingsMutex.Lock()
	defer rankingsMutex.Unlock()

	if r, ok := rankingsCache[eventKey]; ok && time.Since(rankingsTimestamp[eventKey]) < 10*time.Minute {
		return r, nil
	}

	resp, err := tbaDo(fmt.Sprintf("/event/%s/rankings", eventKey))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("tba %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}

	var data struct {
		Rankings []struct {
			TeamKey string `json:"team_key"`
			Rank    int    `json:"rank"`
		} `json:"rankings"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, err
	}

	ranks := make(map[string]int, len(data.Rankings))
	for _, r := range data.Rankings {
		ranks[strings.TrimPrefix(r.TeamKey, "frc")] = r.Rank
	}

	rankingsCache[eventKey] = ranks
	rankingsTimestamp[eventKey] = time.Now()
	return ranks, nil
}

func sortTeamNumbers(teams []string) {
	sort.Slice(teams, func(i, j int) bool {
		a, _ := strconv.Atoi(teams[i])
		b, _ := strconv.Atoi(teams[j])
		return a < b
	})
}

func getEventsCached(year string) ([]Event, error) {
	// Always inject the test event regardless of year filter
	testEvent := Event{Key: testEventKey, Name: testEventName, StartDate: "2026-01-01"}

	cacheMutex.Lock()
	defer cacheMutex.Unlock()

	if len(eventCache) > 0 && time.Since(cacheTimestamp) < time.Hour {
		return append(eventCache, testEvent), nil
	}

	resp, err := tbaDo(fmt.Sprintf("/events/%s/simple", year))
	if err != nil {
		log.Printf("tba events fetch failed: %v", err)
		return []Event{testEvent}, nil
	}

	var events []Event
	if err := decodeTBAList(resp, &events); err != nil {
		log.Printf("tba events decode failed: %v", err)
		return []Event{testEvent}, nil
	}

	eventCache = events
	cacheTimestamp = time.Now()
	return append(events, testEvent), nil
}
