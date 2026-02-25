package templates

type TeamVariability struct {
	Team         int     `json:"team"`
	RawVariation float64 `json:"raw_variation"`
	Normalized   float64 `json:"normalized_variation"`
	Rank         int     `json:"rank"`
	RankingScore float64 `json:"ranking_score"`
}

type CategoryAnalysis struct {
	Category      string            `json:"category"`
	Variabilities []TeamVariability `json:"variabilities"`
	Stability     float64           `json:"stability"`
}

type AnalysisPageData struct {
	Events           map[string]string
	SelectedEvent    string
	Categories       []string
	CategoryAnalyses []CategoryAnalysis
	StabilityScore   float64
	Teams            []int
	XAxis            string
	YAxis            string
	EPATeams         []TeamEPA
}

type TeamEPA struct {
	Team       string  `json:"team"`
	EPA        float64 `json:"epa"`
	DefenseEPA float64 `json:"defense_epa"`
	FoulEPA    float64 `json:"foul_epa"`
}

type EPAPageData struct {
	Events        map[string]string
	SelectedEvent string
	Teams         []TeamEPA
}
