package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

// webcast is one entry from an event's "webcasts" list in TBA's full event
// object (there is no standalone /event/{key}/webcasts endpoint — that 404s).
// For a YouTube webcast, Channel is the video ID (TBA's own naming, not ours).
// Multi-day events commonly have one YouTube webcast per day, each a separate
// video, so Channel alone doesn't tell us which one covers a given match.
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

	resp, err := tbaDo(fmt.Sprintf("/event/%s", eventKey))
	if err != nil {
		return nil, err
	}
	var event struct {
		Webcasts []webcast `json:"webcasts"`
	}
	if err := decodeTBAObject(resp, &event); err != nil {
		return nil, err
	}

	webcastsMu.Lock()
	webcastsCache[eventKey] = event.Webcasts
	webcastsAt[eventKey] = time.Now()
	webcastsMu.Unlock()
	return event.Webcasts, nil
}

// youtubeWebcastIDs returns the video IDs of every YouTube webcast in the
// list, in the order TBA gave them. A single-day event normally has one; a
// multi-day event normally has one per day.
func youtubeWebcastIDs(list []webcast) []string {
	var ids []string
	for _, w := range list {
		if strings.EqualFold(w.Type, "youtube") && w.Channel != "" {
			ids = append(ids, w.Channel)
		}
	}
	return ids
}

// bestWebcastVideo picks whichever of an event's YouTube broadcasts actually
// covers a match, given each video's own start time (via starts, keyed by video
// ID — only the videos that resolved get an entry). A match can only appear in
// a broadcast that had already started, so among those, the one that started
// most recently is the correct day's video; a broadcast that starts after the
// match can't be it, even if it's otherwise the closest in time.
func bestWebcastVideo(ids []string, starts map[string]int64, matchTime int64) (videoID string, startUnix int64, ok bool) {
	bestStart := int64(-1)
	for _, id := range ids {
		start, known := starts[id]
		if !known || start > matchTime {
			continue
		}
		if start > bestStart {
			bestStart, videoID, ok = start, id, true
		}
	}
	return videoID, bestStart, ok
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

// resolveBroadcast finds which of an event's YouTube webcasts actually covers
// a match, and when that broadcast started. Shared by matchWatchURL (a link
// for a person to click) and resolveWatchClip (a trimmed clip for Gemini).
func resolveBroadcast(eventKey string, m Match) (videoID string, startUnix int64, ok bool) {
	if m.ActualTime <= 0 {
		return "", 0, false // TBA has no timestamp for this match
	}
	list, err := eventWebcasts(eventKey)
	if err != nil {
		log.Printf("webcasts for %s: %v", eventKey, err)
		return "", 0, false
	}
	ids := youtubeWebcastIDs(list)
	if len(ids) == 0 {
		return "", 0, false // event isn't on YouTube (Twitch, iframe, none, ...)
	}

	starts := make(map[string]int64, len(ids))
	for _, id := range ids {
		if start, ok := youtubeVideoStart(id); ok {
			starts[id] = start
		}
	}
	return bestWebcastVideo(ids, starts, m.ActualTime)
}

// matchWatchURL returns a link into the event's YouTube broadcast at the
// moment this match actually happened, when we have everything needed for
// that. For a multi-day event it resolves every day's video and picks the
// one that actually covers the match, not just the first one TBA lists.
func matchWatchURL(eventKey string, m Match) (string, bool) {
	videoID, start, ok := resolveBroadcast(eventKey, m)
	if !ok {
		return "", false
	}
	return buildWatchURL(videoID, start, m.ActualTime), true
}

// videoClipLeadSeconds/videoClipDurationSeconds bound the clip of a broadcast
// sent to Gemini for one match: enough to catch the pre-match reset and the
// whole ~2:30 match, without making it process an entire multi-hour stream.
const (
	videoClipLeadSeconds     = 20
	videoClipDurationSeconds = 200
)

// clipOffsets is the pure part of resolveWatchClip: given when a broadcast
// started and when the match happened (both unix seconds), it returns the
// start/end offsets, in seconds from the start of the video, to send to
// Gemini so it only has to watch the relevant clip.
func clipOffsets(startUnix, matchTime int64) (startOffset, endOffset int64) {
	startOffset = matchTime - startUnix - videoClipLeadSeconds
	if startOffset < 0 {
		startOffset = 0
	}
	endOffset = startOffset + videoClipLeadSeconds + videoClipDurationSeconds
	return startOffset, endOffset
}

// resolveWatchClip finds the YouTube video and the offsets (in seconds from
// its start) covering a match, so a video-analysis call only has to watch a
// few minutes of a broadcast that might be hours long.
func resolveWatchClip(eventKey string, m Match) (videoID string, startOffset, endOffset int64, ok bool) {
	videoID, start, ok := resolveBroadcast(eventKey, m)
	if !ok {
		return "", 0, 0, false
	}
	so, eo := clipOffsets(start, m.ActualTime)
	return videoID, so, eo, true
}

// matchReviewURL is for a person going back to check a specific match, e.g.
// to settle a disagreement between two scouts' notes. It prefers TBA's own
// official match video when one has been posted — already trimmed to just
// the match, so the link needs no timestamp — and only falls back to a
// timestamped moment in the event's livestream (matchWatchURL) when TBA
// doesn't have one yet, which is normal for a while after an event.
func matchReviewURL(eventKey string, m Match) (string, bool) {
	if videoID, ok := m.OfficialYouTubeVideo(); ok {
		return fmt.Sprintf("https://www.youtube.com/watch?v=%s", url.QueryEscape(videoID)), true
	}
	return matchWatchURL(eventKey, m)
}
