package cilium

import (
	"fmt"
	"sort"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/yaml"

	egressv1alpha1 "github.com/ihsen/egress-guardian-operator/api/v1alpha1"
	"github.com/ihsen/egress-guardian-operator/internal/baseline"
	"github.com/ihsen/egress-guardian-operator/internal/observer"
)

// GenerationResult holds the output of a policy generation run.
type GenerationResult struct {
	CNP                  *unstructured.Unstructured
	YAML                 string
	ExcludedDestinations []egressv1alpha1.ExcludedDestination
	ConfidenceScore      int
	RiskLevel            egressv1alpha1.RiskLevel
}

// Generate builds a CiliumNetworkPolicy from a scored baseline snapshot.
// Destinations with risk=High are excluded and recorded in ExcludedDestinations.
func Generate(
	profileName, namespace string,
	policy egressv1alpha1.PolicyConfig,
	snap *observer.Snapshot,
) GenerationResult {
	scored := baseline.Build(snap)

	cnp := NewCNP(policy.GeneratedPolicyName, namespace)
	if policy.GeneratedPolicyName == "" {
		cnp.SetName(fmt.Sprintf("%s-egress-allowlist", profileName))
	}

	// Endpoint selector: reuse the profile name as the "app" label selector.
	if err := SetEndpointSelector(cnp, map[string]interface{}{"app": profileName}); err != nil {
		_ = err // best effort — caller checks YAML output
	}

	var rules []interface{}
	var excluded []egressv1alpha1.ExcludedDestination

	// DNS rule (always first when allowDNS=true)
	if policy.AllowDNS {
		rules = append(rules, dnsPolicyRule())
	}

	// Group scored entries by FQDN+port+proto to build compact toFQDNs rules
	type fqdnKey struct {
		fqdn  string
		port  uint32
		proto string
	}
	fqdnGroups := make(map[fqdnKey]bool)

	for _, se := range scored {
		e := se.Entry

		// Exclude High-risk destinations
		if se.RiskLevel == "High" || IsDirectPublicIP(e.DestFQDN, e.DestIP) {
			reason := "risk=High"
			if IsDirectPublicIP(e.DestFQDN, e.DestIP) {
				reason = "IP publique directe sans FQDN observé"
			}
			excluded = append(excluded, egressv1alpha1.ExcludedDestination{
				Dest:   fmt.Sprintf("%s:%d", destHost(e), e.DestPort),
				Risk:   egressv1alpha1.RiskHigh,
				Reason: reason,
			})
			continue
		}

		// Medium wildcards: exclude from auto-enforce
		if e.DestFQDN != "" && WildcardRisk(e.DestFQDN) == "Medium" {
			excluded = append(excluded, egressv1alpha1.ExcludedDestination{
				Dest:   fmt.Sprintf("%s:%d", e.DestFQDN, e.DestPort),
				Risk:   egressv1alpha1.RiskMedium,
				Reason: "wildcard cloud provider — revue manuelle requise",
			})
			continue
		}

		if e.DestFQDN != "" {
			fqdnGroups[fqdnKey{fqdn: e.DestFQDN, port: e.DestPort, proto: e.Protocol}] = true
		}
	}

	// Build toFQDNs rules grouped by port/protocol
	type portProto struct {
		port  uint32
		proto string
	}
	portFQDNs := make(map[portProto][]string)
	for k := range fqdnGroups {
		pp := portProto{port: k.port, proto: k.proto}
		portFQDNs[pp] = append(portFQDNs[pp], k.fqdn)
	}

	// Sort for determinism
	ppKeys := make([]portProto, 0, len(portFQDNs))
	for pp := range portFQDNs {
		ppKeys = append(ppKeys, pp)
	}
	sort.Slice(ppKeys, func(i, j int) bool {
		if ppKeys[i].port != ppKeys[j].port {
			return ppKeys[i].port < ppKeys[j].port
		}
		return ppKeys[i].proto < ppKeys[j].proto
	})

	for _, pp := range ppKeys {
		fqdns := portFQDNs[pp]
		sort.Strings(fqdns)
		rule := fqdnEgressRule(fqdns, pp.port, pp.proto)
		rules = append(rules, rule)
	}

	_ = SetEgressRules(cnp, rules)

	yamlBytes, _ := yaml.Marshal(cnp.Object)
	yamlStr := string(yamlBytes)

	score := baseline.AggregateScore(snap.Destinations)
	risk := computeOverallRisk(scored, score)

	return GenerationResult{
		CNP:                  cnp,
		YAML:                 yamlStr,
		ExcludedDestinations: excluded,
		ConfidenceScore:      score,
		RiskLevel:            risk,
	}
}

func destHost(e *observer.DestEntry) string {
	if e.DestFQDN != "" {
		return e.DestFQDN
	}
	return e.DestIP
}

func dnsPolicyRule() interface{} {
	return map[string]interface{}{
		"toEndpoints": []interface{}{
			map[string]interface{}{
				"matchLabels": map[string]interface{}{
					"k8s:io.kubernetes.pod.namespace": "kube-system",
					"k8s:k8s-app":                    "kube-dns",
				},
			},
		},
		"toPorts": []interface{}{
			map[string]interface{}{
				"ports": []interface{}{
					map[string]interface{}{"port": "53", "protocol": "UDP"},
				},
				"rules": map[string]interface{}{
					"dns": []interface{}{
						map[string]interface{}{"matchPattern": "*"},
					},
				},
			},
		},
	}
}

func fqdnEgressRule(fqdns []string, port uint32, proto string) interface{} {
	toFQDNs := make([]interface{}, 0, len(fqdns))
	for _, f := range fqdns {
		toFQDNs = append(toFQDNs, map[string]interface{}{"matchName": f})
	}
	return map[string]interface{}{
		"toFQDNs": toFQDNs,
		"toPorts": []interface{}{
			map[string]interface{}{
				"ports": []interface{}{
					map[string]interface{}{
						"port":     fmt.Sprintf("%d", port),
						"protocol": proto,
					},
				},
			},
		},
	}
}

func computeOverallRisk(scored []baseline.ScoredEntry, score int) egressv1alpha1.RiskLevel {
	for _, se := range scored {
		if se.RiskLevel == "High" {
			// High entries exist but are excluded; overall risk is at least Medium
			if score < 50 {
				return egressv1alpha1.RiskHigh
			}
			return egressv1alpha1.RiskMedium
		}
	}
	if score >= 80 {
		return egressv1alpha1.RiskLow
	}
	if score >= 50 {
		return egressv1alpha1.RiskMedium
	}
	return egressv1alpha1.RiskHigh
}
