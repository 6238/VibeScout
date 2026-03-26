package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sync"
	"time"
)

const TBA_BASE = "https://www.thebluealliance.com/api/v3"

func tbaKey() string {
	if k := os.Getenv("TBA_API_KEY"); k != "" {
		return k
	}
	return ""
}

type Match struct {
	Key         string `json:"key"`
	MatchNumber int    `json:"match_number"`
	CompLevel   string `json:"comp_level"`
	Alliances   struct {
		Red  Alliance `json:"red"`
		Blue Alliance `json:"blue"`
	} `json:"alliances"`
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

	if m, ok := matchCache[eventKey]; ok && time.Since(matchTimestamp[eventKey]) < 10*time.Minute {
		return m, nil
	}

	req, _ := http.NewRequest("GET", fmt.Sprintf("%s/event/%s/matches/simple", TBA_BASE, eventKey), nil)
	req.Header.Set("X-TBA-Auth-Key", tbaKey())

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

func getEventsCached(year string) ([]Event, error) {
	cacheMutex.Lock()
	defer cacheMutex.Unlock()

	if len(eventCache) > 0 && time.Since(cacheTimestamp) < time.Hour {
		return eventCache, nil
	}

	req, _ := http.NewRequest("GET", fmt.Sprintf("%s/events/%s/simple", TBA_BASE, year), nil)
	req.Header.Set("X-TBA-Auth-Key", tbaKey())

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

	eventCache = events
	cacheTimestamp = time.Now()
	return events, nil
}
