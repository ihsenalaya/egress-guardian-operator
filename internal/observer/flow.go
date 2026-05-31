package observer

import "time"

// Flow is the normalised representation of a single egress network flow.
type Flow struct {
	SourceNamespace string
	SourcePod       string
	SourceWorkload  string // resolved via owner refs: e.g. "Deployment/payment-api"
	DestFQDN        string // empty when DNS L7 visibility is not available
	DestIP          string
	DestPort        uint32
	Protocol        string // TCP | UDP
	Verdict         string // FORWARDED | DROPPED | AUDIT
	Bytes           uint64 // 0 when unavailable
	Timestamp       time.Time
}
