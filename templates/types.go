package templates

// OurTeam is the team this scouting app belongs to.
const OurTeam = "6238"

type GeminiAnalysisPageData struct {
	EventKey  string
	EventName string
}

// NextMatchData is the "next match" section at the top of the analysis page.
type NextMatchData struct {
	EventKey  string
	Found     bool // our team has an unplayed qual match
	Label     string
	MatchNum  int
	Plan      MatchPlanCard
	PlanError string
	Partners  []string // our alliance, excluding us
	Opponents []string

	// Statbotics' forecast, from our alliance's side. Unset when Statbotics
	// has no prediction, in which case the section leaves it out.
	HasPrediction bool
	WinPct        int // our chance to win, 0-100
	Outlook       string
	OurScore      float64
	TheirScore    float64
}

// PitProfile is the handful of pit interview answers that say something about
// how a robot plays. They are the team's own claims, so the card shows them
// separately from what our scouts saw in matches.
type PitProfile struct {
	Has        bool // at least one field is filled in
	Archetype  string
	Role       string
	Partner    string // partner archetype that complements them
	Strength   string
	Problem    string // biggest robot problem
	Turnaround string // back-to-back match capability
	Safety     string // driver safety
}

type TeamAnalysisCard struct {
	Pit            PitProfile
	Section        string // id prefix for cards shown in more than one place on the page
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

	// What the verdict rests on. Computed from the database, not by the AI.
	Matches         int    // distinct matches scouted at this event
	LowData         bool   // too few matches for the AI verdict to mean much
	ChecklistN      int    // scouted matches that recorded the yes/no checklist
	BrokeN          int    // of those, matches where the robot broke
	DefenseN        int    // of those, matches where it played defense
	WasDefendedN    int    // of those, matches where it was defended
	HasEPAPct       bool   // EPA percentile among this event's teams is known
	EPATopPct       int    // 1 = best EPA at the event, 50 = median, 100 = worst
	Disagreement    string // set when the notes-based verdict and EPA differ a lot
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
