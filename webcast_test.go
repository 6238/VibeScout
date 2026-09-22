package main

import "testing"

func TestPickYouTubeWebcast(t *testing.T) {
	yt, ok := pickYouTubeWebcast([]webcast{
		{Type: "twitch", Channel: "firstinspires"},
		{Type: "youtube", Channel: "abc123"},
	})
	if !ok || yt != "abc123" {
		t.Errorf("got %q, %v", yt, ok)
	}

	if _, ok := pickYouTubeWebcast([]webcast{{Type: "twitch", Channel: "firstinspires"}}); ok {
		t.Error("a twitch-only event should have no youtube webcast")
	}
	if _, ok := pickYouTubeWebcast(nil); ok {
		t.Error("no webcasts should mean no youtube webcast")
	}
	// A malformed entry (empty channel) must not be picked.
	if _, ok := pickYouTubeWebcast([]webcast{{Type: "YouTube", Channel: ""}}); ok {
		t.Error("an empty channel should not count as a match")
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
