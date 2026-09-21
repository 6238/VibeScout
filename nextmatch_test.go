package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"vibe-scout/templates"
)

func qual(n int, red, blue []string, played bool) Match {
	m := Match{Key: "q", MatchNumber: n, CompLevel: "qm"}
	m.Alliances.Red.TeamKeys = red
	m.Alliances.Blue.TeamKeys = blue
	if played {
		m.ActualTime = 1
	}
	return m
}

func TestNextQualFor(t *testing.T) {
	us := "frc6238"
	matches := []Match{
		qual(9, []string{us, "frc1", "frc2"}, []string{"frc3", "frc4", "frc5"}, false),
		qual(2, []string{us, "frc1", "frc2"}, []string{"frc3", "frc4", "frc5"}, true),
		qual(5, []string{"frc7", "frc8", "frc9"}, []string{us, "frc4", "frc5"}, false),
		qual(4, []string{"frc7", "frc8", "frc9"}, []string{"frc3", "frc4", "frc5"}, false),
	}
	m, ok := nextQualFor(matches, "6238")
	if !ok || m.MatchNumber != 5 {
		t.Fatalf("want match 5 (lowest unplayed with us, not 2 which is played or 4 which lacks us), got %d ok=%v", m.MatchNumber, ok)
	}
	if _, ok := nextQualFor(matches[1:2], "6238"); ok {
		t.Error("a schedule with only played matches should have no next match")
	}
}

func TestNextMatchSectionRenders(t *testing.T) {
	var buf bytes.Buffer
	err := templates.NextMatchSection(templates.NextMatchData{
		EventKey:  "2026test",
		Found:     true,
		MatchNum:  5,
		Label:     "Q5",
		Plan:      templates.MatchPlanCard{OurAlliance: "Blue", Strategy: "- do the thing"},
		Partners:  []string{"4", "5"},
		Opponents: []string{"7", "8", "9"},
	}).Render(context.Background(), &buf)
	if err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"Next Match: Q5", "do the thing", "team_number=4", "team_number=9", "section=next-"} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered section missing %q", want)
		}
	}
	if n := strings.Count(out, "/api/analyze-team"); n != 5 {
		t.Errorf("want 5 team slots (2 partners + 3 opponents), got %d", n)
	}
}
