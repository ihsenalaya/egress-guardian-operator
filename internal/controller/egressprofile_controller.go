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
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	egressv1alpha1 "github.com/ihsen/egress-guardian-operator/api/v1alpha1"
	"github.com/ihsen/egress-guardian-operator/internal/observer"
	"github.com/ihsen/egress-guardian-operator/internal/store"
)

// EgressProfileReconciler reconciles EgressProfile objects.
type EgressProfileReconciler struct {
	client.Client
	Scheme      *runtime.Scheme
	Recorder    record.EventRecorder
	Accumulator *observer.Accumulator
	Store       *store.ConfigMapStore
}

//+kubebuilder:rbac:groups=egress.platform.io,resources=egressprofiles,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=egress.platform.io,resources=egressprofiles/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=egress.platform.io,resources=egressprofiles/finalizers,verbs=update
//+kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups="",resources=events,verbs=create;patch
//+kubebuilder:rbac:groups=apps,resources=deployments;replicasets;statefulsets;daemonsets,verbs=get;list;watch
//+kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch

func (r *EgressProfileReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	profile := &egressv1alpha1.EgressProfile{}
	if err := r.Get(ctx, req.NamespacedName, profile); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Ensure the baseline ConfigMap exists (owned by this profile).
	if err := r.Store.EnsureConfigMap(ctx, profile); err != nil {
		logger.Error(err, "failed to ensure baseline ConfigMap")
		return ctrl.Result{RequeueAfter: 10 * time.Second}, err
	}

	cmName := store.ConfigMapName(profile.Name)

	// Register the workload with the accumulator so it routes flows correctly.
	workloadKey := fmt.Sprintf("%s/%s/%s",
		profile.Namespace,
		profile.Spec.TargetRef.Kind,
		profile.Spec.TargetRef.Name,
	)
	if r.Accumulator != nil {
		r.Accumulator.RegisterWorkload(workloadKey, profile.Namespace, cmName)
	}

	// Read current snapshot to fill status counters.
	snap, err := r.Store.GetSnapshot(ctx, profile.Namespace, cmName)
	if err != nil {
		logger.Error(err, "failed to read baseline snapshot")
		snap = &observer.Snapshot{}
	}

	// Determine phase.
	phase := egressv1alpha1.PhaseObserving
	proposalRef := profile.Status.ProposalRef

	switch profile.Spec.Mode {
	case egressv1alpha1.ModeSuggest, egressv1alpha1.ModeEnforce:
		proposal, err := r.ensureProposal(ctx, profile)
		if err != nil {
			logger.Error(err, "failed to ensure EgressPolicyProposal")
			return ctrl.Result{RequeueAfter: 15 * time.Second}, err
		}
		proposalRef = proposal.Name
		switch proposal.Status.Phase {
		case egressv1alpha1.ProposalPhaseApplied:
			phase = egressv1alpha1.PhaseEnforced
		case egressv1alpha1.ProposalPhasePendingApproval, egressv1alpha1.ProposalPhaseApproved:
			if profile.Spec.Mode == egressv1alpha1.ModeEnforce {
				phase = egressv1alpha1.PhaseAwaitingApproval
			} else {
				phase = egressv1alpha1.PhaseProposed
			}
		default:
			phase = egressv1alpha1.PhaseProposed
		}
	}

	// Build risk summary from snapshot.
	low, medium, high := countRisk(snap)

	// Update status.
	now := metav1.Now()
	patch := client.MergeFrom(profile.DeepCopy())
	profile.Status.Phase = phase
	profile.Status.ObservedDestinationsCount = len(snap.Destinations)
	profile.Status.Truncated = snap.Truncated
	profile.Status.LastObservationTime = &now
	profile.Status.BaselineConfigMapRef = cmName
	profile.Status.ProposalRef = proposalRef
	profile.Status.RiskSummary = egressv1alpha1.RiskSummary{Low: low, Medium: medium, High: high}
	if high > 0 {
		profile.Status.Message = fmt.Sprintf("%d destination(s) High exclue(s) de la proposition.", high)
	}

	setCondition(&profile.Status.Conditions, metav1.Condition{
		Type:               "Ready",
		Status:             metav1.ConditionTrue,
		Reason:             "Reconciled",
		Message:            string(phase),
		LastTransitionTime: now,
	})

	if err := r.Status().Patch(ctx, profile, patch); err != nil {
		logger.Error(err, "failed to patch status")
		return ctrl.Result{RequeueAfter: 10 * time.Second}, err
	}

	logger.Info("reconciled EgressProfile", "phase", phase, "destinations", len(snap.Destinations))
	return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
}

// ensureProposal creates an EgressPolicyProposal for this profile if it does not exist.
func (r *EgressProfileReconciler) ensureProposal(ctx context.Context, profile *egressv1alpha1.EgressProfile) (*egressv1alpha1.EgressPolicyProposal, error) {
	proposalName := fmt.Sprintf("%s-proposal", profile.Name)
	proposal := &egressv1alpha1.EgressPolicyProposal{}
	err := r.Get(ctx, client.ObjectKey{Namespace: profile.Namespace, Name: proposalName}, proposal)
	if err == nil {
		return proposal, nil
	}
	if !errors.IsNotFound(err) {
		return nil, err
	}

	policyName := profile.Spec.Policy.GeneratedPolicyName
	if policyName == "" {
		policyName = fmt.Sprintf("%s-egress-allowlist", profile.Name)
	}

	proposal = &egressv1alpha1.EgressPolicyProposal{
		ObjectMeta: metav1.ObjectMeta{
			Name:      proposalName,
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
		Spec: egressv1alpha1.EgressPolicyProposalSpec{
			ProfileRef: egressv1alpha1.ProfileRef{Name: profile.Name},
			GeneratedPolicy: egressv1alpha1.GeneratedPolicyRef{
				Type: "CiliumNetworkPolicy",
				Name: policyName,
			},
		},
	}

	if err := r.Create(ctx, proposal); err != nil {
		return nil, fmt.Errorf("create EgressPolicyProposal: %w", err)
	}
	r.Recorder.Eventf(profile, corev1.EventTypeNormal, "ProposalCreated",
		"Created EgressPolicyProposal %s", proposalName)
	return proposal, nil
}

func countRisk(snap *observer.Snapshot) (low, medium, high int) {
	for _, d := range snap.Destinations {
		switch {
		case d.DestFQDN == "" && isPublicIPSimple(d.DestIP):
			high++
		case d.FlowCount >= 5 && d.Snapshots >= 2:
			low++
		default:
			medium++
		}
	}
	return
}

func isPublicIPSimple(ip string) bool {
	if ip == "" {
		return false
	}
	privPrefixes := []string{"10.", "172.", "192.168.", "127.", "::1"}
	for _, p := range privPrefixes {
		if len(ip) >= len(p) && ip[:len(p)] == p {
			return false
		}
	}
	return true
}

func setCondition(conditions *[]metav1.Condition, cond metav1.Condition) {
	for i, c := range *conditions {
		if c.Type == cond.Type {
			(*conditions)[i] = cond
			return
		}
	}
	*conditions = append(*conditions, cond)
}

func boolPtr(b bool) *bool { return &b }

// SetupWithManager sets up the controller with the Manager.
func (r *EgressProfileReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&egressv1alpha1.EgressProfile{}).
		Owns(&egressv1alpha1.EgressPolicyProposal{}).
		Complete(r)
}
