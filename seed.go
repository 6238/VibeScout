package main

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

const testEventKey = "2026test"
const testEventName = "Test Event 2026"

// demoPitPrefix marks pit notes written by the seed so they can be found and
// removed again without touching real pit scouting.
const demoPitPrefix = "[DEMO] "

// ── Demo stats ────────────────────────────────────────────────────────────────
//
// The test event has no real Statbotics data, so it gets made-up totals. They are
// only ever read for testEventKey (see epaBreakdown), so real events are
// unaffected. 1005 is deliberately the "sleeper": its notes read as slow-but-steady
// while its EPA is near the top, which is what the notes-vs-EPA warning is for.
var demoTotalEPA = map[string]float64{
	"6238": 55,
	"1001": 64,
	"1002": 52,
	"1003": 24, // defense specialist: little scoring
	"1004": 60,
	"1005": 66, // the sleeper
	"1006": 46,
	"1007": 33,
	"1008": 30,
	"1009": 68,
	"1010": 41,
	"1011": 20,
}

// demoEPA expands each total into the breakdown shape Statbotics returns.
var demoEPA = func() map[string]map[string]float64 {
	out := make(map[string]map[string]float64, len(demoTotalEPA))
	for team, total := range demoTotalEPA {
		out[team] = map[string]float64{
			"total_points":   total,
			"auto_points":    math.Round(total*0.30*10) / 10,
			"teleop_points":  math.Round(total*0.55*10) / 10,
			"endgame_points": math.Round(total*0.15*10) / 10,
		}
	}
	return out
}()

// demoSum is an alliance's combined demo EPA, given TBA-style "frc1234" keys.
func demoSum(teamKeys []string) float64 {
	var sum float64
	for _, k := range stripFRC(teamKeys) {
		sum += demoTotalEPA[k]
	}
	return sum
}

// demoMatchPrediction forecasts a test match from the demo EPAs, so the win
// chance shown for it is consistent with the teams on the page.
func demoMatchPrediction(matchKey string) (matchPrediction, bool) {
	for _, m := range testMatches {
		if m.Key != matchKey {
			continue
		}
		red, blue := demoSum(m.Alliances.Red.TeamKeys), demoSum(m.Alliances.Blue.TeamKeys)
		return matchPrediction{
			RedWinProb: 1 / (1 + math.Exp(-(red-blue)/12)),
			RedScore:   red,
			BlueScore:  blue,
		}, true
	}
	return matchPrediction{}, false
}

// demoRankings ranks the test teams by demo EPA, best first.
func demoRankings() map[string]int {
	teams := make([]string, 0, len(demoTotalEPA))
	for t := range demoTotalEPA {
		teams = append(teams, t)
	}
	sort.Slice(teams, func(i, j int) bool { return demoTotalEPA[teams[i]] > demoTotalEPA[teams[j]] })
	ranks := make(map[string]int, len(teams))
	for i, t := range teams {
		ranks[t] = i + 1
	}
	return ranks
}

// ── Schedule ──────────────────────────────────────────────────────────────────

// demoMatch builds a qualification match. Played matches get a time and scores
// taken from the alliances' demo EPA, so the results agree with the stats.
func demoMatch(n int, red, blue []string, played bool) Match {
	m := Match{Key: fmt.Sprintf("%s_qm%d", testEventKey, n), MatchNumber: n, CompLevel: "qm"}
	m.Alliances.Red.TeamKeys = red
	m.Alliances.Blue.TeamKeys = blue
	if played {
		m.ActualTime = 1780000000 + int64(n)*600
		r, b := int(demoSum(red)+0.5), int(demoSum(blue)+0.5)
		m.Alliances.Red.Score, m.Alliances.Blue.Score = &r, &b
	}
	return m
}

