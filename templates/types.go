package templates

// OurTeam is the team this scouting app belongs to.
const OurTeam = "6238"

type GeminiAnalysisPageData struct {
	EventKey  string
	EventName string
}

type TeamAnalysisCard struct {
	EventKey    string
	TeamNumber  string
	Summary     string
	Scoring     int // 1-10
	Reliability int // 1-10
	Defense     int // 0 = N/A, 1-10 = score
	FromCache   bool
	HasNotes    bool // any non-empty match scouting notes at this event
	HasPitNotes bool // any pit scouting summaries
}

type TeamNote struct {
	MatchNum    int
	Notes       string
	ScouterName string // "" for notes saved before scouters had names
	AIGenerated bool   // filled in by Gemini from match video
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
	Mode        string   // "all" (6 robots) or "one" (pick a robot)
}

type PickTeam struct {
	Number     string
	Alliance   string // "Red" or "Blue"
	IsUs       bool
	NextWithUs int  // next qual after this one shared with our team, 0 if none
	Soonest    bool // NextWithUs is the soonest among this match's teams
}

type ScoutTeam struct {
	Number    string
	Alliance  string // "Red" or "Blue"
	DataCount int
}
