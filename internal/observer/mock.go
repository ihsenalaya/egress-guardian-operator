package observer

import (
	"context"
	"time"
)

// MockFlowSource replays a fixed script of Flows for local testing and demos.
// It loops indefinitely until ctx is cancelled, sleeping interval between rounds.
type MockFlowSource struct {
	Flows    []Flow
	Interval time.Duration
}

// NewMockFlowSource returns a MockFlowSource with realistic demo flows.
func NewMockFlowSource() *MockFlowSource {
	now := time.Now()
	return &MockFlowSource{
		Interval: 5 * time.Second,
		Flows: []Flow{
			{
				SourceNamespace: "payment",
				SourcePod:       "payment-api-7d6f9c-abc12",
				SourceWorkload:  "Deployment/payment-api",
				DestFQDN:        "api.stripe.com",
				DestIP:          "54.187.174.169",
				DestPort:        443,
				Protocol:        "TCP",
				Verdict:         "FORWARDED",
				Bytes:           2048,
				Timestamp:       now,
			},
			{
				SourceNamespace: "payment",
				SourcePod:       "payment-api-7d6f9c-abc12",
				SourceWorkload:  "Deployment/payment-api",
				DestFQDN:        "login.microsoftonline.com",
				DestIP:          "20.190.144.130",
				DestPort:        443,
				Protocol:        "TCP",
				Verdict:         "FORWARDED",
				Bytes:           1024,
				Timestamp:       now,
			},
			{
				SourceNamespace: "payment",
				SourcePod:       "payment-api-7d6f9c-abc12",
				SourceWorkload:  "Deployment/payment-api",
				DestFQDN:        "",
				DestIP:          "8.8.8.8",
				DestPort:        443,
				Protocol:        "TCP",
				Verdict:         "FORWARDED",
				Bytes:           512,
				Timestamp:       now,
			},
			{
				SourceNamespace: "payment",
				SourcePod:       "payment-api-7d6f9c-abc12",
				SourceWorkload:  "Deployment/payment-api",
				DestFQDN:        "storage.googleapis.com",
				DestIP:          "142.250.80.48",
				DestPort:        443,
				Protocol:        "TCP",
				Verdict:         "FORWARDED",
				Bytes:           8192,
				Timestamp:       now,
			},
		},
	}
}

func (m *MockFlowSource) Observe(ctx context.Context, out chan<- Flow) error {
	for {
		for _, f := range m.Flows {
			f.Timestamp = time.Now()
			select {
			case <-ctx.Done():
				return nil
			case out <- f:
			}
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(m.Interval):
		}
	}
}
