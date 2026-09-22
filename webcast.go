package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

// webcast is one entry from TBA's /event/{key}/webcasts. For a YouTube webcast,
// Channel is the video ID (TBA's own naming, not ours).
type webcast struct {
	Type    string `json:"type"`
	Channel string `json:"channel"`
}

var (
	webcastsCache = map[string][]webcast{}
	webcastsAt    = map[string]time.Time{}
	webcastsMu    sync.Mutex
)

// eventWebcasts fetches (and caches) an event's webcasts from TBA.
func eventWebcasts(eventKey string) ([]webcast, error) {
	webcastsMu.Lock()
	if w, ok := webcastsCache[eventKey]; ok && time.Since(webcastsAt[eventKey]) < 30*time.Minute {
		webcastsMu.Unlock()
		return w, nil
	}
	webcastsMu.Unlock()

	resp, err := tbaDo(fmt.Sprintf("/event/%s/webcasts", eventKey))
	if err != nil {
		return nil, err
	}
	var list []webcast
	if err := decodeTBAList(resp, &list); err != nil {
		return nil, err
	}

	webcastsMu.Lock()
	webcastsCache[eventKey] = list
	webcastsAt[eventKey] = time.Now()
	webcastsMu.Unlock()
	return list, nil
}

// pickYouTubeWebcast returns the video ID of the first YouTube webcast in the
// list, if any. Most events have exactly one; if there's a backup stream too,
// we take the first, same as TBA's own match video preference.
func pickYouTubeWebcast(list []webcast) (videoID string, ok bool) {
	for _, w := range list {
		if strings.EqualFold(w.Type, "youtube") && w.Channel != "" {
			return w.Channel, true
		}
	}
	return "", false
}

// watchLeadSeconds backs the link up a little before the match's recorded start,
// so the viewer sees the field reset and queue rather than starting mid-action.
const watchLeadSeconds = 15

// buildWatchURL is the pure part of the calculation: given a YouTube video ID,
// when that broadcast actually started (unix seconds), and when the match
// actually happened (unix seconds), it returns the deep link into the video.
func buildWatchURL(videoID string, startUnix, matchTime int64) string {
	offset := matchTime - startUnix - watchLeadSeconds
	if offset < 0 {
		offset = 0
	}
	return fmt.Sprintf("https://www.youtube.com/watch?v=%s&t=%ds", url.QueryEscape(videoID), offset)
}

// parseYouTubeStart reads a YouTube Data API v3 videos.list response (with
// part=liveStreamingDetails) and returns when the broadcast actually started.
func parseYouTubeStart(body []byte) (int64, bool) {
	var data struct {
		Items []struct {
			LiveStreamingDetails struct {
				ActualStartTime string `json:"actualStartTime"`
			} `json:"liveStreamingDetails"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &data); err != nil || len(data.Items) == 0 {
		return 0, false
	}
	raw := data.Items[0].LiveStreamingDetails.ActualStartTime
	if raw == "" {
		return 0, false // video exists but was never (or isn't yet) a livestream
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return 0, false
	}
	return t.Unix(), true
}

var (
	ytStartCache = map[string]int64{}
	ytFailedAt   = map[string]time.Time{}
	ytMu         sync.Mutex
)

const youtubeVideosURL = "https://www.googleapis.com/youtube/v3/videos"

// youtubeVideoStart returns (and caches) when a YouTube broadcast actually
// started. A successful lookup is cached for the process lifetime, since a
// past broadcast's start time never changes; a failure is retried after a
// while, in case the key was missing at startup or the video wasn't live yet.
func youtubeVideoStart(videoID string) (int64, bool) {
	ytMu.Lock()
	if t, ok := ytStartCache[videoID]; ok {
		ytMu.Unlock()
		return t, true
	}
	if at, failed := ytFailedAt[videoID]; failed && time.Since(at) < 10*time.Minute {
		ytMu.Unlock()
		return 0, false
	}
	ytMu.Unlock()

	apiKey := strings.TrimSpace(os.Getenv("YOUTUBE_API_KEY"))
	if apiKey == "" {
		return 0, false // feature is simply off until a key is configured
	}

	reqURL := fmt.Sprintf("%s?part=liveStreamingDetails&id=%s&key=%s",
		youtubeVideosURL, url.QueryEscape(videoID), url.QueryEscape(apiKey))
	resp, err := httpClient.Get(reqURL)
	start, ok := int64(0), false
	if err == nil {
		defer resp.Body.Close()
		if resp.StatusCode == 200 {
			if body, readErr := io.ReadAll(resp.Body); readErr == nil {
				start, ok = parseYouTubeStart(body)
			}
		}
	}

	ytMu.Lock()
	if ok {
		ytStartCache[videoID] = start
	} else {
		ytFailedAt[videoID] = time.Now()
	}
	ytMu.Unlock()
	return start, ok
}

// matchWatchURL returns a link into the event's YouTube broadcast at the
// moment this match actually happened, when we have everything needed for
// that: a played match with a recorded start time, a YouTube webcast for the
// event, and (via youtubeVideoStart) that broadcast's own start time.
func matchWatchURL(eventKey string, m Match) (string, bool) {
	if m.ActualTime <= 0 {
		return "", false // TBA has no timestamp for this match
	}
	list, err := eventWebcasts(eventKey)
	if err != nil || len(list) == 0 {
		return "", false
	}
	videoID, ok := pickYouTubeWebcast(list)
	if !ok {
		return "", false
	}
	start, ok := youtubeVideoStart(videoID)
	if !ok {
		return "", false
	}
	return buildWatchURL(videoID, start, m.ActualTime), true
}
