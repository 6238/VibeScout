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

func TestParseMatchPrediction(t *testing.T) {
	p, ok := parseMatchPrediction([]byte(`{"pred":{"red_win_prob":0.72,"red_score":81.5,"blue_score":66.2}}`))
	if !ok || p.RedWinProb != 0.72 || p.RedScore != 81.5 || p.BlueScore != 66.2 {
		t.Fatalf("got %+v ok=%v", p, ok)
	}
	// A missing field or an unexpected shape must not become a 0% forecast.
	for _, bad := range []string{`{}`, `{"pred":{}}`, `{"pred":{"red_win_prob":0.5}}`, `{"pred":{"red_win_prob":7,"red_score":1,"blue_score":1}}`, `nope`} {
		if _, ok := parseMatchPrediction([]byte(bad)); ok {
			t.Errorf("%s should not parse", bad)
		}
	}
}

func TestOutlook(t *testing.T) {
	cases := map[int]string{95: "Easy win", 80: "Easy win", 65: "Favored", 55: "Close match", 50: "Close match", 41: "Close match", 40: "Underdog", 25: "Underdog", 10: "Tough match"}
	for pct, want := range cases {
		if got := outlook(pct); got != want {
			t.Errorf("outlook(%d) = %q, want %q", pct, got, want)
		}
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

		HasPrediction: true, WinPct: 72, Outlook: "Favored", OurScore: 81.4, TheirScore: 66.2,
	}).Render(context.Background(), &buf)
	if err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"Next Match: Q5", "do the thing", "team_number=4", "team_number=9", "section=next-", "72%", "Favored", "81 – 66"} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered section missing %q", want)
		}
	}
	if n := strings.Count(out, "/api/analyze-team"); n != 5 {
		t.Errorf("want 5 team slots (2 partners + 3 opponents), got %d", n)
	}
}
