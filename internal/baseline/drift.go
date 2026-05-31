package baseline

import (
	"fmt"

	"github.com/ihsen/egress-guardian-operator/internal/observer"
)

// DriftResult describes differences between a current snapshot and a previous one.
type DriftResult struct {
	NewDestinations     []string
	RemovedDestinations []string
}

// Detect compares the current snapshot to a previous one and returns new/removed entries.
func Detect(previous, current *observer.Snapshot) DriftResult {
	prev := indexSnapshot(previous)
	curr := indexSnapshot(current)

	var result DriftResult
	for k := range curr {
		if _, ok := prev[k]; !ok {
			result.NewDestinations = append(result.NewDestinations, k)
		}
	}
	for k := range prev {
		if _, ok := curr[k]; !ok {
			result.RemovedDestinations = append(result.RemovedDestinations, k)
		}
	}
	return result
}

func indexSnapshot(snap *observer.Snapshot) map[string]struct{} {
	if snap == nil {
		return map[string]struct{}{}
	}
	m := make(map[string]struct{}, len(snap.Destinations))
	for _, d := range snap.Destinations {
		m[destID(d)] = struct{}{}
	}
	return m
}

func destID(d *observer.DestEntry) string {
	host := d.DestFQDN
	if host == "" {
		host = d.DestIP
	}
	return fmt.Sprintf("%s:%d/%s", host, d.DestPort, d.Protocol)
}
