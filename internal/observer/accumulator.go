package observer

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/go-logr/logr"
)

const maxDestinations = 500

// DestEntry is one aggregated egress destination for a given workload.
type DestEntry struct {
	Workload  string    `json:"workload"`
	DestFQDN  string    `json:"destFqdn,omitempty"`
	DestIP    string    `json:"destIp,omitempty"`
	DestPort  uint32    `json:"destPort"`
	Protocol  string    `json:"protocol"`
	FirstSeen time.Time `json:"firstSeen"`
	LastSeen  time.Time `json:"lastSeen"`
	FlowCount uint64    `json:"flowCount"`
	Bytes     uint64    `json:"bytes"`
	// Snapshots tracks how many distinct snapshot cycles this entry appeared in.
	Snapshots int `json:"snapshots"`
}

// destKey uniquely identifies a destination within a workload.
type destKey struct {
	workload string
	fqdn     string
	ip       string
	port     uint32
	proto    string
}

// Snapshot is the serialisable view written to the ConfigMap.
type Snapshot struct {
	WorkloadKey  string       `json:"workloadKey"` // "namespace/kind/name"
	Destinations []*DestEntry `json:"destinations"`
	Truncated    bool         `json:"truncated"`
	GeneratedAt  time.Time    `json:"generatedAt"`
}

// SnapshotStore is implemented by store.ConfigMapStore.
type SnapshotStore interface {
	SaveSnapshot(ctx context.Context, namespace, name string, snap *Snapshot) error
}

// Accumulator consumes a Flow stream, aggregates destinations per workload,
// applies a sliding purge window, and periodically writes snapshots to the store.
// It implements sigs.k8s.io/controller-runtime/pkg/manager.Runnable.
type Accumulator struct {
	source           FlowSource
	store            SnapshotStore
	window           time.Duration
	snapshotInterval time.Duration
	// workloadNS maps workload key → namespace (for store writes).
	workloadNS map[string]string
	// configMapName maps workload key → ConfigMap name.
	configMapName map[string]string

	mu      sync.RWMutex
	entries map[destKey]*DestEntry
	log     logr.Logger
}

// NewAccumulator constructs an Accumulator.
func NewAccumulator(src FlowSource, store SnapshotStore, window, snapshotInterval time.Duration, log logr.Logger) *Accumulator {
	return &Accumulator{
		source:           src,
		store:            store,
		window:           window,
		snapshotInterval: snapshotInterval,
		workloadNS:       make(map[string]string),
		configMapName:    make(map[string]string),
		entries:          make(map[destKey]*DestEntry),
		log:              log,
	}
}

// RegisterWorkload tells the accumulator which namespace/configmap to use for a workload key.
func (a *Accumulator) RegisterWorkload(workloadKey, namespace, cmName string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.workloadNS[workloadKey] = namespace
	a.configMapName[workloadKey] = cmName
}

// Start implements manager.Runnable. It blocks until ctx is cancelled.
func (a *Accumulator) Start(ctx context.Context) error {
	flowCh := make(chan Flow, 256)

	go func() {
		if err := a.source.Observe(ctx, flowCh); err != nil {
			a.log.Error(err, "flow source error")
		}
	}()

	ticker := time.NewTicker(a.snapshotInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case f := <-flowCh:
			a.ingest(f)
		case <-ticker.C:
			a.purge()
			a.flushSnapshots(ctx)
		}
	}
}

func (a *Accumulator) ingest(f Flow) {
	k := destKey{
		workload: fmt.Sprintf("%s/%s", f.SourceNamespace, f.SourceWorkload),
		fqdn:     f.DestFQDN,
		ip:       f.DestIP,
		port:     f.DestPort,
		proto:    f.Protocol,
	}
	a.mu.Lock()
	defer a.mu.Unlock()

	if e, ok := a.entries[k]; ok {
		e.LastSeen = f.Timestamp
		e.FlowCount++
		e.Bytes += f.Bytes
		return
	}
	if len(a.entries) >= maxDestinations {
		return // cap reached; truncated flag is set in snapshot
	}
	a.entries[k] = &DestEntry{
		Workload:  k.workload,
		DestFQDN:  f.DestFQDN,
		DestIP:    f.DestIP,
		DestPort:  f.DestPort,
		Protocol:  f.Protocol,
		FirstSeen: f.Timestamp,
		LastSeen:  f.Timestamp,
		FlowCount: 1,
		Bytes:     f.Bytes,
		Snapshots: 0,
	}
}

func (a *Accumulator) purge() {
	cutoff := time.Now().Add(-a.window)
	a.mu.Lock()
	defer a.mu.Unlock()
	for k, e := range a.entries {
		if e.LastSeen.Before(cutoff) {
			delete(a.entries, k)
		}
	}
}

func (a *Accumulator) flushSnapshots(ctx context.Context) {
	// Group entries by workload key.
	byWorkload := make(map[string][]*DestEntry)
	a.mu.Lock()
	for k, e := range a.entries {
		e.Snapshots++
		byWorkload[k.workload] = append(byWorkload[k.workload], e)
	}
	truncated := len(a.entries) >= maxDestinations
	a.mu.Unlock()

	for wk, dests := range byWorkload {
		ns := a.workloadNS[wk]
		cmName := a.configMapName[wk]
		if ns == "" || cmName == "" {
			continue
		}
		snap := &Snapshot{
			WorkloadKey:  wk,
			Destinations: dests,
			Truncated:    truncated,
			GeneratedAt:  time.Now(),
		}
		if err := a.store.SaveSnapshot(ctx, ns, cmName, snap); err != nil {
			a.log.Error(err, "failed to save snapshot", "workload", wk)
		}
	}
}

// GetSnapshot returns a current in-memory snapshot for the given workload key (for tests).
func (a *Accumulator) GetSnapshot(workloadKey string) *Snapshot {
	a.mu.RLock()
	defer a.mu.RUnlock()

	var dests []*DestEntry
	for k, e := range a.entries {
		if k.workload == workloadKey {
			cp := *e
			dests = append(dests, &cp)
		}
	}
	return &Snapshot{
		WorkloadKey:  workloadKey,
		Destinations: dests,
		Truncated:    len(a.entries) >= maxDestinations,
		GeneratedAt:  time.Now(),
	}
}

// MarshalSnapshot serialises a Snapshot to JSON for ConfigMap storage.
func MarshalSnapshot(s *Snapshot) ([]byte, error) {
	return json.Marshal(s)
}

// UnmarshalSnapshot deserialises a Snapshot from JSON.
func UnmarshalSnapshot(data []byte) (*Snapshot, error) {
	var s Snapshot
	return &s, json.Unmarshal(data, &s)
}
