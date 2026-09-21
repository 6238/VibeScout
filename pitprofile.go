package main

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"
	"text/template"

	"vibe-scout/templates"
)

//go:embed prompts/pit_profile.prompt
var pitProfilePromptTmpl string

// pitProfilePromptVersion is mixed into the cache key so edits to the prompt
// invalidate previously extracted profiles.
const pitProfilePromptVersion = "v1"

var (
	pitArchetypes = map[string]bool{"Scorer": true, "Defender": true, "Feeder": true, "All-Around": true}
	pitRoles      = map[string]bool{"Offense": true, "Defense": true, "Feeding": true, "Flexible": true}
)

const pitProfileMaxLen = 120

// parsePitProfile reads the model's JSON into a profile, dropping anything that
// isn't one of the allowed labels and capping free text, so a bad answer shows
// as blank instead of as junk on the card.
func parsePitProfile(raw string) (templates.PitProfile, error) {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimPrefix(raw, "```json")
	raw = strings.TrimPrefix(raw, "```")
	raw = strings.TrimSuffix(raw, "```")
	raw = strings.TrimSpace(raw)

	var in struct {
		Archetype  string `json:"archetype"`
		Role       string `json:"role"`
		Partner    string `json:"partner"`
		Strength   string `json:"strength"`
		Problem    string `json:"problem"`
		Turnaround string `json:"turnaround"`
		Safety     string `json:"safety"`
	}
	if err := json.Unmarshal([]byte(raw), &in); err != nil {
		return templates.PitProfile{}, fmt.Errorf("failed to parse pit profile JSON: %v — raw: %s", err, raw)
	}

	clean := func(s string) string {
		s = strings.TrimSpace(s)
		if r := []rune(s); len(r) > pitProfileMaxLen {
			s = string(r[:pitProfileMaxLen]) + "…"
		}
		return s
	}
	p := templates.PitProfile{
		Partner:    clean(in.Partner),
		Strength:   clean(in.Strength),
		Problem:    clean(in.Problem),
		Turnaround: clean(in.Turnaround),
		Safety:     clean(in.Safety),
	}
	if a := strings.TrimSpace(in.Archetype); pitArchetypes[a] {
		p.Archetype = a
	}
	if r := strings.TrimSpace(in.Role); pitRoles[r] {
		p.Role = r
	}
	p.Has = p.Archetype != "" || p.Role != "" || p.Partner != "" || p.Strength != "" ||
		p.Problem != "" || p.Turnaround != "" || p.Safety != ""
	return p, nil
}

// getPitProfile returns the team's structured pit info: archetype, role and the
// few interview answers that say something about how the robot plays. It only
// re-asks the model when the pit notes change. A team that hasn't been pit
// scouted has no profile.
func getPitProfile(teamNum string) (templates.PitProfile, error) {
	pitNotes := pitNotesFor(teamNum)
	if pitNotes == "" {
		return templates.PitProfile{}, nil
	}
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(pitProfilePromptVersion+"\n"+pitNotes)))

	var cachedJSON, cachedHash string
	err := db.QueryRow(`SELECT profile, notes_hash FROM pit_profile_cache WHERE team_number = ?`, teamNum).
		Scan(&cachedJSON, &cachedHash)
	if err == nil && cachedHash == hash {
		if p, perr := parsePitProfile(cachedJSON); perr == nil {
			return p, nil
		}
		// fall through and regenerate
	}

	tmpl, err := template.New("pit_profile").Parse(pitProfilePromptTmpl)
	if err != nil {
		return templates.PitProfile{}, fmt.Errorf("failed to parse pit profile prompt: %w", err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, struct{ TeamNum, PitNotes string }{teamNum, pitNotes}); err != nil {
		return templates.PitProfile{}, fmt.Errorf("failed to render pit profile prompt: %w", err)
	}
	raw, err := geminiPost(buf.String())
	if err != nil {
		return templates.PitProfile{}, err
	}
	p, err := parsePitProfile(raw)
	if err != nil {
		return templates.PitProfile{}, err
	}

	// Cache the cleaned profile, not the raw model text.
	stored, _ := json.Marshal(map[string]string{
		"archetype": p.Archetype, "role": p.Role, "partner": p.Partner, "strength": p.Strength,
		"problem": p.Problem, "turnaround": p.Turnaround, "safety": p.Safety,
	})
	db.Exec(`
		INSERT INTO pit_profile_cache (team_number, profile, notes_hash) VALUES (?, ?, ?)
		ON CONFLICT(team_number) DO UPDATE SET
			profile = excluded.profile,
			notes_hash = excluded.notes_hash,
			created_at = CURRENT_TIMESTAMP`,
		teamNum, string(stored), hash)
	return p, nil
}
