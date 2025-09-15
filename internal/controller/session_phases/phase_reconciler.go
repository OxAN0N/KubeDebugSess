package session_phases

import (
	"context"

	debugv1alpha1 "github.com/OxAN0N/KubeDebugSess/api/v1alpha1"
	"k8s.io/client-go/kubernetes"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// PhaseReconciler defines the interface for handling a specific SessionPhase.
type PhaseReconciler interface {
	// Reconcile handles the logic for a specific phase.
	Reconcile(ctx context.Context, session *debugv1alpha1.DebugSession) (ctrl.Result, error)
}

type PhaseReconcilerFactory func(client client.Client, cs kubernetes.Interface) PhaseReconciler

var reconcilerRegistry = make(map[debugv1alpha1.SessionPhase]PhaseReconcilerFactory)

func Register(phase debugv1alpha1.SessionPhase, factory PhaseReconcilerFactory) {
	reconcilerRegistry[phase] = factory
}

func GetReconcilers(client client.Client, cs kubernetes.Interface) map[debugv1alpha1.SessionPhase]PhaseReconciler {
	reconcilers := make(map[debugv1alpha1.SessionPhase]PhaseReconciler)
	for phase, factory := range reconcilerRegistry {
		reconcilers[phase] = factory(client, cs)
	}
	return reconcilers
}

func UpdateSessionStatus(ctx context.Context, c client.Client, session *debugv1alpha1.DebugSession, newPhase debugv1alpha1.SessionPhase, message string) (reconcile.Result, error) {
	logger := log.FromContext(ctx)

	session.Status.Phase = newPhase
	session.Status.Message = message

	if err := c.Status().Update(ctx, session); err != nil {
		logger.Error(err, "Failed to update DebugSession status", "targetPhase", newPhase)
		return reconcile.Result{}, err
	}

	logger.Info("Successfully updated session status", "newPhase", newPhase)
	return reconcile.Result{}, nil
}
