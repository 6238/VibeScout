package main

import (
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"

	"vibe-scout/templates"
)

// pitProfileText is a pit profile as one line for the match plan prompt, or ""
// when there's nothing to say.
func pitProfileText(p templates.PitProfile) string {
	if !p.Has {
		return ""
	}
	var parts []string
	add := func(label, v string) {
		if v != "" {
			parts = append(parts, label+": "+v)
		}
	}
	add("archetype", p.Archetype)
	add("prefers", p.Role)
	add("strength", p.Strength)
	add("biggest problem", p.Problem)
	add("wants partner", p.Partner)
	add("back-to-back", p.Turnaround)
	add("driver safety", p.Safety)
	return strings.Join(parts, "; ")
}

// pitProfiles fetches the pit profiles for several teams at once. The extraction
// is cached, so this is only slow the first time a team's pit notes change.
func pitProfiles(teams []string) map[string]templates.PitProfile {
	out := make(map[string]templates.PitProfile, len(teams))
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, t := range teams {
		wg.Add(1)
		go func(t string) {
			defer wg.Done()
			p, err := getPitProfile(t)
			if err != nil {
				log.Printf("pit profile for %s: %v", t, err)
				return
			}
			mu.Lock()
			out[t] = p
			mu.Unlock()
		}(t)
	}
	wg.Wait()
	return out
}

// lineup describes an alliance's teams from highest EPA down, so the model can
// see who the main scorer on each side is.
func lineup(eventKey, label string, teams []string) string {
	type entry struct {
		team string
		epa  float64
		ok   bool
	}
	entries := make([]entry, 0, len(teams))
	for _, t := range teams {
		epa, ok := teamTotalEPA(eventKey, t)
		entries = append(entries, entry{t, epa, ok})
	}
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].epa > entries[j].epa })

	parts := make([]string, 0, len(entries))
	var total float64
	allKnown := true
	for _, e := range entries {
		if e.ok {
			parts = append(parts, fmt.Sprintf("%s (EPA %.0f)", e.team, e.epa))
			total += e.epa
		} else {
			parts = append(parts, e.team+" (EPA unknown)")
			allKnown = false
		}
	}
	text := label + " by EPA, highest first: " + strings.Join(parts, ", ")
	if allKnown {
		text += fmt.Sprintf(". Combined EPA: %.0f", total)
	}
	return text
}

// defenseRecord says which robots on our alliance have shown they can play
// defense, so the plan can't hand defense to a robot with no record of it. The
// evidence is the yes/no checklist (played defense in at least one match) and the
// pit interview (defender archetype or preferred defense role).
func defenseRecord(eventKey string, ours []string, profiles map[string]templates.PitProfile) string {
	proven := defenseProof(eventKey, ours, profiles)
	if len(proven) == 0 {
		return "Robots on our alliance with a defense record: NONE. No robot on our alliance may be given defense anywhere in the briefing."
	}
	return "Robots on our alliance with a defense record: " + strings.Join(proven, ", ") + ". Only these robots may be given defense."
}

// opponentDefense is the same check for the other alliance: which of them are
// known to play defense, and so might go after us.
func opponentDefense(eventKey string, theirs []string, profiles map[string]templates.PitProfile) string {
	proven := defenseProof(eventKey, theirs, profiles)
	if len(proven) == 0 {
		return "Opponents with a defense record: NONE."
	}
	return "Opponents with a defense record: " + strings.Join(proven, ", ") + "."
}

// defenseProof lists each robot in teams that has shown it can play defense,
// with the evidence: a played-defense checklist or a defender pit interview.
func defenseProof(eventKey string, teams []string, profiles map[string]templates.PitProfile) []string {
	var proven []string
	for _, t := range teams {
		var reasons []string
		if s := scoutStatsFor(eventKey, t); s.DefenseN > 0 {
			reasons = append(reasons, fmt.Sprintf("played defense in %d of %d checklist matches", s.DefenseN, s.ChecklistN))
		}
		if p := profiles[t]; p.Archetype == "Defender" || p.Role == "Defense" {
			reasons = append(reasons, "pit interview says defender")
		}
		if len(reasons) > 0 {
			proven = append(proven, fmt.Sprintf("%s (%s)", t, strings.Join(reasons, "; ")))
		}
	}
	return proven
}

// matchPlanContext gathers what the model knows about all six teams, including
// our own robot, so its plan for us rests on our data and not on a guess.
func matchPlanContext(eventKey, ourTeam string, ours, theirs []string) (context, lineups string) {
	all := append(append([]string{}, ours...), theirs...)
	profiles := pitProfiles(all)

	describe := func(team, role string) string {
		notes := "none"
		if n, err := combineTeamNotes(eventKey, team); err == nil && n != "" {
			notes = n
		}
		pit := pitProfileText(profiles[team])
		if pit == "" {
			pit = "none"
		}
		return fmt.Sprintf("Team %s [%s]\n  EPA (Statbotics):\n%s\n  Scouted: %s\n  Pit interview (self-reported): %s\n  Match notes: %s",
			team, role, fetchStatboticsEPA(eventKey, team), scoutStatsFor(eventKey, team).promptText(), pit, notes)
	}

	var parts []string
	parts = append(parts, describe(ourTeam, "US"))
	for _, t := range ours {
		if t != ourTeam {
			parts = append(parts, describe(t, "PARTNER"))
		}
	}
	for _, t := range theirs {
		parts = append(parts, describe(t, "OPPONENT"))
	}

	lineups = lineup(eventKey, "Our alliance", ours) + "\n" + lineup(eventKey, "Opponent alliance", theirs) +
		"\n" + defenseRecord(eventKey, ours, profiles) +
		"\n" + opponentDefense(eventKey, theirs, profiles)
	return strings.Join(parts, "\n\n"), lineups
}

// forecastText is the Statbotics forecast for a match from our side, as a line
// for the prompt, or "" when there isn't one.
func forecastText(m Match, weAreRed bool) string {
	p, ok := statboticsMatchPrediction(m.Key)
	if !ok {
		return ""
	}
	win, ours, theirs := p.RedWinProb, p.RedScore, p.BlueScore
	if !weAreRed {
		win, ours, theirs = 1-p.RedWinProb, p.BlueScore, p.RedScore
	}
	return fmt.Sprintf("Forecast: we have a %.0f%% chance to win (predicted score: us %.0f, them %.0f).",
		win*100, ours, theirs)
}
