package main

import (
	"bytes"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// Simple in-memory cache for analysis results
type analysisCacheEntry struct {
	Summary AnalysisSummary
	Time    time.Time
}

var analysisCache = struct {
	mu   sync.Mutex
	data map[string]analysisCacheEntry
}{data: make(map[string]analysisCacheEntry)}

func callAnalysisAPI(comps []Comparison) (*AnalysisResponse, error) {
	payload := AnalysisRequest{Comparisons: comps}
	jsonData, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	resp, err := http.Post(
		"https://www.pairwisetool.com/api/analyze_comparisons",
		"application/json",
		bytes.NewBuffer(jsonData),
	)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var result AnalysisResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, err
	}
	return &result, nil
}
func buildAnalysisSummary(result *AnalysisResponse) AnalysisSummary {
	u := result.Svd.U
	s := result.Svd.S
	numTeams := len(u)

	raw := computeRawVariation(u, s)
	normalized := normalize(raw)

	variabilities := make([]TeamVariability, numTeams)
	for i, r := range result.Rankings {
		variabilities[i] = TeamVariability{
			Team:         r.Team,
			Rank:         r.Rank,
			RankingScore: r.Score,
			RawVariation: raw[i],
			Normalized:   normalized[i],
		}
	}

	stability := computeStabilityMetric(result.Svd.S, result.Stats.MatrixRank)

	return AnalysisSummary{
		Variabilities: variabilities,
		Stability:     stability,
		Stats:         result.Stats,
	}
}
func computeRawVariation(u [][]float64, s []float64) []float64 {
	numTeams := len(u)
	raw := make([]float64, numTeams)

	for i := 0; i < numTeams; i++ {
		sum := 0.0
		for j := 1; j < len(s); j++ {
			val := u[i][j] * s[j]
			sum += val * val
		}
		raw[i] = math.Sqrt(sum)
	}
	return raw
}
func normalize(vals []float64) []float64 {
	if len(vals) == 0 {
		return nil
	}
	minV, maxV := vals[0], vals[0]
	for _, v := range vals[1:] {
		if v < minV {
			minV = v
		}
		if v > maxV {
			maxV = v
		}
	}

	out := make([]float64, len(vals))
	if maxV == minV {
		return out
	}
	denom := maxV - minV
	for i, v := range vals {
		out[i] = (v - minV) / denom
	}
	return out
}
func computeStabilityMetric(singularValues []float64, matrixRank int) float64 {
	if matrixRank <= 0 || matrixRank > len(singularValues) {
		return math.NaN()
	}
	return singularValues[0] / singularValues[matrixRank-1]
}

// Retrieve all pairwise comparisons for a given event and category from the DB
func getComparisonsForEvent(eventKey, category string) ([]Comparison, error) {
	rows, err := db.Query("SELECT team_a, team_b, difference FROM pairwise_scouting WHERE event_key = ? AND category = ?", eventKey, category)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var comps []Comparison
	for rows.Next() {
		var taStr, tbStr string
		var diff int
		if err := rows.Scan(&taStr, &tbStr, &diff); err != nil {
			return nil, err
		}
		ta, err := strconv.Atoi(taStr)
		if err != nil {
			return nil, err
		}
		tb, err := strconv.Atoi(tbStr)
		if err != nil {
			return nil, err
		}
		comps = append(comps, Comparison{TeamA: ta, TeamB: tb, Diff: diff})
	}
	return comps, nil
}

// Analyze an event for a specific category end-to-end, with caching
func analyzeEventCategory(eventKey, category string) (*AnalysisSummary, error) {
	cacheKey := eventKey + "-" + category

	analysisCache.mu.Lock()
	if ent, ok := analysisCache.data[cacheKey]; ok {
		if time.Since(ent.Time) < time.Hour {
			summary := ent.Summary
			analysisCache.mu.Unlock()
			return &summary, nil
		}
		delete(analysisCache.data, cacheKey)
	}
	analysisCache.mu.Unlock()

	comps, err := getComparisonsForEvent(eventKey, category)
	if err != nil {
		return nil, err
	}
	resp, err := callAnalysisAPI(comps)
	if err != nil {
		return nil, err
	}
	summary := buildAnalysisSummary(resp)
	analysisCache.mu.Lock()
	analysisCache.data[cacheKey] = analysisCacheEntry{Summary: summary, Time: time.Now()}
	analysisCache.mu.Unlock()
	return &summary, nil
}
