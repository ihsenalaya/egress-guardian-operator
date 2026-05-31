package baseline

import (
	"github.com/ihsen/egress-guardian-operator/internal/observer"
)

// ScoredEntry pairs a DestEntry with its computed score and risk level.
type ScoredEntry struct {
	Entry     *observer.DestEntry
	Score     int
	RiskLevel string
}

// Build scores all destinations in a snapshot and returns a sorted slice.
// Entries are ordered: Low risk first, then Medium, then High.
func Build(snap *observer.Snapshot) []ScoredEntry {
	result := make([]ScoredEntry, 0, len(snap.Destinations))
	for _, e := range snap.Destinations {
		s := Score(e)
		result = append(result, ScoredEntry{
			Entry:     e,
			Score:     s,
			RiskLevel: RiskLevel(e, s),
		})
	}
	// Stable sort: Low → Medium → High
	sortByRisk(result)
	return result
}

// RiskSummary counts entries by risk level.
func RiskSummaryFrom(entries []ScoredEntry) (low, medium, high int) {
	for _, e := range entries {
		switch e.RiskLevel {
		case "Low":
			low++
		case "Medium":
			medium++
		case "High":
			high++
		}
	}
	return
}

func sortByRisk(entries []ScoredEntry) {
	order := map[string]int{"Low": 0, "Medium": 1, "High": 2}
	n := len(entries)
	for i := 1; i < n; i++ {
		for j := i; j > 0 && order[entries[j].RiskLevel] < order[entries[j-1].RiskLevel]; j-- {
			entries[j], entries[j-1] = entries[j-1], entries[j]
		}
	}
}
