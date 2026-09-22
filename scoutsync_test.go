package main

import "testing"

// countScoutRows is a small test helper.
func countScoutRows(t *testing.T, eventKey, teamNumber string) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM scout_submissions WHERE event_key = ? AND team_number = ?`,
		eventKey, teamNumber).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestSaveScoutSubmissionRetryIsIdempotent(t *testing.T) {
	useTempDB(t)

	sub := ScoutSubmission{
		EventKey: "2026test", MatchNum: 1, ScouterName: "Tester", SubmissionID: "attempt-1",
		Teams: []TeamScoutData{{TeamNumber: "254", Notes: "first try, request may have actually landed"}},
	}
	if err := saveScoutSubmission(sub); err != nil {
		t.Fatal(err)
	}
	if n := countScoutRows(t, "2026test", "254"); n != 1 {
		t.Fatalf("after first save: %d rows, want 1", n)
	}

	// The classic bad-wifi case: the first request actually saved, but its
	// response never made it back, so the client thinks it failed and retries
	// with the SAME submission_id — possibly with slightly different notes if
	// the scout kept typing before the retry fired.
	sub.Teams[0].Notes = "retried after the first response was lost"
	if err := saveScoutSubmission(sub); err != nil {
		t.Fatal(err)
	}
	if n := countScoutRows(t, "2026test", "254"); n != 1 {
		t.Fatalf("after retry with the same submission_id: %d rows, want 1 (no duplicate)", n)
	}
	var notes string
	db.QueryRow(`SELECT notes FROM scout_submissions WHERE event_key = ? AND team_number = ?`, "2026test", "254").Scan(&notes)
	if want := "retried after the first response was lost"; notes != want {
		t.Errorf("retry should update to the latest content: got %q, want %q", notes, want)
	}
}

func TestSaveScoutSubmissionDifferentIDsDontCollide(t *testing.T) {
	useTempDB(t)

	sub := ScoutSubmission{
		EventKey: "2026test", MatchNum: 1, SubmissionID: "attempt-a",
		Teams: []TeamScoutData{{TeamNumber: "254", Notes: "a"}},
	}
	saveScoutSubmission(sub)
	sub.SubmissionID = "attempt-b" // a deliberate second scout, not a retry
	sub.Teams[0].Notes = "b"
	saveScoutSubmission(sub)

	if n := countScoutRows(t, "2026test", "254"); n != 2 {
		t.Errorf("two genuinely different submissions should both be kept: got %d rows, want 2", n)
	}
}

func TestSaveScoutSubmissionEmptyIDKeepsOldBehavior(t *testing.T) {
	useTempDB(t)

	// No submission_id at all (older client, or the AI video-fill admin tool):
	// every save must still insert a fresh row, exactly as before retries existed.
	sub := ScoutSubmission{EventKey: "2026test", MatchNum: 2, Teams: []TeamScoutData{{TeamNumber: "111", Notes: "a"}}}
	saveScoutSubmission(sub)
	saveScoutSubmission(sub)

	if n := countScoutRows(t, "2026test", "111"); n != 2 {
		t.Errorf("callers with no submission_id should be unaffected by the dedupe constraint: got %d rows, want 2", n)
	}
}
