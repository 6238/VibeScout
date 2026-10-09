package main

import (
	"database/sql"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/a-h/templ"

	"vibe-scout/templates"
)

// ── Data layer ───────────────────────────────────────────────────────────

// videoScoutNotesFor reads a team's video-review notes for an event. Returns
// a zero-value VideoScoutNotes (Empty() == true) if none exist yet.
func videoScoutNotesFor(eventKey, teamNum string) (templates.VideoScoutNotes, error) {
	var n templates.VideoScoutNotes
	err := db.QueryRow(`
		SELECT shooting, driving, auto, failures FROM video_scouting
		WHERE event_key = ? AND team_number = ?`, eventKey, teamNum).
		Scan(&n.Shooting, &n.Driving, &n.Auto, &n.Failures)
	if err == sql.ErrNoRows {
		return templates.VideoScoutNotes{}, nil
	}
	return n, err
}

// saveVideoScoutNotes upserts a team's video-review notes for an event.
func saveVideoScoutNotes(eventKey, teamNum string, n templates.VideoScoutNotes) error {
	_, err := db.Exec(`
		INSERT INTO video_scouting (event_key, team_number, shooting, driving, auto, failures)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(event_key, team_number) DO UPDATE SET
			shooting = excluded.shooting,
			driving = excluded.driving,
			auto = excluded.auto,
			failures = excluded.failures,
			updated_at = CURRENT_TIMESTAMP`,
		eventKey, teamNum,
		strings.TrimSpace(n.Shooting), strings.TrimSpace(n.Driving),
		strings.TrimSpace(n.Auto), strings.TrimSpace(n.Failures))
	return err
}

// videoScoutMatchNotesFor returns a team's per-match quick notes from video
// review, keyed by qualification match number.
func videoScoutMatchNotesFor(eventKey, teamNum string) (map[int]string, error) {
	rows, err := db.Query(`
		SELECT match_num, note FROM video_scouting_match_notes
		WHERE event_key = ? AND team_number = ?`, eventKey, teamNum)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[int]string{}
	for rows.Next() {
		var m int
		var note string
		if err := rows.Scan(&m, &note); err != nil {
			return nil, err
		}
		out[m] = note
	}
	return out, nil
}

// saveVideoScoutMatchNote upserts one match's quick note. An empty note
// deletes the row instead — clearing the box removes the tag rather than
// leaving a blank one behind.
func saveVideoScoutMatchNote(eventKey, teamNum string, matchNum int, note string) error {
	note = strings.TrimSpace(note)
	if note == "" {
		_, err := db.Exec(`DELETE FROM video_scouting_match_notes WHERE event_key = ? AND team_number = ? AND match_num = ?`,
			eventKey, teamNum, matchNum)
		return err
	}
	_, err := db.Exec(`
		INSERT INTO video_scouting_match_notes (event_key, team_number, match_num, note)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(event_key, team_number, match_num) DO UPDATE SET
			note = excluded.note,
			updated_at = CURRENT_TIMESTAMP`,
		eventKey, teamNum, matchNum, note)
	return err
}

