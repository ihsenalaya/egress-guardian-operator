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

// ProposalPhase represents the lifecycle phase of an EgressPolicyProposal.
// +kubebuilder:validation:Enum=Draft;PendingApproval;Approved;Applied;Failed
type ProposalPhase string

const (
	ProposalPhaseDraft           ProposalPhase = "Draft"
	ProposalPhasePendingApproval ProposalPhase = "PendingApproval"
	ProposalPhaseApproved        ProposalPhase = "Approved"
	ProposalPhaseApplied         ProposalPhase = "Applied"
	ProposalPhaseFailed          ProposalPhase = "Failed"
)

// RiskLevel categorises the overall risk of a proposal.
// +kubebuilder:validation:Enum=Low;Medium;High
type RiskLevel string

const (
	RiskLow    RiskLevel = "Low"
	RiskMedium RiskLevel = "Medium"
	RiskHigh   RiskLevel = "High"
)

// ProfileRef points back to the owning EgressProfile.
type ProfileRef struct {
	// +kubebuilder:validation:Required
	Name string `json:"name"`
}

// GeneratedPolicyRef describes the CiliumNetworkPolicy that will be applied.
type GeneratedPolicyRef struct {
	// Type is always "CiliumNetworkPolicy".
	// +kubebuilder:default=CiliumNetworkPolicy
	Type string `json:"type,omitempty"`
	// Name is the metadata.name of the CNP object.
	Name string `json:"name,omitempty"`
}

// ApprovalSpec is the approval gate; it is filled by a human operator.
type ApprovalSpec struct {
	// Approved must be set to true to unlock enforcement in mode=Enforce.
	Approved bool `json:"approved,omitempty"`
	// ApprovedBy records who approved (audit trail).
	ApprovedBy string `json:"approvedBy,omitempty"`
	// ApprovedAt records when the approval was granted.
	ApprovedAt *metav1.Time `json:"approvedAt,omitempty"`
}

// EgressPolicyProposalSpec defines the desired state of EgressPolicyProposal.
type EgressPolicyProposalSpec struct {
	// ProfileRef points to the EgressProfile that owns this proposal.
	// +kubebuilder:validation:Required
	ProfileRef ProfileRef `json:"profileRef"`

	// GeneratedPolicy describes the CiliumNetworkPolicy to apply.
	GeneratedPolicy GeneratedPolicyRef `json:"generatedPolicy,omitempty"`

	// Approval is the human approval gate required before enforcement.
	Approval ApprovalSpec `json:"approval,omitempty"`
}

// ExcludedDestination records a destination that was excluded from the proposal.
type ExcludedDestination struct {
	// Dest is the destination in "host:port" or "ip:port" format.
	Dest string `json:"dest"`
	// Risk is the risk level that caused the exclusion.
	Risk RiskLevel `json:"risk"`
	// Reason explains why this destination was excluded.
	Reason string `json:"reason"`
}

// EgressPolicyProposalStatus defines the observed state of EgressPolicyProposal.
type EgressPolicyProposalStatus struct {
	// Phase is the current lifecycle phase.
	Phase ProposalPhase `json:"phase,omitempty"`

	// ConfidenceScore is 0-100; higher means the policy is safer to enforce.
	ConfidenceScore int `json:"confidenceScore,omitempty"`

	// RiskLevel is the overall risk of the generated policy.
	RiskLevel RiskLevel `json:"riskLevel,omitempty"`

	// PolicyConfigMapRef is the name of the ConfigMap holding data["policy.yaml"].
	PolicyConfigMapRef string `json:"policyConfigMapRef,omitempty"`

	// ExcludedDestinations lists destinations explicitly excluded from the policy.
	ExcludedDestinations []ExcludedDestination `json:"excludedDestinations,omitempty"`

	// Conditions holds standard Kubernetes conditions.
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// Message holds a human-readable explanation of the current phase.
	Message string `json:"message,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status
//+kubebuilder:resource:scope=Namespaced,shortName=epp
//+kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
//+kubebuilder:printcolumn:name="Score",type=integer,JSONPath=`.status.confidenceScore`
//+kubebuilder:printcolumn:name="Risk",type=string,JSONPath=`.status.riskLevel`
//+kubebuilder:printcolumn:name="Approved",type=boolean,JSONPath=`.spec.approval.approved`
//+kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// EgressPolicyProposal is the Schema for the egresspolicyproposals API.
type EgressPolicyProposal struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   EgressPolicyProposalSpec   `json:"spec,omitempty"`
	Status EgressPolicyProposalStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// EgressPolicyProposalList contains a list of EgressPolicyProposal.
type EgressPolicyProposalList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []EgressPolicyProposal `json:"items"`
}

func init() {
	SchemeBuilder.Register(&EgressPolicyProposal{}, &EgressPolicyProposalList{})
}
