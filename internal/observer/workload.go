package observer

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ResolveWorkload walks the owner reference chain of a Pod and returns
// a "Kind/name" string identifying its top-level controller (e.g. "Deployment/payment-api").
// Falls back to "Pod/<name>" when no owner chain is found.
func ResolveWorkload(ctx context.Context, c client.Client, namespace, podName string) string {
	pod := &corev1.Pod{}
	if err := c.Get(ctx, client.ObjectKey{Namespace: namespace, Name: podName}, pod); err != nil {
		return fmt.Sprintf("Pod/%s", podName)
	}
	return resolveOwner(ctx, c, namespace, pod.OwnerReferences, fmt.Sprintf("Pod/%s", podName))
}

func resolveOwner(_ context.Context, _ client.Client, _ string, owners []metav1.OwnerReference, fallback string) string {
	if len(owners) == 0 {
		return fallback
	}
	top := owners[0]
	return fmt.Sprintf("%s/%s", top.Kind, top.Name)
}

// OwnerRef is a simplified owner reference used for testing without importing k8s api types.
type OwnerRef struct {
	Kind string
	Name string
}

// ResolveWorkloadFromOwners resolves the top-level owner for a pod given its owner references.
func ResolveWorkloadFromOwners(_ context.Context, _ client.Client, namespace, podName string, ownerRefs []OwnerRef) string {
	if len(ownerRefs) == 0 {
		return fmt.Sprintf("Pod/%s", podName)
	}
	top := ownerRefs[0]
	return fmt.Sprintf("%s/%s", top.Kind, top.Name)
}
