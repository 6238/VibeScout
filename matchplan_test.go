package main

import (
	"os"
	"strings"
	"testing"

	"vibe-scout/templates"
)

func TestPitProfileText(t *testing.T) {
	if got := pitProfileText(templates.PitProfile{}); got != "" {
		t.Errorf("empty profile should give no text, got %q", got)
	}
	got := pitProfileText(templates.PitProfile{Has: true, Archetype: "Defender", Role: "Defense", Problem: "slow turning"})
	for _, want := range []string{"archetype: Defender", "prefers: Defense", "biggest problem: slow turning"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %q", want, got)
		}
	}
	if strings.Contains(got, "strength") {
		t.Errorf("blank fields should be left out, got %q", got)
	}
}

// useTempDB points the package at a fresh database in a temp folder, loaded with
// the demo event, and restores the working directory afterwards.
func useTempDB(t *testing.T) {
	t.Helper()
	old, _ := os.Getwd()
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	initDB()
	seedTestData()
	t.Cleanup(func() {
		db.Close()
		os.Chdir(old)
	})
}

func TestDefenseRecord(t *testing.T) {
	useTempDB(t)

	// Demo data: 1003 and 1008 have "played defense" checklists; 1001 and 1005 don't.
	none := defenseRecord(testEventKey, []string{"1001", "1005"}, nil)
	if !strings.Contains(none, "NONE") {
		t.Errorf("no defenders expected, got %q", none)
	}

	fromChecklist := defenseRecord(testEventKey, []string{"1001", "1003"}, nil)
	if strings.Contains(fromChecklist, "NONE") || !strings.Contains(fromChecklist, "1003 (played defense in 4 of 4") {
		t.Errorf("1003's checklists should count, got %q", fromChecklist)
	}
	if strings.Contains(fromChecklist, "1001 (") {
		t.Errorf("1001 has no defense record, got %q", fromChecklist)
	}

	// The pit interview alone is enough too.
	withPit := defenseRecord(testEventKey, []string{"1001", "1005"},
		map[string]templates.PitProfile{"1005": {Has: true, Archetype: "Defender"}})
	if !strings.Contains(withPit, "1005 (pit interview says defender)") {
		t.Errorf("a pit defender should be listed, got %q", withPit)
	}
}

func TestOpponentDefense(t *testing.T) {
	useTempDB(t)

	// Q10's opponents (1001, 1009, 1007) have no played-defense checklist and no
	// defender pit profile.
	if got := opponentDefense(testEventKey, []string{"1001", "1009", "1007"}, nil); got != "Opponents with a defense record: NONE." {
		t.Errorf("Q10 opponents have no defense record, got %q", got)
	}
	// Q11's opponents include 1008, who played defense in checklist matches.
	got := opponentDefense(testEventKey, []string{"1002", "1004", "1008"}, nil)
	if !strings.Contains(got, "1008 (played defense in 3 of 3") || strings.Contains(got, "NONE") {
		t.Errorf("1008 should be listed as a defender, got %q", got)
	}
}

func TestForecastTextFlipsForBlue(t *testing.T) {
	m, _ := findQualMatch(testMatches, 10) // demo: red 162, blue 165
	red := forecastText(m, true)
	blue := forecastText(m, false)
	if !strings.Contains(red, "us 162, them 165") {
		t.Errorf("red side forecast wrong: %q", red)
	}
	if !strings.Contains(blue, "us 165, them 162") {
		t.Errorf("blue side forecast wrong: %q", blue)
	}
	if red == blue {
		t.Error("the two sides should see different chances to win")
	}
}
