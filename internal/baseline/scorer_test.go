package baseline

import (
	"testing"
	"time"

	"github.com/ihsen/egress-guardian-operator/internal/observer"
)

func entry(fqdn, ip string, port uint32, proto string, flowCount uint64, snapshots int) *observer.DestEntry {
	return &observer.DestEntry{
		Workload:  "payment/Deployment/payment-api",
		DestFQDN:  fqdn,
		DestIP:    ip,
		DestPort:  port,
		Protocol:  proto,
		FirstSeen: time.Now().Add(-24 * time.Hour),
		LastSeen:  time.Now(),
		FlowCount: flowCount,
		Bytes:     1024,
		Snapshots: snapshots,
	}
}

func TestScore_StableFQDN(t *testing.T) {
	e := entry("api.stripe.com", "54.187.174.169", 443, "TCP", 50, 5)
	s := Score(e)
	if s < 80 {
		t.Errorf("expected score >= 80 for stable FQDN, got %d", s)
	}
}

func TestScore_DirectPublicIP(t *testing.T) {
	e := entry("", "8.8.8.8", 443, "TCP", 1, 1)
	s := Score(e)
	if s >= 50 {
		t.Errorf("expected score < 50 for direct public IP, got %d", s)
	}
}

func TestScore_WildcardLarge(t *testing.T) {
	// Single observation so no stability/volume bonus; wildcard -30 dominates.
	e := entry("*.com", "", 443, "TCP", 1, 1)
	s := Score(e)
	if s >= 50 {
		t.Errorf("expected score < 50 for large wildcard (single obs), got %d", s)
	}
	// Regardless of score, RiskLevel must be High.
	if r := RiskLevel(e, s); r != "High" {
		t.Errorf("expected RiskLevel=High for *.com, got %s", r)
	}
}

func TestScore_SeenOnce(t *testing.T) {
	e := entry("unknown.example.com", "1.2.3.4", 443, "TCP", 1, 1)
	base := 50 + 10 + 10 // standard port + no wildcard
	s := Score(e)
	// Should be penalised for single observation
	if s >= base {
		t.Errorf("expected penalty for single observation, score=%d base=%d", s, base)
	}
}

func TestRiskLevel_High_DirectIP(t *testing.T) {
	e := entry("", "1.2.3.4", 443, "TCP", 1, 1)
	s := Score(e)
	r := RiskLevel(e, s)
	if r != "High" {
		t.Errorf("expected High risk for direct public IP, got %s", r)
	}
}

func TestRiskLevel_Low_StableFQDN(t *testing.T) {
	e := entry("api.stripe.com", "54.1.1.1", 443, "TCP", 100, 10)
	s := Score(e)
	r := RiskLevel(e, s)
	if r != "Low" {
		t.Errorf("expected Low risk for stable FQDN, got %s (score=%d)", r, s)
	}
}
