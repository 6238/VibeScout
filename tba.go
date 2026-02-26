package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

const (
	TBA_BASE = "https://www.thebluealliance.com/api/v3"
	TBA_KEY  = "jZMSZytdjtlkKq3EnZwSjRNSGgfa9ruJ3ogIAJkvOp7JXFD29S4FrTi4fMvb6gCA"
)

type Match struct {
	Key         string `json:"key"`
	MatchNumber int    `json:"match_number"`
	CompLevel   string `json:"comp_level"` // "qm", "qf", "sf", "f"
	Alliances   struct {
		Red  Alliance `json:"red"`
		Blue Alliance `json:"blue"`
	} `json:"alliances"`
	Winner         string         `json:"winner"`
	Score          Score          `json:"scores"`
	ScoreBreakdown ScoreBreakdown `json:"score_breakdown"`
}

type Score struct {
	Red  int `json:"red"`
	Blue int `json:"blue"`
}

type ScoreBreakdown struct {
	Red  map[string]interface{} `json:"red"`
	Blue map[string]interface{} `json:"blue"`
}

func getHubScore(breakdown map[string]interface{}, color string) int {
	// Try different possible field names for hub score
	if v, ok := breakdown["hubScore"]; ok {
		if f, ok := v.(float64); ok {
			return int(f)
		}
	}
	if v, ok := breakdown["teleopPoints"]; ok {
		if f, ok := v.(float64); ok {
			return int(f)
		}
	}
	// Try to get total points
	if v, ok := breakdown["totalPoints"]; ok {
		if f, ok := v.(float64); ok {
			return int(f)
		}
	}
	return 0
}

func getFoulPoints(breakdown map[string]interface{}) int {
	if v, ok := breakdown["foulPoints"]; ok {
		if f, ok := v.(float64); ok {
			return int(f)
		}
	}
	return 0
}

type Alliance struct {
	TeamKeys []string `json:"team_keys"`
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
	matchMutex.Lock()
	defer matchMutex.Unlock()

	// Cache check (valid for 10 minutes since schedules change)
	if m, ok := matchCache[eventKey]; ok && time.Since(matchTimestamp[eventKey]) < 10*time.Minute {
		return m, nil
	}

	req, _ := http.NewRequest("GET", fmt.Sprintf("%s/event/%s/matches/simple", TBA_BASE, eventKey), nil)
	req.Header.Set("X-TBA-Auth-Key", TBA_KEY)

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var matches []Match
	if err := json.NewDecoder(resp.Body).Decode(&matches); err != nil {
		return nil, err
	}

	matchCache[eventKey] = matches
	matchTimestamp[eventKey] = time.Now()
	return matches, nil
}

func getMatchesWithBreakdown(eventKey string) ([]Match, error) {
	req, _ := http.NewRequest("GET", fmt.Sprintf("%s/event/%s/matches", TBA_BASE, eventKey), nil)
	req.Header.Set("X-TBA-Auth-Key", TBA_KEY)

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var matches []Match
	if err := json.NewDecoder(resp.Body).Decode(&matches); err != nil {
		return nil, err
	}

	return matches, nil
}

func getEventsCached(year string) ([]Event, error) {
	cacheMutex.Lock()
	defer cacheMutex.Unlock()

	// Return cached data if it's less than 1 hour old
	if len(eventCache) > 0 && time.Since(cacheTimestamp) < time.Hour {
		return eventCache, nil
	}

	req, _ := http.NewRequest("GET", fmt.Sprintf("%s/events/%s/simple", TBA_BASE, year), nil)
	req.Header.Set("X-TBA-Auth-Key", TBA_KEY)

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var events []Event
	if err := json.NewDecoder(resp.Body).Decode(&events); err != nil {
		return nil, err
	}

	// Update the cache
	eventCache = events
	cacheTimestamp = time.Now()

	return events, nil
}
