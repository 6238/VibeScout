package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
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
