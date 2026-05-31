package cilium

import "strings"

// WildcardRisk returns the risk level of an FQDN wildcard pattern.
// Returns "Safe", "Medium", or "High".
func WildcardRisk(fqdn string) string {
	if fqdn == "" {
		return "Safe"
	}
	if !strings.HasPrefix(fqdn, "*.") {
		return "Safe"
	}
	// Broad TLD-level wildcards are always High
	highPatterns := []string{
		"*.com", "*.net", "*.org", "*.io", "*.co",
		"*.de", "*.fr", "*.uk", "*.ru", "*.cn",
	}
	for _, p := range highPatterns {
		if fqdn == p {
			return "High"
		}
	}
	// Cloud provider wildcards are Medium/High; never auto-include in Enforce
	mediumPatterns := []string{
		"*.amazonaws.com",
		"*.cloudfront.net",
		"*.s3.amazonaws.com",
		"*.azurewebsites.net",
		"*.blob.core.windows.net",
		"*.googleapis.com",
		"*.appspot.com",
	}
	for _, p := range mediumPatterns {
		if fqdn == p {
			return "Medium"
		}
	}
	// Generic sub-domain wildcard — Medium by default
	return "Medium"
}

// IsAllowedInEnforce returns true when the FQDN is safe to include in an
// auto-enforce policy without human review.
func IsAllowedInEnforce(fqdn string) bool {
	r := WildcardRisk(fqdn)
	return r == "Safe"
}

// IsDirectPublicIP returns true when the destination has no FQDN and the IP
// appears to be a public address (heuristic: not RFC-1918 / loopback).
func IsDirectPublicIP(fqdn, ip string) bool {
	if fqdn != "" {
		return false
	}
	if ip == "" {
		return false
	}
	// RFC-1918 + loopback prefix check (avoids net.ParseIP import at this layer)
	for _, priv := range []string{"10.", "172.16.", "172.17.", "172.18.", "172.19.",
		"172.20.", "172.21.", "172.22.", "172.23.", "172.24.", "172.25.", "172.26.",
		"172.27.", "172.28.", "172.29.", "172.30.", "172.31.",
		"192.168.", "127.", "::1"} {
		if strings.HasPrefix(ip, priv) {
			return false
		}
	}
	return true
}
