package templates

import "testing"

func TestStrategyLines(t *testing.T) {
	got := strategyLines("EXPECTED RESULT: We are favored.\n\nWHAT THEY WILL TRY:\n1009: Will score fast.\nTeam 1003: plain line\nDO WE NEED A COUNTER: No, because we lead.")
	want := []StrategyLine{
		{Label: "EXPECTED RESULT", Text: "We are favored.", Section: true},
		{Blank: true},
		{Label: "WHAT THEY WILL TRY", Text: "", Section: true},
		{Label: "1009", Text: "Will score fast."},
		{Text: "Team 1003: plain line"},
		{Label: "DO WE NEED A COUNTER", Text: "No, because we lead.", Section: true},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d lines, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("line %d: got %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestSplitTLDR(t *testing.T) {
	tldr, rest := SplitTLDR("TLDR: 6238 and 1005 score, 1010 feeds.\n\nEXPECTED RESULT: Close.\nOUR PLAN:")
	if tldr != "6238 and 1005 score, 1010 feeds." {
		t.Errorf("tldr = %q", tldr)
	}
	if rest != "EXPECTED RESULT: Close.\nOUR PLAN:" {
		t.Errorf("rest = %q", rest)
	}

	// No TLDR line: nothing pulled out, briefing unchanged.
	in := "EXPECTED RESULT: Close."
	if tldr, rest := SplitTLDR(in); tldr != "" || rest != in {
		t.Errorf("without a TLDR line got %q / %q", tldr, rest)
	}
}

func TestStrategyLinesLeavesSentencesAlone(t *testing.T) {
	// A sentence with a colon in the middle is not a label.
	got := strategyLines("If 1009 stalls: switch to full scoring.")
	if len(got) != 1 || got[0].Label != "" {
		t.Errorf("mid-sentence colon should not make a label: %+v", got)
	}
}
