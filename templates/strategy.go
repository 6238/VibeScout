package templates

import (
	"regexp"
	"strings"
)

// StrategyLine is one line of the match briefing. Label is the part before the
// colon when the line starts with a section label ("EXPECTED RESULT") or a team
// number ("6238"), so the page can pick it out.
type StrategyLine struct {
	Label   string
	Text    string
	Blank   bool
	Section bool // an all-caps section label rather than a team number
}

// SplitTLDR pulls the "TLDR:" line out of the briefing. It returns the summary
// and the rest of the briefing without that line. If there is no TLDR line, the
// summary is empty and the briefing comes back unchanged.
func SplitTLDR(s string) (tldr, rest string) {
	lines := strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	for i, line := range lines {
		t := strings.TrimSpace(line)
		upper := strings.ToUpper(t)
		for _, prefix := range []string{"TLDR:", "TL;DR:"} {
			if strings.HasPrefix(upper, prefix) {
				tldr = strings.TrimSpace(t[len(prefix):])
				remaining := append(append([]string{}, lines[:i]...), lines[i+1:]...)
				return tldr, strings.TrimSpace(strings.Join(remaining, "\n"))
			}
		}
	}
	return "", s
}

var strategyLabel =regexp.MustCompile(`^([A-Z0-9][A-Z0-9 ,'/&-]{1,40}):\s*(.*)$`)

// strategyLines splits the briefing into lines, marking the section labels and
// team numbers at the start of a line. Anything else is shown as plain text.
func strategyLines(s string) []StrategyLine {
	var out []StrategyLine
	for _, raw := range strings.Split(strings.TrimSpace(strings.ReplaceAll(s, "\r\n", "\n")), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			out = append(out, StrategyLine{Blank: true})
			continue
		}
		if m := strategyLabel.FindStringSubmatch(line); m != nil {
			isTeam := strings.Trim(m[1], "0123456789") == ""
			out = append(out, StrategyLine{Label: m[1], Text: m[2], Section: !isTeam})
			continue
		}
		out = append(out, StrategyLine{Text: line})
	}
	return out
}
