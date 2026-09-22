package main

import "testing"

func TestYouTubeWebcastIDs(t *testing.T) {
	ids := youtubeWebcastIDs([]webcast{
		{Type: "twitch", Channel: "firstinspires"},
		{Type: "youtube", Channel: "day1"},
		{Type: "youtube", Channel: "day2"},
	})
	if len(ids) != 2 || ids[0] != "day1" || ids[1] != "day2" {
		t.Errorf("got %v", ids)
	}

	if ids := youtubeWebcastIDs([]webcast{{Type: "twitch", Channel: "firstinspires"}}); len(ids) != 0 {
		t.Error("a twitch-only event should have no youtube webcasts")
	}
	if ids := youtubeWebcastIDs(nil); len(ids) != 0 {
		t.Error("no webcasts should mean no youtube webcasts")
	}
	// A malformed entry (empty channel) must not be picked.
	if ids := youtubeWebcastIDs([]webcast{{Type: "YouTube", Channel: ""}}); len(ids) != 0 {
		t.Error("an empty channel should not count as a match")
	}
}

func TestBestWebcastVideo(t *testing.T) {
	ids := []string{"day1", "day2", "day3"}
	starts := map[string]int64{"day1": 1000, "day2": 90000, "day3": 180000}

	// A match during day 2's broadcast must resolve to day2, not day1 (the
	// first one TBA listed) or day3 (which hadn't started yet).
	id, start, ok := bestWebcastVideo(ids, starts, 95000)
	if !ok || id != "day2" || start != 90000 {
		t.Errorf("got %q %d %v", id, start, ok)
	}

	// Before any broadcast has started: no match.
	if _, _, ok := bestWebcastVideo(ids, starts, 500); ok {
		t.Error("a match before every broadcast started should resolve to nothing")
	}

	// A video whose start time we never resolved (YouTube lookup failed) must
	// be skipped, not treated as starting at time zero.
	partial := map[string]int64{"day2": 90000}
	id, _, ok = bestWebcastVideo(ids, partial, 5000)
	if ok {
		t.Errorf("day1's unresolved start should not make it eligible, got %q", id)
	}
}

func TestBuildWatchURL(t *testing.T) {
	// Broadcast started at unix 1000; match happened at 1000+3600 (an hour in).
	got := buildWatchURL("vid123", 1000, 1000+3600)
	want := "https://www.youtube.com/watch?v=vid123&t=3585s" // minus the 15s lead-in
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}

	// A match "before" the broadcast's recorded start (clock skew, or the
	// broadcast started mid-match) must clamp to the beginning, never negative.
	got = buildWatchURL("vid123", 1000, 1005)
	want = "https://www.youtube.com/watch?v=vid123&t=0s"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestClipOffsets(t *testing.T) {
	// Broadcast started at unix 1000; match at 1000+3600 (an hour in).
	start, end := clipOffsets(1000, 1000+3600)
	if want := int64(3600 - videoClipLeadSeconds); start != want {
		t.Errorf("start offset = %d, want %d", start, want)
	}
	if want := start + videoClipLeadSeconds + videoClipDurationSeconds; end != want {
		t.Errorf("end offset = %d, want %d", end, want)
	}
	if end-start != videoClipLeadSeconds+videoClipDurationSeconds {
		t.Errorf("clip length should always be lead+duration, got %d", end-start)
	}

	// A match right at (or before) the broadcast's own start must clamp to 0,
	// not go negative.
	start, _ = clipOffsets(1000, 1005)
	if start != 0 {
		t.Errorf("start offset = %d, want 0", start)
	}
}

func TestParseYouTubeStart(t *testing.T) {
	ts, ok := parseYouTubeStart([]byte(`{"items":[{"liveStreamingDetails":{"actualStartTime":"2026-03-14T15:04:05Z"}}]}`))
	if !ok {
		t.Fatal("expected a parsed start time")
	}
	if want := int64(1773500645); ts != want {
		t.Errorf("got %d, want %d", ts, want)
	}

	// A video that was never a livestream has no liveStreamingDetails at all,
	// or an empty actualStartTime — either way, no timestamp to build from.
	for _, body := range []string{
		`{"items":[{}]}`,
		`{"items":[{"liveStreamingDetails":{}}]}`,
		`{"items":[]}`,
		`not json`,
	} {
		if _, ok := parseYouTubeStart([]byte(body)); ok {
			t.Errorf("expected no start time for %s", body)
		}
	}
}