// videoScoutBlock formats a team's video-review notes as the text Gemini
// reads: the four categories, then every per-match quick note explicitly
// numbered, so it can be cross-referenced against the same match numbers in
// the live field-scouting notes (see combineTeamNotes). Returns "" when
// nothing has been recorded, so callers can skip adding an empty section.
func videoScoutBlock(eventKey, teamNum string) string {
	notes, err := videoScoutNotesFor(eventKey, teamNum)
	if err != nil {
		log.Printf("video scout notes for %s at %s: %v", teamNum, eventKey, err)
		return ""
	}
	matchNotes, err := videoScoutMatchNotesFor(eventKey, teamNum)
	if err != nil {
		log.Printf("video scout match notes for %s at %s: %v", teamNum, eventKey, err)
		matchNotes = nil
	}
	if notes.Empty() && len(matchNotes) == 0 {
		return ""
	}

	var b strings.Builder
	field := func(label, value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		fmt.Fprintf(&b, "%s: %s\n", label, value)
	}
	field("Shooting", notes.Shooting)
	field("Driving", notes.Driving)
	field("Auto", notes.Auto)
	field("Failures", notes.Failures)

	if len(matchNotes) > 0 {
		nums := make([]int, 0, len(matchNotes))
		for m := range matchNotes {
			nums = append(nums, m)
		}
		sort.Ints(nums)
		b.WriteString("Per-match notes from video review:\n")
		for _, m := range nums {
			fmt.Fprintf(&b, "- Match %d: %s\n", m, matchNotes[m])
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// ── HTTP handlers ────────────────────────────────────────────────────────

func videoScoutPageHandler(w http.ResponseWriter, r *http.Request) {
	eventKey, ok := requireEvent(w, r)
	if !ok {
		return
	}
	templ.Handler(templates.VideoScoutPage(eventKey, eventNameFor(eventKey))).ServeHTTP(w, r)
}

// apiVideoScoutTeamsHandler renders the event's team list for the video
// scouting picker, marking teams that already have a video review started.
func apiVideoScoutTeamsHandler(w http.ResponseWriter, r *http.Request) {
	eventKey := r.URL.Query().Get("event_key")
	if eventKey == "" {
		http.Error(w, "event_key required", http.StatusBadRequest)
		return
	}

	teams, err := getEventTeamsCached(eventKey)
	if err != nil {
		log.Printf("video scout teams %s: %v", eventKey, err)
		templates.PitTeamList(nil, "Couldn't load teams for this event.").Render(r.Context(), w)
		return
	}

	reviewed := map[string]bool{}
	rows, err := db.Query(`SELECT DISTINCT team_number FROM video_scouting WHERE event_key = ?`, eventKey)
	if err == nil {
		for rows.Next() {
			var t string
			rows.Scan(&t)
			reviewed[t] = true
		}
		rows.Close()
	}

	list := make([]templates.PitTeam, len(teams))
	for i, t := range teams {
		list[i] = templates.PitTeam{Number: t, Scouted: reviewed[t]}
	}
	templates.VideoScoutTeamList(eventKey, list, "").Render(r.Context(), w)
}

// videoScoutReviewPageHandler is the main review screen for one team: every
// qualification match it played, a link to go watch each one, and the
// structured notes. Qualification matches only — a set/match-number pair
// (as playoffs use) isn't representable by the match_num-only schema above,
// and quals are the overwhelming majority of what there is to review anyway.
func videoScoutReviewPageHandler(w http.ResponseWriter, r *http.Request) {
	eventKey, ok := requireEvent(w, r)
	if !ok {
		return
	}
	teamNum := strings.TrimSpace(r.URL.Query().Get("team_number"))
	if teamNum == "" {
		http.Redirect(w, r, "/video-scout?event_key="+url.QueryEscape(eventKey), http.StatusSeeOther)
		return
	}

	data := templates.VideoScoutReviewData{
		EventKey:   eventKey,
		EventName:  eventNameFor(eventKey),
		TeamNumber: teamNum,
	}

	notes, err := videoScoutNotesFor(eventKey, teamNum)
	if err != nil {
		log.Printf("video scout notes for %s at %s: %v", teamNum, eventKey, err)
	}
	data.Notes = notes

	matches, err := getMatchesCached(eventKey)
	if err != nil {
		log.Printf("video scout matches for %s: %v", eventKey, err)
	}
	quickNotes, err := videoScoutMatchNotesFor(eventKey, teamNum)
	if err != nil {
		log.Printf("video scout match notes for %s at %s: %v", teamNum, eventKey, err)
	}

	key := "frc" + teamNum
	for _, m := range matches {
		if m.CompLevel != "qm" {
			continue
		}
		onRed := slices.Contains(m.Alliances.Red.TeamKeys, key)
		onBlue := slices.Contains(m.Alliances.Blue.TeamKeys, key)
		if !onRed && !onBlue {
			continue
		}

		row := templates.VideoScoutMatchRow{
			MatchNum: m.MatchNumber,
			Label:    m.ShortLabel(),
		}
		own, opp := m.Alliances.Red, m.Alliances.Blue
		if !onRed {
			own, opp = m.Alliances.Blue, m.Alliances.Red
		}
		var partners []string
		for _, t := range stripFRC(own.TeamKeys) {
			if t != teamNum {
				partners = append(partners, t)
			}
		}
		row.Partners = strings.Join(partners, ", ")
		row.Opponents = strings.Join(stripFRC(opp.TeamKeys), ", ")
		row.WatchURL, row.HasWatch = matchReviewURL(eventKey, m)

		if m.Played() && m.Alliances.Red.Score != nil && m.Alliances.Blue.Score != nil {
			redScore, blueScore := *m.Alliances.Red.Score, *m.Alliances.Blue.Score
			ourScore, theirScore := redScore, blueScore
			if !onRed {
				ourScore, theirScore = blueScore, redScore
			}
			result := "Tied"
			if ourScore > theirScore {
				result = "Won"
			} else if ourScore < theirScore {
				result = "Lost"
			}
			row.ScoreText = fmt.Sprintf("%s %d–%d", result, ourScore, theirScore)
		}

		if note, ok := quickNotes[m.MatchNumber]; ok {
			row.Note = note
			row.HasNote = true
		}
		data.Matches = append(data.Matches, row)
	}
	sort.Slice(data.Matches, func(i, j int) bool { return data.Matches[i].MatchNum < data.Matches[j].MatchNum })

	templates.VideoScoutReviewPage(data).Render(r.Context(), w)
}

// apiSaveVideoScoutHandler saves a team's four structured video-review
// categories. Invalidates that team's cached analysis the same way every
// other note source does: the cache key is a hash of the exact text Gemini
// reads (see getOrGenerateAnalysis), and this text is now part of it.
func apiSaveVideoScoutHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Only POST allowed", http.StatusMethodNotAllowed)
		return
	}
	eventKey := r.FormValue("event_key")
	teamNum := r.FormValue("team_number")
	if eventKey == "" || teamNum == "" {
		http.Error(w, "event_key and team_number are required", http.StatusBadRequest)
		return
	}

	notes := templates.VideoScoutNotes{
		Shooting: r.FormValue("shooting"),
		Driving:  r.FormValue("driving"),
		Auto:     r.FormValue("auto"),
		Failures: r.FormValue("failures"),
	}
	if err := saveVideoScoutNotes(eventKey, teamNum, notes); err != nil {
		http.Error(w, "db error", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// apiSaveVideoScoutMatchNoteHandler saves (or, if blank, clears) one match's
// quick note.
func apiSaveVideoScoutMatchNoteHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Only POST allowed", http.StatusMethodNotAllowed)
		return
	}
	eventKey := r.FormValue("event_key")
	teamNum := r.FormValue("team_number")
	matchNum, err := strconv.Atoi(r.FormValue("match_num"))
	if eventKey == "" || teamNum == "" || err != nil {
		http.Error(w, "event_key, team_number, and a valid match_num are required", http.StatusBadRequest)
		return
	}
	if err := saveVideoScoutMatchNote(eventKey, teamNum, matchNum, r.FormValue("note")); err != nil {
		http.Error(w, "db error", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}
