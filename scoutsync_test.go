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

// TestSaveAIGeneratedNoteRetryIsIdempotent: retrying the video-fill admin
// tool for a team it already filled (a page reload, a double click) must
// update that row, not insert a duplicate that would then look like a
// second, independent scout to combineTeamNotes.
func TestSaveAIGeneratedNoteRetryIsIdempotent(t *testing.T) {
	useTempDB(t)

	if err := saveAIGeneratedNote("2026test", 1, "254", "first pass: scored well"); err != nil {
		t.Fatal(err)
	}
	if n := countScoutRows(t, "2026test", "254"); n != 1 {
		t.Fatalf("after first save: %d rows, want 1", n)
	}

	if err := saveAIGeneratedNote("2026test", 1, "254", "retried: scored well, climbed too"); err != nil {
		t.Fatal(err)
	}
	if n := countScoutRows(t, "2026test", "254"); n != 1 {
		t.Fatalf("after retry: %d rows, want 1 (no duplicate)", n)
	}
	var notes string
	db.QueryRow(`SELECT notes FROM scout_submissions WHERE event_key = ? AND team_number = ?`, "2026test", "254").Scan(&notes)
	if want := "retried: scored well, climbed too"; notes != want {
		t.Errorf("retry should update to the latest content: got %q, want %q", notes, want)
	}
}

