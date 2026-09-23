package main

import "testing"

func TestDemoEventIsConsistent(t *testing.T) {
	inMatch := map[string]bool{}
	for _, m := range testMatches {
		for _, k := range append(append([]string{}, m.Alliances.Red.TeamKeys...), m.Alliances.Blue.TeamKeys...) {
			inMatch[m.Key+"/"+k[3:]] = true
			if _, ok := demoEPA[k[3:]]; !ok {
				t.Errorf("team %s in %s has no demo EPA", k[3:], m.Key)
			}
		}
	}
	// Every observation must be for a team that was actually in that match.
	for _, o := range testObservations {
		key := testEventKey + "_qm" + itoa(o.matchNum) + "/" + o.team
		if !inMatch[key] {
			t.Errorf("observation for team %s in match %d, but they weren't in it", o.team, o.matchNum)
		}
	}
	for key := range testChecklists {
		found := false
		for _, o := range testObservations {
			if itoa(o.matchNum)+"/"+o.team == key {
				found = true
			}
		}
		if !found {
			t.Errorf("checklist %s has no matching observation", key)
		}
	}
}

func TestDemoNextMatchIsOurs(t *testing.T) {
	m, ok := nextQualFor(testMatches, "6238")
	if !ok || m.MatchNumber != 10 {
		t.Fatalf("want 6238's next match to be Q10, got %d ok=%v", m.MatchNumber, ok)
	}
	p, ok := demoMatchPrediction(m.Key)
	if !ok {
		t.Fatal("no demo prediction for the next match")
	}
	// We're red in Q10; the demo is meant to show a close match.
	if got := outlook(int(p.RedWinProb*100 + 0.5)); got != "Close match" {
		t.Errorf("Q10 should read as a close match, got %q (%.0f%%)", got, p.RedWinProb*100)
	}
}

func TestDemoHasLowDataAndSleeper(t *testing.T) {
	counts := map[string]int{}
	for _, o := range testObservations {
		counts[o.team]++
	}
	if counts["1011"] >= minMatchesForVerdict || counts["1010"] >= minMatchesForVerdict {
		t.Error("1010 and 1011 should have fewer than 3 scouted matches to show the low-data state")
	}
	// The sleeper has top-tier EPA, so the notes-vs-EPA warning can trigger.
	if demoRankings()["1005"] > 3 {
		t.Errorf("1005 should rank in the top 3 by demo EPA, got %d", demoRankings()["1005"])
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	s := ""
	for n > 0 {
		s = string(rune('0'+n%10)) + s
		n /= 10
	}
	return s
}
