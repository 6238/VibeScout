package main

import "testing"

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
