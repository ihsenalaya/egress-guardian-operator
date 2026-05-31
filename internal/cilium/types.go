package cilium

import (
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var CiliumNetworkPolicyGVK = schema.GroupVersionKind{
	Group:   "cilium.io",
	Version: "v2",
	Kind:    "CiliumNetworkPolicy",
}

// NewCNP returns an empty CiliumNetworkPolicy Unstructured object.
func NewCNP(name, namespace string) *unstructured.Unstructured {
	cnp := &unstructured.Unstructured{}
	cnp.SetGroupVersionKind(CiliumNetworkPolicyGVK)
	cnp.SetName(name)
	cnp.SetNamespace(namespace)
	cnp.SetLabels(map[string]string{
		"app.kubernetes.io/managed-by": "egress-guardian-operator",
	})
	return cnp
}

// SetEndpointSelector sets spec.endpointSelector.matchLabels on the CNP.
func SetEndpointSelector(cnp *unstructured.Unstructured, matchLabels map[string]interface{}) error {
	return unstructured.SetNestedField(cnp.Object, matchLabels,
		"spec", "endpointSelector", "matchLabels")
}

// SetEgressRules replaces spec.egress with the provided rules slice.
func SetEgressRules(cnp *unstructured.Unstructured, rules []interface{}) error {
	return unstructured.SetNestedSlice(cnp.Object, rules, "spec", "egress")
}
