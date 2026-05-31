/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EgressMode defines the operational mode of the profile.
// +kubebuilder:validation:Enum=Observe;Suggest;Enforce
type EgressMode string

const (
	ModeObserve EgressMode = "Observe"
	ModeSuggest EgressMode = "Suggest"
	ModeEnforce EgressMode = "Enforce"
)

// EgressPhase represents the current phase of an EgressProfile.
// +kubebuilder:validation:Enum=Observing;Proposed;AwaitingApproval;Enforced;Failed
type EgressPhase string

const (
	PhaseObserving       EgressPhase = "Observing"
	PhaseProposed        EgressPhase = "Proposed"
	PhaseAwaitingApproval EgressPhase = "AwaitingApproval"
	PhaseEnforced        EgressPhase = "Enforced"
	PhaseFailed          EgressPhase = "Failed"
)

// TargetRef identifies the workload governed by this profile.
type TargetRef struct {
	// +kubebuilder:validation:Required
	APIVersion string `json:"apiVersion"`
	// +kubebuilder:validation:Required
	Kind string `json:"kind"`
	// +kubebuilder:validation:Required
	Name string `json:"name"`
}

// PolicyConfig controls how the generated CiliumNetworkPolicy is structured.
type PolicyConfig struct {
	// AllowDNS adds a kube-dns egress rule so DNS resolution keeps working.
	// +kubebuilder:default=true
	AllowDNS bool `json:"allowDNS,omitempty"`
	// AllowKubeSystem allows traffic to kube-system endpoints.
	// +kubebuilder:default=true
	AllowKubeSystem bool `json:"allowKubeSystem,omitempty"`
	// GeneratedPolicyName is the name of the CiliumNetworkPolicy to create.
	GeneratedPolicyName string `json:"generatedPolicyName,omitempty"`
}

// GitOpsConfig controls GitOps export of generated policies.
type GitOpsConfig struct {
	// Enabled enables writing generated policy YAML to outputPath.
	Enabled bool `json:"enabled,omitempty"`
	// OutputPath is the local filesystem path where YAML is written.
	OutputPath string `json:"outputPath,omitempty"`
}

// EgressProfileSpec defines the desired state of EgressProfile.
type EgressProfileSpec struct {
	// TargetRef identifies the workload to govern.
	// +kubebuilder:validation:Required
	TargetRef TargetRef `json:"targetRef"`

	// Mode drives the state machine: Observe | Suggest | Enforce.
	// +kubebuilder:validation:Required
	// +kubebuilder:default=Observe
	Mode EgressMode `json:"mode"`

	// BaselineWindow is the sliding retention window for accumulated flows (e.g. "24h").
	// +kubebuilder:default="24h"
	BaselineWindow string `json:"baselineWindow,omitempty"`

	// Policy controls the generated CiliumNetworkPolicy structure.
	Policy PolicyConfig `json:"policy,omitempty"`

	// GitOps controls GitOps export of generated policy YAML.
	GitOps GitOpsConfig `json:"gitOps,omitempty"`
}

// RiskSummary counts destinations by risk level.
type RiskSummary struct {
	Low    int `json:"low,omitempty"`
	Medium int `json:"medium,omitempty"`
	High   int `json:"high,omitempty"`
}

// EgressProfileStatus defines the observed state of EgressProfile.
type EgressProfileStatus struct {
	// Phase is the current phase of the profile.
	Phase EgressPhase `json:"phase,omitempty"`

	// Conditions holds standard Kubernetes conditions.
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// ObservedDestinationsCount is the number of distinct egress destinations observed.
	ObservedDestinationsCount int `json:"observedDestinationsCount,omitempty"`

	// Truncated is true when the destination inventory hit the cap (500).
	Truncated bool `json:"truncated,omitempty"`

	// LastObservationTime is when the accumulator last produced a snapshot.
	LastObservationTime *metav1.Time `json:"lastObservationTime,omitempty"`

	// BaselineConfigMapRef is the name of the ConfigMap holding baseline.json and policy.yaml.
	BaselineConfigMapRef string `json:"baselineConfigMapRef,omitempty"`

	// ProposalRef is the name of the EgressPolicyProposal created in Suggest/Enforce mode.
	ProposalRef string `json:"proposalRef,omitempty"`

	// RiskSummary counts destinations by risk level.
	RiskSummary RiskSummary `json:"riskSummary,omitempty"`

	// Message holds a human-readable explanation of the current phase.
	Message string `json:"message,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status
//+kubebuilder:resource:scope=Namespaced,shortName=ep
//+kubebuilder:printcolumn:name="Mode",type=string,JSONPath=`.spec.mode`
//+kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
//+kubebuilder:printcolumn:name="Destinations",type=integer,JSONPath=`.status.observedDestinationsCount`
//+kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// EgressProfile is the Schema for the egressprofiles API.
type EgressProfile struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   EgressProfileSpec   `json:"spec,omitempty"`
	Status EgressProfileStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// EgressProfileList contains a list of EgressProfile.
type EgressProfileList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []EgressProfile `json:"items"`
}

func init() {
	SchemeBuilder.Register(&EgressProfile{}, &EgressProfileList{})
}
