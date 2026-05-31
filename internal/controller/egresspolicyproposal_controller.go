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

package controller

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	egressv1alpha1 "github.com/ihsen/egress-guardian-operator/api/v1alpha1"
	ciliumgen "github.com/ihsen/egress-guardian-operator/internal/cilium"
	"github.com/ihsen/egress-guardian-operator/internal/gitops"
	"github.com/ihsen/egress-guardian-operator/internal/store"
)

// EgressPolicyProposalReconciler reconciles EgressPolicyProposal objects.
type EgressPolicyProposalReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
	Store    *store.ConfigMapStore
}

//+kubebuilder:rbac:groups=egress.platform.io,resources=egresspolicyproposals,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=egress.platform.io,resources=egresspolicyproposals/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=egress.platform.io,resources=egresspolicyproposals/finalizers,verbs=update
//+kubebuilder:rbac:groups=cilium.io,resources=ciliumnetworkpolicies,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups="",resources=events,verbs=create;patch

func (r *EgressPolicyProposalReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	proposal := &egressv1alpha1.EgressPolicyProposal{}
	if err := r.Get(ctx, req.NamespacedName, proposal); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Fetch the owning EgressProfile.
	profile := &egressv1alpha1.EgressProfile{}
	if err := r.Get(ctx, client.ObjectKey{
		Namespace: proposal.Namespace,
		Name:      proposal.Spec.ProfileRef.Name,
	}, profile); err != nil {
		logger.Error(err, "failed to get owning EgressProfile")
		return ctrl.Result{RequeueAfter: 15 * time.Second}, err
	}

	// Read baseline snapshot from the ConfigMap.
	cmName := store.ConfigMapName(profile.Name)
	snap, err := r.Store.GetSnapshot(ctx, proposal.Namespace, cmName)
	if err != nil {
		logger.Error(err, "failed to read baseline snapshot")
		return ctrl.Result{RequeueAfter: 15 * time.Second}, err
	}

	// Generate the CiliumNetworkPolicy.
	result := ciliumgen.Generate(profile.Name, proposal.Namespace, profile.Spec.Policy, snap)

	// Persist YAML to ConfigMap (never to status).
	if err := r.Store.SavePolicyYAML(ctx, proposal.Namespace, cmName, result.YAML); err != nil {
		logger.Error(err, "failed to save policy YAML to ConfigMap")
		return ctrl.Result{RequeueAfter: 10 * time.Second}, err
	}

	// GitOps export.
	if profile.Spec.GitOps.Enabled && profile.Spec.GitOps.OutputPath != "" {
		policyName := proposal.Spec.GeneratedPolicy.Name
		if policyName == "" {
			policyName = fmt.Sprintf("%s-egress-allowlist", profile.Name)
		}
		if err := gitops.ExportPolicy(profile.Spec.GitOps.OutputPath, policyName, result.YAML); err != nil {
			logger.Error(err, "failed to export policy YAML for GitOps")
		}
	}

	// Determine proposal phase.
	phase := egressv1alpha1.ProposalPhasePendingApproval

	// Apply CNP only when mode=Enforce AND approval.approved=true.
	if profile.Spec.Mode == egressv1alpha1.ModeEnforce {
		if proposal.Spec.Approval.Approved {
			phase = egressv1alpha1.ProposalPhaseApproved
			if err := r.applyCNP(ctx, proposal, profile, result.CNP); err != nil {
				logger.Error(err, "failed to apply CiliumNetworkPolicy")
				phase = egressv1alpha1.ProposalPhaseFailed
			} else {
				phase = egressv1alpha1.ProposalPhaseApplied
			}
		} else {
			phase = egressv1alpha1.ProposalPhasePendingApproval
		}
	}

	// Patch proposal status.
	now := metav1.Now()
	patch := client.MergeFrom(proposal.DeepCopy())
	proposal.Status.Phase = phase
	proposal.Status.ConfidenceScore = result.ConfidenceScore
	proposal.Status.RiskLevel = result.RiskLevel
	proposal.Status.PolicyConfigMapRef = cmName
	proposal.Status.ExcludedDestinations = result.ExcludedDestinations
	proposal.Status.Message = fmt.Sprintf("score=%d risk=%s excluded=%d",
		result.ConfidenceScore, result.RiskLevel, len(result.ExcludedDestinations))

	setCondition(&proposal.Status.Conditions, metav1.Condition{
		Type:               "Ready",
		Status:             metav1.ConditionTrue,
		Reason:             string(phase),
		Message:            proposal.Status.Message,
		LastTransitionTime: now,
	})

	if err := r.Status().Patch(ctx, proposal, patch); err != nil {
		logger.Error(err, "failed to patch proposal status")
		return ctrl.Result{RequeueAfter: 10 * time.Second}, err
	}

	logger.Info("reconciled EgressPolicyProposal",
		"phase", phase,
		"score", result.ConfidenceScore,
		"excluded", len(result.ExcludedDestinations))
	return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
}

// applyCNP creates or updates the CiliumNetworkPolicy in the cluster.
// It emits a Warning event to make the default-deny egress effect visible.
func (r *EgressPolicyProposalReconciler) applyCNP(
	ctx context.Context,
	proposal *egressv1alpha1.EgressPolicyProposal,
	profile *egressv1alpha1.EgressProfile,
	cnp *unstructured.Unstructured,
) error {
	logger := log.FromContext(ctx)

	// Set owner reference so CNP is GC'd with the proposal.
	cnp.SetOwnerReferences([]metav1.OwnerReference{
		{
			APIVersion:         egressv1alpha1.GroupVersion.String(),
			Kind:               "EgressPolicyProposal",
			Name:               proposal.Name,
			UID:                proposal.UID,
			BlockOwnerDeletion: boolPtr(true),
			Controller:         boolPtr(true),
		},
	})

	existing := &unstructured.Unstructured{}
	existing.SetGroupVersionKind(ciliumgen.CiliumNetworkPolicyGVK)
	err := r.Get(ctx, client.ObjectKey{
		Namespace: cnp.GetNamespace(),
		Name:      cnp.GetName(),
	}, existing)

	if errors.IsNotFound(err) {
		if err := r.Create(ctx, cnp); err != nil {
			return fmt.Errorf("create CNP: %w", err)
		}
	} else if err != nil {
		return fmt.Errorf("get CNP: %w", err)
	} else {
		cnp.SetResourceVersion(existing.GetResourceVersion())
		if err := r.Update(ctx, cnp); err != nil {
			return fmt.Errorf("update CNP: %w", err)
		}
	}

	// Emit Warning: applying this policy causes default-deny egress.
	r.Recorder.Eventf(proposal, corev1.EventTypeWarning, "DefaultDenyEgress",
		"CiliumNetworkPolicy %s applied — workload %s/%s now operates under DEFAULT-DENY egress. "+
			"All traffic not in the allowlist (including DNS if allowDNS=false) will be dropped.",
		cnp.GetName(), profile.Namespace, profile.Spec.TargetRef.Name)

	logger.Info("CiliumNetworkPolicy applied", "name", cnp.GetName(), "namespace", cnp.GetNamespace())
	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *EgressPolicyProposalReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&egressv1alpha1.EgressPolicyProposal{}).
		Complete(r)
}
