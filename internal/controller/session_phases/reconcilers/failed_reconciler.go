package reconcilers

import (
	"context"

	debugv1alpha1 "github.com/OxAN0N/KubeDebugSess/api/v1alpha1"
	"github.com/OxAN0N/KubeDebugSess/internal/controller/session_phases"
	"k8s.io/client-go/kubernetes"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func init() {
	session_phases.Register(debugv1alpha1.Failed, NewFailedReconciler)
}

func NewFailedReconciler(client client.Client, cs kubernetes.Interface) session_phases.PhaseReconciler {
	return &FailedReconciler{Client: client, ClientSet: cs}
}

type FailedReconciler struct {
	client.Client
	ClientSet kubernetes.Interface
}

func (r *FailedReconciler) Reconcile(ctx context.Context, session *debugv1alpha1.DebugSession) (ctrl.Result, error) {
	// TOOD: implement alert to admin or slack
	return ctrl.Result{}, nil
}
