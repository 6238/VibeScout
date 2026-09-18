package templates

import "fmt"

// pitMapPos is a team's exact drawn box on one of the two Chezy Champs venue
// maps, as a percentage of that image's width/height (so it scales with the
// image). Measured directly from the black box borders in each image, not
// estimated, so hotspots match the real pit boundaries cell by cell.
type pitMapPos struct {
	Image               string
	LeftPct, TopPct     float64
	WidthPct, HeightPct float64
}

// Hand-measured from static/pit-map1.webp (Pit Map) and static/pit-map2.webp
// (Arena Map, which also lists a handful of overflow pits along its hallway).
// This is specific to the 2026 Chezy Champs venue layout.
var pitMapPositions = map[string]pitMapPos{
	"359":  {"pit-map1.webp", 24.297, 14.475, 4.453, 6.876},
	"5507": {"pit-map1.webp", 42.813, 14.475, 4.531, 6.876},
	"5940": {"pit-map1.webp", 52.109, 14.475, 4.453, 6.876},
	"9496": {"pit-map1.webp", 56.719, 14.475, 4.531, 6.876},
	"687":  {"pit-map1.webp", 24.297, 21.471, 4.453, 7.118},
	"2046": {"pit-map1.webp", 28.906, 21.471, 4.531, 7.118},
	"2073": {"pit-map1.webp", 38.203, 21.471, 4.453, 7.118},
	"5199": {"pit-map1.webp", 42.813, 21.471, 4.531, 7.118},
	"6017": {"pit-map1.webp", 52.109, 21.471, 4.453, 7.118},
	"9470": {"pit-map1.webp", 56.719, 21.471, 4.531, 7.118},
	"694":  {"pit-map1.webp", 24.297, 28.830, 4.453, 7.118},
	"1868": {"pit-map1.webp", 28.906, 28.830, 4.531, 7.118},
	"2813": {"pit-map1.webp", 38.203, 28.830, 4.453, 7.118},
	"5026": {"pit-map1.webp", 42.813, 28.830, 4.531, 7.118},
	"6036": {"pit-map1.webp", 52.109, 28.830, 4.453, 7.118},
	"9128": {"pit-map1.webp", 56.719, 28.830, 4.531, 7.118},
	"846":  {"pit-map1.webp", 24.297, 36.067, 4.453, 7.118},
	"1678": {"pit-map1.webp", 28.906, 36.067, 4.531, 7.118},
	"2910": {"pit-map1.webp", 38.203, 36.067, 4.453, 7.118},
	"4698": {"pit-map1.webp", 42.813, 36.067, 4.531, 7.118},
	"6238": {"pit-map1.webp", 52.109, 36.067, 4.453, 7.118},
	"9032": {"pit-map1.webp", 56.719, 36.067, 4.531, 7.118},
	"971":  {"pit-map1.webp", 24.297, 43.305, 4.453, 7.118},
	"1540": {"pit-map1.webp", 28.906, 43.305, 4.531, 7.118},
	"3045": {"pit-map1.webp", 38.203, 43.305, 4.453, 7.118},
	"4499": {"pit-map1.webp", 42.813, 43.305, 4.531, 7.118},
	"6647": {"pit-map1.webp", 52.109, 43.305, 4.453, 7.118},
	"9023": {"pit-map1.webp", 56.719, 43.305, 4.531, 7.118},
	"972":  {"pit-map1.webp", 24.141, 50.663, 4.688, 6.876},
	"973":  {"pit-map1.webp", 28.828, 50.663, 4.766, 6.876},
	"3256": {"pit-map1.webp", 38.203, 50.784, 4.453, 6.996},
	"4270": {"pit-map1.webp", 42.813, 50.784, 4.531, 6.996},
	"6665": {"pit-map1.webp", 51.953, 50.663, 4.609, 6.996},
	"8229": {"pit-map1.webp", 56.563, 50.663, 4.844, 6.996},

	"254":  {"pit-map2.webp", 20.547, 84.139, 3.438, 5.576},
	"581":  {"pit-map2.webp", 24.063, 84.139, 3.438, 5.576},
	"604":  {"pit-map2.webp", 28.984, 84.139, 3.516, 5.576},
	"841":  {"pit-map2.webp", 36.094, 84.139, 3.438, 5.576},
	"3847": {"pit-map2.webp", 41.406, 84.139, 3.438, 5.576},
	"4414": {"pit-map2.webp", 44.922, 84.139, 3.516, 5.576},
	"6800": {"pit-map2.webp", 50.781, 84.139, 3.438, 5.576},
	"9408": {"pit-map2.webp", 54.297, 84.139, 3.438, 5.576},
}

// pitMapImageAspect is height/width, used to size each image's container
// before it loads so hotspots don't jump around.
var pitMapImageAspect = map[string]float64{
	"pit-map1.webp": 829.0 / 1280.0,
	"pit-map2.webp": 807.0 / 1280.0,
}

func hotspotStyle(pos pitMapPos) string {
	return fmt.Sprintf("left: %.3f%%; top: %.3f%%; width: %.3f%%; height: %.3f%%;", pos.LeftPct, pos.TopPct, pos.WidthPct, pos.HeightPct)
}

type pitMapTeam struct {
	PitTeam
	Pos pitMapPos
}

func mapTeamsFor(teams []PitTeam, image string) []pitMapTeam {
	var out []pitMapTeam
	for _, t := range teams {
		if pos, ok := pitMapPositions[t.Number]; ok && pos.Image == image {
			out = append(out, pitMapTeam{PitTeam: t, Pos: pos})
		}
	}
	return out
}

func unmappedPitTeams(teams []PitTeam) []PitTeam {
	var out []PitTeam
	for _, t := range teams {
		if _, ok := pitMapPositions[t.Number]; !ok {
			out = append(out, t)
		}
	}
	return out
}
