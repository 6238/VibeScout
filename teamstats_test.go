package main

import "testing"

func TestTierIndex(t *testing.T) {
	cases := []struct {
		pct  float64
		want int
	}{{1.0, 4}, {0.85, 4}, {0.7, 3}, {0.5, 2}, {0.2, 1}, {0.0, 0}}
	for _, c := range cases {
		if got := tierIndex(c.pct); got != c.want {
			t.Errorf("tierIndex(%v) = %d, want %d", c.pct, got, c.want)
		}
	}
}

func TestDisagreement(t *testing.T) {
	// The 2910 case: a top-EPA team judged "Average" by the notes.
	if got := disagreement("Average", 0.95); got == "" {
		t.Error("Average vs top-tier EPA is two steps apart and should be flagged")
	}
	if got := disagreement("Strong Pick", 0.95); got != "" {
		t.Errorf("one step apart should not be flagged, got %q", got)
	}
	if got := disagreement("Elite Pick", 0.05); got == "" {
		t.Error("Elite Pick vs bottom EPA should be flagged")
	}
	// No verdict (insufficient data) means nothing to disagree with.
	if got := disagreement("Insufficient Data", 0.95); got != "" {
		t.Errorf("Insufficient Data should never be flagged, got %q", got)
	}
}