// testMatches is the fake qualification schedule for the test event. Matches 1-9
// are played; 10-12 are still to come, and 10 is 6238's next match.
var testMatches = []Match{
	demoMatch(1, []string{"frc1001", "frc1002", "frc1003"}, []string{"frc1004", "frc1005", "frc1006"}, true),
	demoMatch(2, []string{"frc1007", "frc1008", "frc1009"}, []string{"frc1002", "frc1005", "frc1008"}, true),
	demoMatch(3, []string{"frc1001", "frc1006", "frc1009"}, []string{"frc1003", "frc1007", "frc1008"}, true),
	demoMatch(4, []string{"frc1004", "frc1006", "frc1008"}, []string{"frc1001", "frc1005", "frc1007"}, true),
	demoMatch(5, []string{"frc1002", "frc1007", "frc1009"}, []string{"frc1003", "frc1004", "frc1006"}, true),
	demoMatch(6, []string{"frc1001", "frc1003", "frc1008"}, []string{"frc1002", "frc1006", "frc1009"}, true),
	demoMatch(7, []string{"frc6238", "frc1004", "frc1010"}, []string{"frc1001", "frc1007", "frc1011"}, true),
	demoMatch(8, []string{"frc1002", "frc1003", "frc1011"}, []string{"frc6238", "frc1009", "frc1010"}, true),
	demoMatch(9, []string{"frc1005", "frc1006", "frc1010"}, []string{"frc1001", "frc1008", "frc1011"}, true),
	demoMatch(10, []string{"frc6238", "frc1005", "frc1010"}, []string{"frc1001", "frc1009", "frc1007"}, false),
	demoMatch(11, []string{"frc1002", "frc1004", "frc1008"}, []string{"frc6238", "frc1007", "frc1011"}, false),
	demoMatch(12, []string{"frc1006", "frc1009", "frc1003"}, []string{"frc1001", "frc1010", "frc1005"}, false),
}

// ── Scouting data ─────────────────────────────────────────────────────────────

type seedObs struct {
	matchNum int
	team     string
	notes    string
}

