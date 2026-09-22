package main

import (
	"strings"
	"testing"
)

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

// scoutNoteFor re-reads a team's notes the way getOrGenerateAnalysis does, so
// a test sees exactly the text Gemini would.
func scoutNoteFor(t *testing.T, eventKey, teamNumber string) string {
	t.Helper()
	note, err := combineTeamNotes(eventKey, teamNumber)
	if err != nil {
		t.Fatal(err)
	}
	return note
}

// TestFieldScoutingTagsBecomeStructuredChecklist covers the field-scouting
// quick-tag buttons (Broke / Played Defense / Was Defended): they must land
// in the structured has_checklist/broke/played_defense/was_defended columns,
// not get hand-merged into the free-text notes — see saveAndAdvance in
// scout.templ. combineTeamNotes is what turns the columns back into a
// sentence, so round-tripping through it is what actually proves the wiring
// works.
func TestFieldScoutingTagsBecomeStructuredChecklist(t *testing.T) {
	useTempDB(t)

	sub := ScoutSubmission{
		EventKey: "2026test", MatchNum: 1,
		Teams: []TeamScoutData{{
			TeamNumber: "254", Notes: "scored a lot in auto",
			HasChecklist: true, Broke: true, PlayedDefense: false, WasDefended: true,
		}},
	}
	if err := saveScoutSubmission(sub); err != nil {
		t.Fatal(err)
	}

	got := scoutNoteFor(t, "2026test", "254")
	want := "Match 1: scored a lot in auto [Auto: not recorded; Broke: yes; Played defense: no; Was defended: yes]"
	if got != want {
		t.Errorf("combineTeamNotes didn't reconstruct the checklist:\n got:  %q\n want: %q", got, want)
	}
}

// TestCombineTeamNotesPrioritizesSingleTeamScout: when two scouts cover the
// same match, the one who was watching only this robot (not splitting
// attention across an alliance) should be labeled as the more focused
// account, and neither scout's notes should be dropped.
func TestCombineTeamNotesPrioritizesSingleTeamScout(t *testing.T) {
	useTempDB(t)

	saveScoutSubmission(ScoutSubmission{
		EventKey: "2026test", MatchNum: 1, SubmissionID: "a",
		Teams: []TeamScoutData{{TeamNumber: "254", Notes: "dominant scoring all match", SingleTeam: true}},
	})
	saveScoutSubmission(ScoutSubmission{
		EventKey: "2026test", MatchNum: 1, SubmissionID: "b",
		Teams: []TeamScoutData{{TeamNumber: "254", Notes: "seemed fine, was watching two other robots too", SingleTeam: false}},
	})

	got := scoutNoteFor(t, "2026test", "254")
	if !strings.Contains(got, "Match 1 (scouted by 2 people)") {
		t.Errorf("expected the match to be labeled as scouted by 2 people, got %q", got)
	}
	if !strings.Contains(got, "focused scout (watching only this robot): dominant scoring all match") {
		t.Errorf("expected the single-team scout's note to be labeled as the focused account, got %q", got)
	}
	if !strings.Contains(got, "scout also watching other robots: seemed fine") {
		t.Errorf("expected the multi-team scout's note to still be included, labeled, got %q", got)
	}
}

// TestCombineTeamNotesMergesAndFlagsChecklistDisagreement: a direct
// contradiction on broke/played_defense/was_defended between two scouts
// should OR together (favoring the report that something happened) and be
// called out explicitly rather than silently picked one way or the other.
func TestCombineTeamNotesMergesAndFlagsChecklistDisagreement(t *testing.T) {
	useTempDB(t)

	saveScoutSubmission(ScoutSubmission{
		EventKey: "2026test", MatchNum: 1, SubmissionID: "a",
		Teams: []TeamScoutData{{TeamNumber: "254", Notes: "", HasChecklist: true, Broke: true}},
	})
	saveScoutSubmission(ScoutSubmission{
		EventKey: "2026test", MatchNum: 1, SubmissionID: "b",
		Teams: []TeamScoutData{{TeamNumber: "254", Notes: "", HasChecklist: true, Broke: false}},
	})

	got := scoutNoteFor(t, "2026test", "254")
	if !strings.Contains(got, "Broke: yes") {
		t.Errorf("expected the merged checklist to OR toward Broke: yes, got %q", got)
	}
	if !strings.Contains(got, "scouts disagreed on: Broke") {
		t.Errorf("expected the disagreement to be flagged explicitly, got %q", got)
	}
}

// TestCombineTeamNotesNoDisagreementNotFlagged: when scouts' checklists
// actually agree, nothing should claim they disagreed.
func TestCombineTeamNotesNoDisagreementNotFlagged(t *testing.T) {
	useTempDB(t)

	saveScoutSubmission(ScoutSubmission{
		EventKey: "2026test", MatchNum: 1, SubmissionID: "a",
		Teams: []TeamScoutData{{TeamNumber: "254", Notes: "", HasChecklist: true, WasDefended: true}},
	})
	saveScoutSubmission(ScoutSubmission{
		EventKey: "2026test", MatchNum: 1, SubmissionID: "b",
		Teams: []TeamScoutData{{TeamNumber: "254", Notes: "", HasChecklist: true, WasDefended: true}},
	})

	got := scoutNoteFor(t, "2026test", "254")
	if strings.Contains(got, "disagreed") {
		t.Errorf("scouts agreed on every field, nothing should be flagged as disagreement: got %q", got)
	}
	if !strings.Contains(got, "Was defended: yes") {
		t.Errorf("expected the agreed-upon value to appear, got %q", got)
	}
}

// TestFieldScoutingNoTagsStillCountsAsScouted: a scout who taps no tags and
// writes no notes still submitted a real checklist (everything "no"), which
// should count as having scouted the match — not look identical to a match
// nobody ever opened the form for.
func TestFieldScoutingNoTagsStillCountsAsScouted(t *testing.T) {
	useTempDB(t)

	sub := ScoutSubmission{
		EventKey: "2026test", MatchNum: 1,
		Teams: []TeamScoutData{{TeamNumber: "254", Notes: "", HasChecklist: true}},
	}
	if err := saveScoutSubmission(sub); err != nil {
		t.Fatal(err)
	}

	var scouted bool
	if err := db.QueryRow(`SELECT EXISTS(SELECT 1 FROM scout_submissions
		WHERE event_key = ? AND team_number = ? AND `+hasScoutDataSQL+`)`,
		"2026test", "254").Scan(&scouted); err != nil {
		t.Fatal(err)
	}
	if !scouted {
		t.Error("a submitted checklist with no notes and no tags should still count as scouted")
	}
}
