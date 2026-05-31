package observer

import "context"

// FlowSource is the streaming interface for egress flow ingestion.
// Implementations must be started in a goroutine; they block until ctx is cancelled.
type FlowSource interface {
	// Observe opens a stream and pushes matched egress Flows into out.
	// Returns a non-nil error only on unrecoverable failures; ctx cancellation is not an error.
	Observe(ctx context.Context, out chan<- Flow) error
}