var testObservations = []seedObs{
	// Team 1001 — strong all-rounder
	{1, "1001", "Great auto — scored 3 notes and climbed L2. Fast intake, consistent teleop. Very reliable robot."},
	{3, "1001", "Scored 4 notes in teleop, quick cycle times. Had a brief brownout in auto but recovered fast."},
	{4, "1001", "Dominant match. Scored 5 notes, climbed L3. Defense against them barely slowed them down."},
	{6, "1001", "Another strong match. 4 notes teleop, L2 climb. One intake miss but otherwise flawless."},
	{7, "1001", "Won the match almost alone. 4 notes teleop, L3 climb. Drove through defense like it wasn't there. Stays on the left side of the field the whole match."},
	{9, "1001", "Steady again — 3 auto, 4 teleop. Partner 1011 was useless but they carried the alliance."},

	// Team 1002 — inconsistent, high ceiling
	{1, "1002", "Amazing auto (4 notes!) but broke down in teleop for 2 minutes. Came back and scored 2 more."},
	{2, "1002", "Played very well today — consistent cycles, 3 notes auto, 4 teleop, L1 climb. Big improvement."},
	{5, "1002", "Stalled out completely in match 3. Sat still for last 60 seconds. Mechanical issue suspected."},
	{6, "1002", "Back to good form — 3 notes auto, fast cycles. Skipped climb to score more. Good decision."},
	{8, "1002", "Solid match. 2 notes auto, 3 teleop. No problems this time."},

	// Team 1003 — defense specialist
	{1, "1003", "Pure defense this match. Shadowed 1004 the entire teleop. Very effective, opponents barely scored."},
	{3, "1003", "Attempted offense — scored 2 notes in auto but switched to defense in teleop. Decent blocker."},
	{5, "1003", "Aggressive defense, got two yellow-card warnings. Effective but risky. Scoring ability minimal."},
	{6, "1003", "Mostly defense again. Did score 1 note in auto. Their defense game is legitimately elite."},
	{8, "1003", "Defense on 1009 the whole match and held them to 2 notes. Very good at this."},

	// Team 1004 — strong scorer, good climb
	{1, "1004", "3 notes auto, steady teleop, L2 climb. Very smooth intake, no dropped notes. Solid robot."},
	{4, "1004", "4 notes teleop despite defense, L3 climb. Handled contact well. High priority pick."},
	{5, "1004", "2 notes auto, 3 teleop. Slower today — possibly battery issue? Still finished with L2 climb."},
	{7, "1004", "3 notes auto, 3 teleop, L2. Consistent and clean."},

	// Team 1005 — slow but reliable (its EPA is actually near the top; see demoTotalEPA)
	{1, "1005", "Only 1 note in auto, 2 teleop. Slow but never broke down or dropped a note. Very steady."},
	{2, "1005", "Consistent again — 1 note auto, 3 teleop, L1 climb. Won't wow you but always finishes."},
	{4, "1005", "Reliable as always. 2 notes auto, 2 teleop. Tried L2 climb and made it! Progress."},
	{9, "1005", "Nothing flashy. 1 auto, 2 teleop, L1. Did what it always does."},

	// Team 1006 — great auto, weak teleop
	{1, "1006", "Incredible auto — 4 notes in 15 seconds! Teleop was very slow though, only 1 note. Strange gap."},
	{3, "1006", "Same pattern: dominant auto (3 notes), then kind of wandered in teleop. 2 notes total teleop."},
	{4, "1006", "Auto was again best on field (4 notes). Teleop scored 2. Their auto alone is worth picking for."},
	{5, "1006", "4 note auto again. Teleop better this time — 3 notes. Maybe they found a fix. L1 climb."},
	{9, "1006", "4 note auto. Teleop 2 notes. Same story."},

	// Team 1007 — rookie, improving
	{2, "1007", "First comp for this team. Missed all auto notes. Scored 1 in teleop. Navigating field well."},
	{3, "1007", "Much better! 1 note auto, 2 teleop. Robot looks mechanically sound, just needs driver practice."},
	{4, "1007", "Scored 2 notes auto for first time! 2 teleop also. Rapidly improving match over match."},
	{5, "1007", "Consistent 2+2 performance. Tried defense in last 30 seconds, not very effective yet."},
	{7, "1007", "2 auto, 3 teleop. Best match yet."},

	// Team 1008 — defense specialist (different style from 1003)
	{2, "1008", "Physical defense, knocked opponents off notes twice. Got 1 note in auto themselves. Useful."},
	{3, "1008", "Set up a wall in front of opponent intake. Very disruptive. Refs watching them closely."},
	{4, "1008", "Mixed strategy — 2 notes auto then switched to defense. Their 2-in-1 capability is solid."},
	{6, "1008", "Defense only match. Shadowed top scorer all game. Highly effective. No penalties this time."},
	{9, "1008", "Played defense on 1005 most of the match. Got 1 note in auto."},

	// Team 1009 — high scorer, somewhat unreliable
	{2, "1009", "WOW — 5 notes in teleop, L3 climb. Best performance of the event so far. Robot looks great."},
	{3, "1009", "Stalled out in auto (brownout?). Recovered for 3 notes teleop, L2 climb. Still impressive."},
	{5, "1009", "4 notes teleop, fast cycles. Dropped the L3 climb attempt and settled for L2. Still strong. Runs to the right side in auto every time."},
	{6, "1009", "E-stopped at the 30 second mark — connection issue. Had already scored 3 notes. Unreliable."},
	{8, "1009", "Great scoring (4 teleop) but got shut down by 1003's defense for most of the second half."},

	// Team 1010 — only two matches scouted: shows the "low data" state
	{7, "1010", "Scored 2 notes in teleop. Quiet match, hard to say much else."},
	{9, "1010", "1 note auto, 2 teleop. Slow intake but no failures. Spent the second half passing notes to 1005."},

	// Team 1011 — a single match scouted
	{7, "1011", "Barely moved. Might have had a drivetrain problem."},

	// Team 6238 (us) — two matches scouted
	{7, "6238", "Good match. 2 notes auto, 3 teleop, L2 climb. Communication with partners was fine."},
	{8, "6238", "Lost a close one. 3 teleop notes, missed the climb. Got defended a lot by 1003."},
}

// seedChecklist is the yes/no checklist the one-robot scouting mode records.
type seedChecklist struct {
	broke, playedDefense, wasDefended bool
	autoType                          string
}

