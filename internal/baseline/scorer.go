package baseline

import (
	"net"
	"strings"

	"github.com/ihsen/egress-guardian-operator/internal/observer"
)

// Score computes a 0-100 confidence score for a destination entry.
// Higher = safer to include in an enforce policy.
// The score is advisory; hard rules in wildcard.go block enforcement independently.
func Score(e *observer.DestEntry) int {
	score := 50

	// Stability: seen across multiple snapshot cycles
	if e.Snapshots >= 3 {
		score += 25
	}

	// Volume: high flow count indicates regular usage
	if e.FlowCount >= 10 {
		score += 15
	}

	// Standard port
	if isStandardPort(e.DestPort) {
		score += 10
	}

	// No wildcard in FQDN
	if e.DestFQDN != "" && !strings.Contains(e.DestFQDN, "*") {
		score += 10
	}

	// Seen only once — penalise
	if e.Snapshots == 1 && e.FlowCount == 1 {
		score -= 15
	}

	// Direct public IP without FQDN
	if e.DestFQDN == "" && isPublicIP(e.DestIP) {
		score -= 30
	}

	// Wildcard large
	if isWildcardLarge(e.DestFQDN) {
		score -= 30
	}

	if score < 0 {
		score = 0
	}
	if score > 100 {
		score = 100
	}
	return score
}

// RiskLevel returns Low/Medium/High based on score and hard rules.
func RiskLevel(e *observer.DestEntry, score int) string {
	if e.DestFQDN == "" && isPublicIP(e.DestIP) {
		return "High"
	}
	if isWildcardLarge(e.DestFQDN) {
		return "High"
	}
	if score >= 80 {
		return "Low"
	}
	if score >= 50 {
		return "Medium"
	}
	return "High"
}

// AggregateScore returns the mean confidence score across all destinations.
func AggregateScore(entries []*observer.DestEntry) int {
	if len(entries) == 0 {
		return 0
	}
	total := 0
	for _, e := range entries {
		total += Score(e)
	}
	return total / len(entries)
}

func isStandardPort(port uint32) bool {
	switch port {
	case 443, 80, 8080, 8443, 5432, 3306, 6379, 27017:
		return true
	}
	return false
}

func isPublicIP(ip string) bool {
	if ip == "" {
		return false
	}
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return false
	}
	return !parsed.IsLoopback() &&
		!parsed.IsPrivate() &&
		!parsed.IsLinkLocalUnicast() &&
		!parsed.IsLinkLocalMulticast()
}

func isWildcardLarge(fqdn string) bool {
	if fqdn == "" {
		return false
	}
	large := []string{"*.com", "*.net", "*.org", "*.io", "*.amazonaws.com", "*.cloudfront.net"}
	for _, pattern := range large {
		if fqdn == pattern {
			return true
		}
	}
	return false
}
