package main

import (
	"fmt"
	"sort"
	"sync"
	"time"

	"vibe-scout/templates"
)

// minMatchesForVerdict is how many scouted matches a team needs before the
// AI's notes-based verdict is shown. Below it, one bad or quiet match can swing
// the read, so the card says "Low data" instead of a grade.
const minMatchesForVerdict = 3

// scoutStats counts what the scouts recorded for one team at one event. These
// numbers come straight from the database so the card can show exactly what the
// AI's verdict is based on.
type scoutStats struct {
	Matches      int // distinct matches with notes or a checklist
	ChecklistN   int // submissions that recorded the yes/no checklist
	BrokeN       int
	DefenseN     int
	WasDefendedN int
}

func scoutStatsFor(eventKey, teamNum string) scoutStats {
	var s scoutStats
	err := db.QueryRow(`
		SELECT COUNT(DISTINCT match_num),
			COALESCE(SUM(COALESCE(has_checklist, 0)), 0),
			COALESCE(SUM(CASE WHEN COALESCE(has_checklist, 0) = 1 THEN COALESCE(broke, 0) ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN COALESCE(has_checklist, 0) = 1 THEN COALESCE(played_defense, 0) ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN COALESCE(has_checklist, 0) = 1 THEN COALESCE(was_defended, 0) ELSE 0 END), 0)
		FROM scout_submissions
		WHERE event_key = ? AND team_number = ? AND `+hasScoutDataSQL,
		eventKey, teamNum).Scan(&s.Matches, &s.ChecklistN, &s.BrokeN, &s.DefenseN, &s.WasDefendedN)
	if err != nil {
		return scoutStats{}
	}
	return s
}

// promptText is the plain-language version of the stats given to the AI, so it
// judges reliability by rate over a known sample instead of by one anecdote.
func (s scoutStats) promptText() string {
	text := fmt.Sprintf("%d match(es) scouted.", s.Matches)
	if s.ChecklistN > 0 {
		text += fmt.Sprintf(" Of the %d that recorded the checklist: broke in %d, played defense in %d, was defended in %d.",
			s.ChecklistN, s.BrokeN, s.DefenseN, s.WasDefendedN)
	}
	return text
}

// ── EPA percentile among the event's teams ────────────────────────────────────

var (
	epaPctCache = map[string]map[string]float64{}
	epaPctAt    = map[string]time.Time{}
	epaPctMu    sync.Mutex
)

// eventEPAPercentiles returns each team's EPA percentile at the event: 1.0 is
// the highest EPA, 0.0 the lowest. Statbotics is measured over every match a
// team has played, so it anchors the pick list where scouting notes are thin.
// It returns nil when too few teams have EPA to rank against.
func eventEPAPercentiles(eventKey string) map[string]float64 {
	// One computation at a time: the analysis page loads every card at once and
	// they would all fetch the same numbers.
	epaPctMu.Lock()
	defer epaPctMu.Unlock()

	if p, ok := epaPctCache[eventKey]; ok && time.Since(epaPctAt[eventKey]) < 10*time.Minute {
		return p
	}

	teams, err := getEventTeamsCached(eventKey)
	if err != nil || len(teams) == 0 {
		return nil
	}

	type result struct {
		team string
		epa  float64
	}
	jobs := make(chan string)
	results := make(chan result)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for t := range jobs {
				if epa, ok := teamTotalEPA(t); ok {
					results <- result{t, epa}
				}
			}
		}()
	}
	go func() {
		for _, t := range teams {
			jobs <- t
		}
		close(jobs)
		wg.Wait()
		close(results)
	}()

	var ranked []result
	for r := range results {
		ranked = append(ranked, r)
	}

	var pcts map[string]float64
	if len(ranked) >= 8 {
		sort.Slice(ranked, func(i, j int) bool { return ranked[i].epa < ranked[j].epa })
		pcts = make(map[string]float64, len(ranked))
		for i, r := range ranked {
			pcts[r.team] = float64(i) / float64(len(ranked)-1)
		}
	}
	epaPctCache[eventKey] = pcts
	epaPctAt[eventKey] = time.Now()
	return pcts
}

// tierIndex buckets a percentile into the same five steps as the AI's verdict
// (4 = Elite Pick ... 0 = Avoid) so the two can be compared.
func tierIndex(pct float64) int {
	switch {
	case pct >= 0.85:
		return 4
	case pct >= 0.60:
		return 3
	case pct >= 0.35:
		return 2
	case pct >= 0.15:
		return 1
	}
	return 0
}

func verdictIndex(verdict string) int {
	switch verdict {
	case "Elite Pick":
		return 4
	case "Strong Pick":
		return 3
	case "Average":
		return 2
	case "Below Average":
		return 1
	case "Avoid":
		return 0
	}
	return -1
}

// disagreement returns a warning when the notes-based verdict is two or more
// steps away from where EPA puts the team, or "" when they roughly agree.
func disagreement(verdict string, pct float64) string {
	v, t := verdictIndex(verdict), tierIndex(pct)
	if v < 0 {
		return ""
	}
	switch {
	case t-v >= 2:
		return "The notes rate this team much lower than its EPA does. Read the notes before trusting the verdict."
	case v-t >= 2:
		return "The notes rate this team much higher than its EPA does. Check whether that's earned."
	}
	return ""
}

// addTrustInfo fills in everything on a card that comes from data rather than
// from the AI: how many matches it rests on, the checklist tallies, the EPA
// standing and whether the two views of the team disagree. A verdict built on
// too few matches is withheld.
func addTrustInfo(card *templates.TeamAnalysisCard) {
	s := scoutStatsFor(card.EventKey, card.TeamNumber)
	card.Matches = s.Matches
	card.ChecklistN = s.ChecklistN
	card.BrokeN = s.BrokeN
	card.DefenseN = s.DefenseN
	card.WasDefendedN = s.WasDefendedN

	if pcts := eventEPAPercentiles(card.EventKey); pcts != nil {
		if pct, ok := pcts[card.TeamNumber]; ok {
			card.HasEPAPct = true
			card.EPATopPct = int((1-pct)*99+0.5) + 1
			card.Disagreement = disagreement(card.Verdict, pct)
		}
	}

	if card.Error == "" && s.Matches < minMatchesForVerdict {
		card.LowData = true
		card.Verdict = ""
		card.Disagreement = ""
	}
}