// testChecklists attaches a checklist to some of the observations above, keyed by
// "match/team". The rest stay notes-only, as older data would be.
var testChecklists = map[string]seedChecklist{
	"1/1002": {broke: true, autoType: "Scored"},
	"5/1002": {broke: true, autoType: "Scored"},
	"6/1002": {autoType: "Scored"},
	"8/1002": {autoType: "Scored"},
	"1/1003": {playedDefense: true, autoType: "None"},
	"5/1003": {playedDefense: true, autoType: "Scored"},
	"6/1003": {playedDefense: true, autoType: "Scored"},
	"8/1003": {playedDefense: true, autoType: "None"},
	"3/1009": {broke: true, autoType: "None"},
	"6/1009": {broke: true, autoType: "Scored"},
	"8/1009": {wasDefended: true, autoType: "Scored"},
	"4/1001": {wasDefended: true, autoType: "Scored"},
	"7/1001": {wasDefended: true, autoType: "Scored"},
	"9/1001": {autoType: "Scored"},
	"2/1008": {playedDefense: true, autoType: "Scored"},
	"6/1008": {playedDefense: true, autoType: "None"},
	"9/1008": {playedDefense: true, autoType: "Scored"},
	"7/6238": {autoType: "Scored"},
	"8/6238": {wasDefended: true, autoType: "Scored"},
}

// testPitNotes are demo pit interviews, written the way scouts write them. Some
// teams deliberately have none, so both states show up on the cards.
var testPitNotes = map[string]string{
	"1001": "1. All-around, but mostly a scorer. 2. Offense. 3. Wants a defender partner so they can score freely. 5. Driver is careful, never hits partners. 6. Intake jams sometimes if a note is misaligned. 7. Yes, 3 in a row on battery swaps. 12. Fast cycle times. 13. Proud of the climber. 15. Laminated checklist. 17. 2 captains, 5 mentors.",
	"1003": "1. Defender, no question. 2. Defense. 3. A strong scorer who doesn't want to play defense. 4. If they get countered they switch to feeding. 5. Driver is aggressive, might bump you, has 2 yellow cards this year. 6. Robot is heavy and slow to turn. 7. Yes, 2 in a row. 12. Best drive team at blocking. 14. Robot is fully in CAD.",
	"1005": "1. Scorer, low volume. 2. Offense. 3. Anyone reliable. 5. Very safe driver. 6. Nothing major, cycle time is their weakness. 7. Yes, 3 in a row. 12. Never breaks. 17. 1 captain, 3 mentors.",
	"1009": "1. Scorer. 2. Offense only. 3. A defender to protect them. 5. Fast driver, been in a couple of collisions. 6. Electrical connection issues, they've lost comms twice. 7. 2 in a row is fine, 3 is risky. 12. Highest scoring ceiling of any team here. 13. Proud of the shooter.",
}

func seedTestData() {
	// Clear existing test event data
	db.Exec("DELETE FROM scout_submissions WHERE event_key = ?", testEventKey)
	db.Exec("DELETE FROM analysis_cache WHERE event_key = ?", testEventKey)
	db.Exec("DELETE FROM match_plan_cache WHERE event_key = ?", testEventKey)
	clearDemoPitNotes()

	// Insert fake observations
	for _, obs := range testObservations {
		db.Exec(`
			INSERT INTO scout_submissions (event_key, match_num, scouter_id, scouter_name, team_number, notes)
			VALUES (?, ?, 1, 'Demo Scout', ?, ?)`,
			testEventKey, obs.matchNum, obs.team, obs.notes)
	}
	for key, c := range testChecklists {
		var match int
		var team string
		fmt.Sscanf(strings.Replace(key, "/", " ", 1), "%d %s", &match, &team)
		db.Exec(`
			UPDATE scout_submissions
			SET has_checklist = 1, broke = ?, played_defense = ?, was_defended = ?, auto_type = ?
			WHERE event_key = ? AND match_num = ? AND team_number = ?`,
			c.broke, c.playedDefense, c.wasDefended, c.autoType, testEventKey, match, team)
	}

	// Pit notes aren't tied to an event, so never add one to a team that already
	// has real notes: that would mix demo text into real pit scouting.
	for team, summary := range testPitNotes {
		db.Exec(`
			INSERT INTO pit_scouting (team_number, summary)
			SELECT ?, ? WHERE NOT EXISTS (SELECT 1 FROM pit_scouting WHERE team_number = ?)`,
			team, demoPitPrefix+summary, team)
	}
}

// clearDemoPitNotes removes the pit notes the seed added, leaving real ones alone.
func clearDemoPitNotes() {
	db.Exec("DELETE FROM pit_scouting WHERE summary LIKE ?", demoPitPrefix+"%")
}
