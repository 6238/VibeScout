package templates

type GeminiAnalysisPageData struct {
	Events        map[string]string
	SelectedEvent string
}

type TeamAnalysisCard struct {
	EventKey    string
	TeamNumber  string
	Summary     string
	Scoring     int    // 1-10
	Reliability int    // 1-10
	Defense     int    // 0 = N/A, 1-10 = score
	FromCache   bool
}

type TeamNote struct {
	MatchNum int
	Notes    string
}

type MatchPlannerPageData struct {
	Events map[string]string
}

type MatchPlanCard struct {
	MatchNum    int
	OurAlliance string // "Red" or "Blue"
	RedTeams    []string
	BlueTeams   []string
	Strategy    string
	FromCache   bool
}
