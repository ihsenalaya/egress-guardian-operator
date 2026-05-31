package cilium

import (
	"strings"
	"testing"
	"time"

	egressv1alpha1 "github.com/ihsen/egress-guardian-operator/api/v1alpha1"
	"github.com/ihsen/egress-guardian-operator/internal/observer"
)

func makeSnap(dests ...*observer.DestEntry) *observer.Snapshot {
	return &observer.Snapshot{
		WorkloadKey:  "payment/Deployment/payment-api",
		Destinations: dests,
		GeneratedAt:  time.Now(),
	}
}

func makeEntry(fqdn, ip string, port uint32, proto string, flowCount uint64, snapshots int) *observer.DestEntry {
	return &observer.DestEntry{
		Workload:  "payment/Deployment/payment-api",
		DestFQDN:  fqdn,
		DestIP:    ip,
		DestPort:  port,
		Protocol:  proto,
		FirstSeen: time.Now().Add(-48 * time.Hour),
		LastSeen:  time.Now(),
		FlowCount: flowCount,
		Snapshots: snapshots,
	}
}

func TestGenerate_IncludesAllowDNS(t *testing.T) {
	snap := makeSnap(makeEntry("api.stripe.com", "54.1.1.1", 443, "TCP", 10, 3))
	policy := egressv1alpha1.PolicyConfig{AllowDNS: true, GeneratedPolicyName: "test-policy"}

	result := Generate("payment-api", "payment", policy, snap)

	if !strings.Contains(result.YAML, "kube-dns") {
		t.Error("expected DNS rule in generated YAML")
	}
}

func TestGenerate_ExcludesDirectPublicIP(t *testing.T) {
	snap := makeSnap(
		makeEntry("api.stripe.com", "54.1.1.1", 443, "TCP", 10, 3),
		makeEntry("", "8.8.8.8", 443, "TCP", 5, 2),
	)
	policy := egressv1alpha1.PolicyConfig{AllowDNS: true, GeneratedPolicyName: "test-policy"}

	result := Generate("payment-api", "payment", policy, snap)

	found := false
	for _, ex := range result.ExcludedDestinations {
		if ex.Dest == "8.8.8.8:443" {
			found = true
			if ex.Risk != egressv1alpha1.RiskHigh {
				t.Errorf("expected High risk for direct IP exclusion, got %s", ex.Risk)
			}
		}
	}
	if !found {
		t.Error("expected 8.8.8.8:443 to be in excluded destinations")
	}
}

func TestGenerate_ExcludesLargeWildcard(t *testing.T) {
	snap := makeSnap(
		makeEntry("*.com", "", 443, "TCP", 10, 3),
	)
	policy := egressv1alpha1.PolicyConfig{AllowDNS: false, GeneratedPolicyName: "test-policy"}

	result := Generate("payment-api", "payment", policy, snap)

	if len(result.ExcludedDestinations) == 0 {
		t.Error("expected *.com to be excluded")
	}
}

func TestGenerate_IncludesFQDN(t *testing.T) {
	snap := makeSnap(
		makeEntry("api.stripe.com", "54.1.1.1", 443, "TCP", 20, 5),
		makeEntry("login.microsoftonline.com", "20.1.1.1", 443, "TCP", 15, 4),
	)
	policy := egressv1alpha1.PolicyConfig{AllowDNS: false, GeneratedPolicyName: "test-policy"}

	result := Generate("payment-api", "payment", policy, snap)

	if !strings.Contains(result.YAML, "api.stripe.com") {
		t.Error("expected api.stripe.com in generated YAML")
	}
	if !strings.Contains(result.YAML, "login.microsoftonline.com") {
		t.Error("expected login.microsoftonline.com in generated YAML")
	}
	if len(result.ExcludedDestinations) != 0 {
		t.Errorf("expected 0 excluded destinations, got %d", len(result.ExcludedDestinations))
	}
}

func TestGenerate_ConfidenceScore(t *testing.T) {
	snap := makeSnap(
		makeEntry("api.stripe.com", "54.1.1.1", 443, "TCP", 50, 5),
	)
	policy := egressv1alpha1.PolicyConfig{AllowDNS: true, GeneratedPolicyName: "test-policy"}

	result := Generate("payment-api", "payment", policy, snap)

	if result.ConfidenceScore < 0 || result.ConfidenceScore > 100 {
		t.Errorf("ConfidenceScore out of range: %d", result.ConfidenceScore)
	}
}
