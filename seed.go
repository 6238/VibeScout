package main

const testEventKey = "2026test"
const testEventName = "Test Event 2026"

// testMatches is the fake qualification schedule for the test event.
var testMatches = []Match{
	{Key: "2026test_qm1", MatchNumber: 1, CompLevel: "qm", Alliances: struct {
		Red  Alliance `json:"red"`
		Blue Alliance `json:"blue"`
	}{
		Red:  Alliance{TeamKeys: []string{"frc1001", "frc1002", "frc1003"}},
		Blue: Alliance{TeamKeys: []string{"frc1004", "frc1005", "frc1006"}},
	}},
	{Key: "2026test_qm2", MatchNumber: 2, CompLevel: "qm", Alliances: struct {
		Red  Alliance `json:"red"`
		Blue Alliance `json:"blue"`
	}{
		Red:  Alliance{TeamKeys: []string{"frc1007", "frc1008", "frc1009"}},
		Blue: Alliance{TeamKeys: []string{"frc1002", "frc1005", "frc1008"}},
	}},
	{Key: "2026test_qm3", MatchNumber: 3, CompLevel: "qm", Alliances: struct {
		Red  Alliance `json:"red"`
		Blue Alliance `json:"blue"`
	}{
		Red:  Alliance{TeamKeys: []string{"frc1001", "frc1006", "frc1009"}},
		Blue: Alliance{TeamKeys: []string{"frc1003", "frc1007", "frc1008"}},
	}},
	{Key: "2026test_qm4", MatchNumber: 4, CompLevel: "qm", Alliances: struct {
		Red  Alliance `json:"red"`
		Blue Alliance `json:"blue"`
	}{
		Red:  Alliance{TeamKeys: []string{"frc1004", "frc1006", "frc1008"}},
		Blue: Alliance{TeamKeys: []string{"frc1001", "frc1005", "frc1007"}},
	}},
	{Key: "2026test_qm5", MatchNumber: 5, CompLevel: "qm", Alliances: struct {
		Red  Alliance `json:"red"`
		Blue Alliance `json:"blue"`
	}{
		Red:  Alliance{TeamKeys: []string{"frc1002", "frc1007", "frc1009"}},
		Blue: Alliance{TeamKeys: []string{"frc1003", "frc1004", "frc1006"}},
	}},
	{Key: "2026test_qm6", MatchNumber: 6, CompLevel: "qm", Alliances: struct {
		Red  Alliance `json:"red"`
		Blue Alliance `json:"blue"`
	}{
		Red:  Alliance{TeamKeys: []string{"frc1001", "frc1003", "frc1008"}},
		Blue: Alliance{TeamKeys: []string{"frc1002", "frc1006", "frc1009"}},
	}},
}

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

	// Team 1002 — inconsistent, high ceiling
	{1, "1002", "Amazing auto (4 notes!) but broke down in teleop for 2 minutes. Came back and scored 2 more."},
	{2, "1002", "Played very well today — consistent cycles, 3 notes auto, 4 teleop, L1 climb. Big improvement."},
	{5, "1002", "Stalled out completely in match 3. Sat still for last 60 seconds. Mechanical issue suspected."},
	{6, "1002", "Back to good form — 3 notes auto, fast cycles. Skipped climb to score more. Good decision."},

	// Team 1003 — defense specialist
	{1, "1003", "Pure defense this match. Shadowed 1004 the entire teleop. Very effective, opponents barely scored."},
	{3, "1003", "Attempted offense — scored 2 notes in auto but switched to defense in teleop. Decent blocker."},
	{5, "1003", "Aggressive defense, got two yellow-card warnings. Effective but risky. Scoring ability minimal."},
	{6, "1003", "Mostly defense again. Did score 1 note in auto. Their defense game is legitimately elite."},

	// Team 1004 — strong scorer, good climb
	{1, "1004", "3 notes auto, steady teleop, L2 climb. Very smooth intake, no dropped notes. Solid robot."},
	{4, "1004", "4 notes teleop despite defense, L3 climb. Handled contact well. High priority pick."},
	{5, "1004", "2 notes auto, 3 teleop. Slower today — possibly battery issue? Still finished with L2 climb."},

	// Team 1005 — slow but reliable
	{1, "1005", "Only 1 note in auto, 2 teleop. Slow but never broke down or dropped a note. Very steady."},
	{2, "1005", "Consistent again — 1 note auto, 3 teleop, L1 climb. Won't wow you but always finishes."},
	{4, "1005", "Reliable as always. 2 notes auto, 2 teleop. Tried L2 climb and made it! Progress."},

	// Team 1006 — great auto, weak teleop
	{1, "1006", "Incredible auto — 4 notes in 15 seconds! Teleop was very slow though, only 1 note. Strange gap."},
	{3, "1006", "Same pattern: dominant auto (3 notes), then kind of wandered in teleop. 2 notes total teleop."},
	{4, "1006", "Auto was again best on field (4 notes). Teleop scored 2. Their auto alone is worth picking for."},
	{5, "1006", "4 note auto again. Teleop better this time — 3 notes. Maybe they found a fix. L1 climb."},

	// Team 1007 — rookie, improving
	{2, "1007", "First comp for this team. Missed all auto notes. Scored 1 in teleop. Navigating field well."},
	{3, "1007", "Much better! 1 note auto, 2 teleop. Robot looks mechanically sound, just needs driver practice."},
	{4, "1007", "Scored 2 notes auto for first time! 2 teleop also. Rapidly improving match over match."},
	{5, "1007", "Consistent 2+2 performance. Tried defense in last 30 seconds, not very effective yet."},

	// Team 1008 — defense specialist (different style from 1003)
	{2, "1008", "Physical defense, knocked opponents off notes twice. Got 1 note in auto themselves. Useful."},
	{3, "1008", "Set up a wall in front of opponent intake. Very disruptive. Refs watching them closely."},
	{4, "1008", "Mixed strategy — 2 notes auto then switched to defense. Their 2-in-1 capability is solid."},
	{6, "1008", "Defense only match. Shadowed top scorer all game. Highly effective. No penalties this time."},

	// Team 1009 — high scorer, somewhat unreliable
	{2, "1009", "WOW — 5 notes in teleop, L3 climb. Best performance of the event so far. Robot looks great."},
	{3, "1009", "Stalled out in auto (brownout?). Recovered for 3 notes teleop, L2 climb. Still impressive."},
	{5, "1009", "4 notes teleop, fast cycles. Dropped the L3 climb attempt and settled for L2. Still strong."},
	{6, "1009", "E-stopped at the 30 second mark — connection issue. Had already scored 3 notes. Unreliable."},
}

func seedTestData() {
	// Clear existing test event data
	db.Exec("DELETE FROM scout_submissions WHERE event_key = ?", testEventKey)
	db.Exec("DELETE FROM analysis_cache WHERE event_key = ?", testEventKey)
	db.Exec("DELETE FROM match_plan_cache WHERE event_key = ?", testEventKey)

	// Insert fake observations (scouter_id 1 for all)
	for _, obs := range testObservations {
		db.Exec(`
			INSERT INTO scout_submissions (event_key, match_num, scouter_id, team_number, notes)
			VALUES (?, ?, 1, ?, ?)`,
			testEventKey, obs.matchNum, obs.team, obs.notes)
	}
}
