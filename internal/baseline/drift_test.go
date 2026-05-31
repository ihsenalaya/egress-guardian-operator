package baseline

import (
	"testing"
	"time"

	"github.com/ihsen/egress-guardian-operator/internal/observer"
)

func snap(dests ...*observer.DestEntry) *observer.Snapshot {
	return &observer.Snapshot{
		WorkloadKey:  "payment/Deployment/payment-api",
		Destinations: dests,
		GeneratedAt:  time.Now(),
	}
}

func dest(fqdn, ip string, port uint32, proto string) *observer.DestEntry {
	return &observer.DestEntry{
		DestFQDN: fqdn, DestIP: ip, DestPort: port, Protocol: proto,
		FirstSeen: time.Now(), LastSeen: time.Now(), FlowCount: 1,
	}
}

func TestDetect_NewDestination(t *testing.T) {
	prev := snap(dest("api.stripe.com", "1.1.1.1", 443, "TCP"))
	curr := snap(
		dest("api.stripe.com", "1.1.1.1", 443, "TCP"),
		dest("new.service.com", "2.2.2.2", 443, "TCP"),
	)
	result := Detect(prev, curr)
	if len(result.NewDestinations) != 1 {
		t.Errorf("expected 1 new destination, got %d", len(result.NewDestinations))
	}
}

func TestDetect_RemovedDestination(t *testing.T) {
	prev := snap(
		dest("api.stripe.com", "1.1.1.1", 443, "TCP"),
		dest("old.service.com", "2.2.2.2", 443, "TCP"),
	)
	curr := snap(dest("api.stripe.com", "1.1.1.1", 443, "TCP"))
	result := Detect(prev, curr)
	if len(result.RemovedDestinations) != 1 {
		t.Errorf("expected 1 removed destination, got %d", len(result.RemovedDestinations))
	}
}

func TestDetect_NilPrevious(t *testing.T) {
	curr := snap(dest("api.stripe.com", "1.1.1.1", 443, "TCP"))
	result := Detect(nil, curr)
	if len(result.NewDestinations) != 1 {
		t.Errorf("expected 1 new destination when previous is nil, got %d", len(result.NewDestinations))
	}
}