// TestSaveAIGeneratedNoteDoesNotCollideWithHumanNotes: the AI-fill dedupe key
// only applies among ai_generated=1 rows, so a human note for the same
// match/team (submitted before or after the AI ran) must not be overwritten
// or blocked by it.
func TestSaveAIGeneratedNoteDoesNotCollideWithHumanNotes(t *testing.T) {
	useTempDB(t)

	saveScoutSubmission(ScoutSubmission{
		EventKey: "2026test", MatchNum: 1,
		Teams: []TeamScoutData{{TeamNumber: "254", Notes: "human note"}},
	})
	if err := saveAIGeneratedNote("2026test", 1, "254", "ai note"); err != nil {
		t.Fatal(err)
	}

	if n := countScoutRows(t, "2026test", "254"); n != 2 {
		t.Errorf("a human row and an AI row for the same match should both exist: got %d rows, want 2", n)
	}
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

// addClarification inserts a clarification row directly, the way
// apiAddClarificationHandler does, without going through an HTTP request.
func addClarification(t *testing.T, eventKey, teamNum, noteType, text, author string) {
	t.Helper()
	if _, err := db.Exec(`
		INSERT INTO note_clarifications (event_key, team_number, note_type, clarification, author)
		VALUES (?, ?, ?, ?, ?)`,
		eventKey, teamNum, noteType, text, author); err != nil {
		t.Fatal(err)
	}
}

// TestCombineTeamNotesAppendsFieldClarification: a clarification on a team's
// match notes must reach Gemini, appended after the notes it clarifies —
// not silently swapped in over the original scout's words.
func TestCombineTeamNotesAppendsFieldClarification(t *testing.T) {
	useTempDB(t)

	saveScoutSubmission(ScoutSubmission{
		EventKey: "2026test", MatchNum: 1,
		Teams: []TeamScoutData{{TeamNumber: "254", Notes: "e-stopped mid match"}},
	})
	addClarification(t, "2026test", "254", "field", "The e-stop was a driver safety call, not a robot failure.", "Elliot")

	got := scoutNoteFor(t, "2026test", "254")
	if !strings.Contains(got, "Match 1: e-stopped mid match") {
		t.Errorf("original note should be preserved untouched, got %q", got)
	}
	if !strings.Contains(got, "Clarifications from strategy team:\n- The e-stop was a driver safety call, not a robot failure. (Elliot)") {
		t.Errorf("expected the clarification appended with its author, got %q", got)
	}
}

// TestPitNotesForAppendsPitClarification: same as above, for pit notes —
// e.g. "2.5 cycles" meaning during auto, not the whole match.
func TestPitNotesForAppendsPitClarification(t *testing.T) {
	useTempDB(t)

	db.Exec(`INSERT INTO pit_scouting (team_number, summary) VALUES (?, ?)`, "254", "Claims 2.5 cycles.")
	addClarification(t, "", "254", "pit", `"2.5 cycles" means during auto, not the whole match.`, "")

	got := pitNotesFor("254")
	if !strings.HasPrefix(got, "Claims 2.5 cycles.") {
		t.Errorf("original pit summary should be preserved untouched, got %q", got)
	}
	want := `Clarifications from strategy team:` + "\n" + `- "2.5 cycles" means during auto, not the whole match.`
	if !strings.Contains(got, want) {
		t.Errorf("expected the clarification appended with no author suffix, got %q", got)
	}
}

// TestClarificationsFieldScopedToEvent: a field clarification is only about
// the notes from the event it was added at, matching combineTeamNotes
// itself — it must not leak into a different event's read of the same team.
func TestClarificationsFieldScopedToEvent(t *testing.T) {
	useTempDB(t)

	saveScoutSubmission(ScoutSubmission{
		EventKey: "2026other", MatchNum: 1,
		Teams: []TeamScoutData{{TeamNumber: "254", Notes: "scored well"}},
	})
	addClarification(t, "2026test", "254", "field", "this only applies at 2026test", "")

	got := scoutNoteFor(t, "2026other", "254")
	if strings.Contains(got, "Clarifications") {
		t.Errorf("a clarification from a different event should not appear here, got %q", got)
	}
}

// TestClarificationsPitNotScopedToEvent: a pit clarification applies no
// matter which event's analysis is reading the pit notes, matching
// pit_scouting's own lack of event scoping.
func TestClarificationsPitNotScopedToEvent(t *testing.T) {
	useTempDB(t)

	db.Exec(`INSERT INTO pit_scouting (team_number, summary) VALUES (?, ?)`, "254", "Claims 2.5 cycles.")
	addClarification(t, "", "254", "pit", "means during auto", "")

	block := clarificationsBlock("2026test", "254", "pit")
	if !strings.Contains(block, "means during auto") {
		t.Errorf("a pit clarification should show up regardless of event_key, got %q", block)
	}
	block = clarificationsBlock("2026other", "254", "pit")
	if !strings.Contains(block, "means during auto") {
		t.Errorf("a pit clarification should show up for any event_key, got %q", block)
	}
}

// TestClarificationsWithNoUnderlyingNotesNotAppended: a clarification on a
// team that hasn't actually been scouted/pit-scouted yet shouldn't produce
// dangling "Clarifications:" text with nothing for it to clarify.
func TestClarificationsWithNoUnderlyingNotesNotAppended(t *testing.T) {
	useTempDB(t)

	addClarification(t, "2026test", "254", "field", "a clarification with nothing to clarify", "")
	if got := scoutNoteFor(t, "2026test", "254"); got != "" {
		t.Errorf("no field notes exist, so nothing should be returned, got %q", got)
	}

	addClarification(t, "", "254", "pit", "a clarification with nothing to clarify", "")
	if got := pitNotesFor("254"); got != "" {
		t.Errorf("no pit notes exist, so nothing should be returned, got %q", got)
	}
}

// TestDeletedClarificationNoLongerAppended: once a clarification is deleted
// (the same way apiDeleteClarificationHandler does it — matching on id,
// team_number and note_type), it must stop showing up anywhere, including in
// what Gemini reads. This is the fix for a clarification going stale once
// the note it was about has been edited directly.
func TestDeletedClarificationNoLongerAppended(t *testing.T) {
	useTempDB(t)

	saveScoutSubmission(ScoutSubmission{
		EventKey: "2026test", MatchNum: 1,
		Teams: []TeamScoutData{{TeamNumber: "254", Notes: "e-stopped mid match"}},
	})
	addClarification(t, "2026test", "254", "field", "this turned out to be wrong, ignore it", "")

	items := clarificationsFor("2026test", "254", "field")
	if len(items) != 1 {
		t.Fatalf("expected 1 clarification before deleting, got %d", len(items))
	}

	res, err := db.Exec(`DELETE FROM note_clarifications WHERE id = ? AND team_number = ? AND note_type = ?`,
		items[0].ID, "254", "field")
	if err != nil {
		t.Fatal(err)
	}
	if n, _ := res.RowsAffected(); n != 1 {
		t.Fatalf("expected the delete to affect 1 row, got %d", n)
	}

	got := scoutNoteFor(t, "2026test", "254")
	if strings.Contains(got, "Clarifications") {
		t.Errorf("deleted clarification should no longer be appended, got %q", got)
	}
	if !strings.Contains(got, "Match 1: e-stopped mid match") {
		t.Errorf("the original note should be untouched, got %q", got)
	}
}
