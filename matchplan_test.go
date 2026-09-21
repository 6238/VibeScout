package main

import (
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
