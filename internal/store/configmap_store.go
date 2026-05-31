package store

import (
	"context"
	"encoding/json"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	egressv1alpha1 "github.com/ihsen/egress-guardian-operator/api/v1alpha1"
	"github.com/ihsen/egress-guardian-operator/internal/observer"
)

const (
	keyBaseline = "baseline.json"
	keyPolicy   = "policy.yaml"
)

// ConfigMapStore persists baseline snapshots and generated policy YAML
// in a per-profile ConfigMap (owned by the EgressProfile).
type ConfigMapStore struct {
	client client.Client
}

// New returns a ConfigMapStore backed by the given controller-runtime client.
func New(c client.Client) *ConfigMapStore {
	return &ConfigMapStore{client: c}
}

// ConfigMapName returns the canonical ConfigMap name for an EgressProfile.
func ConfigMapName(profileName string) string {
	return fmt.Sprintf("egress-guardian-%s", profileName)
}

// EnsureConfigMap creates the ConfigMap if it does not exist, setting the
// EgressProfile as owner so it is garbage-collected together.
func (s *ConfigMapStore) EnsureConfigMap(ctx context.Context, profile *egressv1alpha1.EgressProfile) error {
	cm := &corev1.ConfigMap{}
	name := ConfigMapName(profile.Name)
	err := s.client.Get(ctx, client.ObjectKey{Namespace: profile.Namespace, Name: name}, cm)
	if err == nil {
		return nil
	}
	if !errors.IsNotFound(err) {
		return fmt.Errorf("get configmap: %w", err)
	}
	cm = &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: profile.Namespace,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "egress-guardian-operator",
				"egress.platform.io/profile":   profile.Name,
			},
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion:         egressv1alpha1.GroupVersion.String(),
					Kind:               "EgressProfile",
					Name:               profile.Name,
					UID:                profile.UID,
					BlockOwnerDeletion: boolPtr(true),
					Controller:         boolPtr(true),
				},
			},
		},
		Data: map[string]string{},
	}
	return s.client.Create(ctx, cm)
}

// SaveSnapshot serialises a Snapshot to baseline.json in the ConfigMap.
func (s *ConfigMapStore) SaveSnapshot(ctx context.Context, namespace, name string, snap *observer.Snapshot) error {
	data, err := json.Marshal(snap)
	if err != nil {
		return fmt.Errorf("marshal snapshot: %w", err)
	}
	return s.patchData(ctx, namespace, name, keyBaseline, string(data))
}

// SavePolicyYAML writes a generated CiliumNetworkPolicy YAML to policy.yaml.
func (s *ConfigMapStore) SavePolicyYAML(ctx context.Context, namespace, name, yaml string) error {
	return s.patchData(ctx, namespace, name, keyPolicy, yaml)
}

// GetSnapshot reads and deserialises the baseline snapshot from the ConfigMap.
func (s *ConfigMapStore) GetSnapshot(ctx context.Context, namespace, name string) (*observer.Snapshot, error) {
	cm := &corev1.ConfigMap{}
	if err := s.client.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, cm); err != nil {
		return nil, fmt.Errorf("get configmap %s/%s: %w", namespace, name, err)
	}
	raw, ok := cm.Data[keyBaseline]
	if !ok {
		return &observer.Snapshot{}, nil
	}
	return observer.UnmarshalSnapshot([]byte(raw))
}

// GetPolicyYAML reads the generated policy YAML from the ConfigMap.
func (s *ConfigMapStore) GetPolicyYAML(ctx context.Context, namespace, name string) (string, error) {
	cm := &corev1.ConfigMap{}
	if err := s.client.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, cm); err != nil {
		return "", fmt.Errorf("get configmap %s/%s: %w", namespace, name, err)
	}
	return cm.Data[keyPolicy], nil
}

func (s *ConfigMapStore) patchData(ctx context.Context, namespace, name, key, value string) error {
	cm := &corev1.ConfigMap{}
	if err := s.client.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, cm); err != nil {
		return fmt.Errorf("get configmap %s/%s: %w", namespace, name, err)
	}
	patch := client.MergeFrom(cm.DeepCopy())
	if cm.Data == nil {
		cm.Data = make(map[string]string)
	}
	cm.Data[key] = value
	return s.client.Patch(ctx, cm, patch)
}

func boolPtr(b bool) *bool { return &b }
