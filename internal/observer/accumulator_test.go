package observer

import (
	"context"
	"testing"
	"time"

	"github.com/go-logr/logr"
)

// noopStore is a SnapshotStore that discards all writes (for unit tests).
type noopStore struct{}

func (n *noopStore) SaveSnapshot(_ context.Context, _, _ string, _ *Snapshot) error { return nil }

func TestAccumulator_Ingest(t *testing.T) {
	acc := NewAccumulator(nil, &noopStore{}, 24*time.Hour, time.Minute,
		logr.Discard())

	f := Flow{
		SourceNamespace: "payment",
		SourcePod:       "payment-api-abc",
		SourceWorkload:  "Deployment/payment-api",
		DestFQDN:        "api.stripe.com",
		DestIP:          "54.1.1.1",
		DestPort:        443,
		Protocol:        "TCP",
		Verdict:         "FORWARDED",
		Bytes:           1024,
		Timestamp:       time.Now(),
	}

	acc.ingest(f)
	acc.ingest(f) // duplicate → increments FlowCount

	snap := acc.GetSnapshot("payment/Deployment/payment-api")
	if len(snap.Destinations) != 1 {
		t.Fatalf("expected 1 destination, got %d", len(snap.Destinations))
	}
	if snap.Destinations[0].FlowCount != 2 {
		t.Errorf("expected FlowCount=2, got %d", snap.Destinations[0].FlowCount)
	}
}

func TestAccumulator_Purge(t *testing.T) {
	acc := NewAccumulator(nil, &noopStore{}, 1*time.Millisecond, time.Minute,
		logr.Discard())

	f := Flow{
		SourceNamespace: "payment",
		SourceWorkload:  "Deployment/payment-api",
		DestFQDN:        "old.example.com",
		DestPort:        443,
		Protocol:        "TCP",
		Timestamp:       time.Now().Add(-1 * time.Hour), // very old
	}
	acc.ingest(f)

	time.Sleep(5 * time.Millisecond)
	acc.purge()

	snap := acc.GetSnapshot("payment/Deployment/payment-api")
	if len(snap.Destinations) != 0 {
		t.Errorf("expected 0 destinations after purge, got %d", len(snap.Destinations))
	}
}

func TestAccumulator_Cap(t *testing.T) {
	acc := NewAccumulator(nil, &noopStore{}, 24*time.Hour, time.Minute,
		logr.Discard())

	// Fill exactly at the cap
	for i := 0; i < maxDestinations+10; i++ {
		acc.ingest(Flow{
			SourceNamespace: "test",
			SourceWorkload:  "Deployment/test",
			DestFQDN:        "",
			DestIP:          "1.2.3.4",
			DestPort:        uint32(i + 1),
			Protocol:        "TCP",
			Timestamp:       time.Now(),
		})
	}

	acc.mu.RLock()
	count := len(acc.entries)
	acc.mu.RUnlock()

	if count > maxDestinations {
		t.Errorf("accumulator exceeded cap: %d > %d", count, maxDestinations)
	}
}

