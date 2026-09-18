package main

import (
	"strings"
	"testing"
)

func TestDetectCurrentTeam(t *testing.T) {
	teams := []string{"8", "254", "1678", "6238"}

	tests := []struct {
		text string
		want string
	}{
		{"team 254 great auto", "254"},
		{"Team 8 intake jammed", "8"},
		{"scored 2 coral then team 1678 played defense", "1678"},
		{"254 cycling, then 1678 broke", "1678"},
		{"they scored 2", ""},
		{"nothing useful yet", ""},
		{"team 9999 is not in this match", ""},
		{"team 8 then 254", "254"},
	}

	for _, tt := range tests {
		if got := detectCurrentTeam(tt.text, teams); got != tt.want {
			t.Errorf("detectCurrentTeam(%q) = %q, want %q", tt.text, got, tt.want)
		}
	}
}

func TestDetectCurrentTeamIgnoresOpponentMentions(t *testing.T) {
	teams := []string{"1002", "1003", "1006"}
	tests := []struct {
		text string
		want string
	}{
		{"struggling a little bit against some defense from team 1006", ""},
		{"Team 1002 doing a nice sweep. Struggling against defense from team 1006", "1002"},
		{"team 1006 played defense", "1006"},
		{"against team 1006", ""},
		{"defense from 1006", ""},
		{"Really good sweep of their alliance zone, timing their dump to avoid team 1006's defense", ""},
		{"Team 1003 really good sweep, timing their dump to avoid team 1006's defense", "1003"},
		{"Team 1006's auto was messy", "1006"},
	}

	for _, tt := range tests {
		if got := detectCurrentTeam(tt.text, teams); got != tt.want {
			t.Errorf("detectCurrentTeam(%q) = %q, want %q", tt.text, got, tt.want)
		}
	}
}

func TestOpponentOnlyStrugglingNoteStaysOnFocus(t *testing.T) {
	if !isOpponentOnly("struggling against some defense from team 1006", "1006", []string{"1002", "1006"}) {
		t.Fatal("1006 should be opponent-only")
	}
	if isOpponentOnly("team 1006 played defense", "1006", []string{"1002", "1006"}) {
		t.Fatal("1006 is the subject here")
	}
	avoid := "Really good sweep of their alliance zone, timing their dump to avoid team 1006's defense"
	if !isOpponentOnly(avoid, "1006", []string{"1003", "1006"}) {
		t.Fatal("1006 in avoid-defense should be opponent-only")
	}
}

func TestFormatTeamNotes(t *testing.T) {
	got := formatTeamNotes([]SortedNote{
		{Team: "254", Category: "auto", Text: "two coral"},
		{Team: "254", Category: "auto", Text: "two coral"},
		{Team: "254", Category: "reliability", Text: "intake jammed"},
		{Team: "254", Category: "weird", Text: "called a timeout"},
	})
	want := "Auto: two coral\nReliability: intake jammed\nOther: called a timeout"
	if got != want {
		t.Errorf("formatTeamNotes() = %q, want %q", got, want)
	}
}

func TestFormatNotesByTeam(t *testing.T) {
	got := formatNotesByTeam([]SortedNote{
		{Team: "254", Category: "auto", Text: "moved"},
		{Team: "1678", Category: "defense", Text: "played D"},
	})
	if got["254"] != "Auto: moved" {
		t.Errorf("team 254 notes = %q", got["254"])
	}
	if got["1678"] != "Defense: played D" {
		t.Errorf("team 1678 notes = %q", got["1678"])
	}
}

func TestLeftoverIfRewrite(t *testing.T) {
	leftover, ok := leftoverIfRewrite("intake jammed", "Team 254 intake jammed")
	if !ok {
		t.Fatal("expected rewrite")
	}
	if leftover != "Team 254" {
		t.Errorf("leftover = %q, want %q", leftover, "Team 254")
	}

	leftover, ok = leftoverIfRewrite("intake jammed", "Team 254 intake jammed cycling fast")
	if !ok {
		t.Fatal("expected rewrite of growing turn")
	}
	if leftover != "Team 254 cycling fast" {
		t.Errorf("leftover = %q, want %q", leftover, "Team 254 cycling fast")
	}

	leftover, ok = leftoverIfRewrite("intake jammed", "team 1678 played defense")
	if ok {
		t.Fatalf("independent utterance should not be a rewrite, leftover %q", leftover)
	}
}

func TestStripAndCleanTeamMentions(t *testing.T) {
	teams := []string{"8", "254", "1678"}
	got := cleanNoteText("Team 254 intake jammed", "254")
	if got != "intake jammed" {
		t.Errorf("cleanNoteText() = %q", got)
	}
	got = stripTeamMentions("Team 254", teams)
	if got != "" {
		t.Errorf("stripTeamMentions(team only) = %q, want empty", got)
	}
	got = cleanNoteText("scored 2 coral", "254")
	if got != "scored 2 coral" {
		t.Errorf("should keep game-piece counts, got %q", got)
	}
	got = cleanNoteText("timing their dump to avoid team 1006's defense", "1003")
	if !strings.Contains(got, "1006") {
		t.Errorf("should keep opponent team number, got %q", got)
	}
	got = cleanNoteText("struggling against defense from team 1006", "1002")
	if !strings.Contains(got, "1006") {
		t.Errorf("should keep 1006 on 1002's note, got %q", got)
	}
}

func TestAppendNotesDoesNotRewrite(t *testing.T) {
	dst := []SortedNote{{Team: "1001", Category: "reliability", Text: "intake jammed"}}
	got := appendNotes(dst, []SortedNote{
		{Team: "1001", Category: "reliability", Text: "intake jammed"},
		{Team: "254", Category: "scoring", Text: "cycling fast"},
	})
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2: %+v", len(got), got)
	}
	if got[0].Text != "intake jammed" {
		t.Errorf("original note changed: %q", got[0].Text)
	}
}

func TestMergeRelatedNotesKeepsAutoTogether(t *testing.T) {
	got := mergeRelatedNotes([]SortedNote{
		{Team: "1001", Category: "auto", Text: "ran an interesting second bot auto"},
		{Team: "1001", Category: "driving", Text: "went out behind their teammate"},
	})
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1: %+v", len(got), got)
	}
	if got[0].Category != "auto" {
		t.Errorf("category = %q, want auto", got[0].Category)
	}
	if !strings.Contains(got[0].Text, "second bot auto") || !strings.Contains(got[0].Text, "behind their teammate") {
		t.Errorf("merged text = %q", got[0].Text)
	}
}

func TestMergeRelatedNotesKeepsTeleopDrivingSeparate(t *testing.T) {
	got := mergeRelatedNotes([]SortedNote{
		{Team: "1001", Category: "auto", Text: "scored one in auto"},
		{Team: "1001", Category: "driving", Text: "teleop driving was sloppy"},
	})
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2: %+v", len(got), got)
	}
}
