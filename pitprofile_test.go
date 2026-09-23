package main

import (
	"context"
	"bytes"
	"strings"
	"testing"

	"vibe-scout/templates"
)

func TestParsePitProfile(t *testing.T) {
	p, err := parsePitProfile("```json\n" + `{"archetype":"Scorer","role":"Offense","partner":"a defender","strength":"fast cycles","problem":"","turnaround":"yes, 3","safety":""}` + "\n```")
	if err != nil {
		t.Fatal(err)
	}
	if !p.Has || p.Archetype != "Scorer" || p.Role != "Offense" || p.Strength != "fast cycles" || p.Turnaround != "yes, 3" {
		t.Errorf("unexpected profile: %+v", p)
	}
}

func TestParsePitProfileRejectsUnknownLabels(t *testing.T) {
	// The model can drift from the allowed labels; those must show as blank.
	p, err := parsePitProfile(`{"archetype":"Super Robot","role":"whatever","partner":"","strength":"","problem":"","turnaround":"","safety":""}`)
	if err != nil {
		t.Fatal(err)
	}
	if p.Has || p.Archetype != "" || p.Role != "" {
		t.Errorf("unknown labels should be dropped and leave an empty profile, got %+v", p)
	}
}

func TestParsePitProfileCapsLongText(t *testing.T) {
	long := strings.Repeat("x", 500)
	p, err := parsePitProfile(`{"strength":"` + long + `"}`)
	if err != nil {
		t.Fatal(err)
	}
	if n := len([]rune(p.Strength)); n > pitProfileMaxLen+1 {
		t.Errorf("strength not capped: %d runes", n)
	}
}

func TestParsePitProfileBadJSON(t *testing.T) {
	if _, err := parsePitProfile("sorry, I can't"); err == nil {
		t.Error("non-JSON should be an error")
	}
}

func TestPitProfileRowRenders(t *testing.T) {
	var buf bytes.Buffer
	card := templates.TeamAnalysisCard{
		TeamNumber: "254",
		Pit:        templates.PitProfile{Has: true, Archetype: "Defender", Role: "Defense", Strength: "hard to move"},
	}
	if err := templates.SingleTeamAnalysisCard(card).Render(context.Background(), &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"Pit (self-reported)", "Defender", "Prefers Defense", "hard to move"} {
		if !strings.Contains(out, want) {
			t.Errorf("card missing %q", want)
		}
	}
	// Blank facts must not render empty labels.
	if strings.Contains(out, "Biggest problem") {
		t.Error("empty pit fields should not render a label")
	}
}
