package templates

// OurTeam is the team this scouting app belongs to.
const OurTeam = "6238"

type GeminiAnalysisPageData struct {
	EventKey  string
	EventName string
}

type TeamAnalysisCard struct {
	EventKey       string
	TeamNumber     string
	Verdict        string // one of: Elite Pick, Strong Pick, Average, Below Average, Avoid
	Shooting       string
	Driving        string
	Failures       string
	Auto           string
	Recommendation string
	Scoring        int // 1-10
	Reliability    int // 1-10
	Defense        int // 0 = N/A, 1-10 = score
	Error          string // set instead of the fields above when analysis generation failed
	FromCache      bool
	HasNotes    bool    // any non-empty match scouting notes at this event
	HasPitNotes bool    // any pit scouting summaries
	EPA         float64 // Statbotics total points EPA, current season
	HasEPA      bool
	Rank        int // current event qualification rank
	HasRank     bool
	// Up to the two most recent played matches at the event, newest first
	RecentMatches []MatchLink
}

type MatchLink struct {
	Label string // e.g. "Q12"
	URL   string
}

type TeamNote struct {
	MatchNum    int
	Notes       string
	ScouterName string // "" for notes saved before scouters had names
	AIGenerated bool   // filled in by Gemini from match video
	// Match checklist; only meaningful when HasChecklist
	HasChecklist  bool
	Broke         bool
	PlayedDefense bool
	WasDefended   bool
	AutoType      string
}

type PitTeam struct {
	Number  string
	Scouted bool // has at least one pit scouting summary
}

type PitNote struct {
	CreatedAt string
	Summary   string
}

type MatchPlannerPageData struct {
	EventKey  string
	EventName string
}

type MatchPlanCard struct {
	MatchNum    int
	OurAlliance string // "Red" or "Blue"
	RedTeams    []string
	BlueTeams   []string
	Strategy    string
	FromCache   bool
}

type AiFillSlot struct {
	Team  string
	HXURL string
}

type AiFillTeamResultData struct {
	Team    string
	Notes   string
	Success bool
	Skipped bool // team already had data
}

type FieldScoutData struct {
	EventKey    string
	EventName   string
	MatchNum    int
	ScouterName string   // prefilled when returning from a match
	Scouters    []string // past scouter names for the type-ahead
	Mode        string   // "three" (pick an alliance) or "one" (pick a robot)
}

type PickTeam struct {
	Number     string
	Alliance   string // "Red" or "Blue"
	IsUs       bool
	NextWithUs int  // next qual after this one shared with our team, 0 if none
	Soonest    bool // NextWithUs is the soonest among this match's teams
	// Coverage: scouted in fewer than half of the quals played before this match
	Scouted      int
	PlayedBefore int
	NeedsData    bool
}

type ScoutPageConfig struct {
	Teams    []ScoutTeam `json:"teams"`
	OneRobot bool        `json:"one_robot"`
}

type ScoutTeam struct {
	Number    string `json:"number"`
	Alliance  string `json:"alliance"` // "Red" or "Blue"
	DataCount int    `json:"data_count"`
}
