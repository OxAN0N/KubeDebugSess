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
	session_phases.Register(debugv1alpha1.Completed, NewCompletedReconciler)
}

func NewCompletedReconciler(client client.Client, cs kubernetes.Interface) session_phases.PhaseReconciler {
	return &CompletedReconciler{Client: client, ClientSet: cs}
}

type CompletedReconciler struct {
	client.Client
	ClientSet kubernetes.Interface
}

func (r *CompletedReconciler) Reconcile(ctx context.Context, session *debugv1alpha1.DebugSession) (ctrl.Result, error) {
	// TODO: implement alert for slack or other messengers
	// to manually delete the DebugSession CRD on GitOps
	session.Status.Message = "Session Completed."
	if err := r.Status().Update(ctx, session); err != nil {
		return session_phases.UpdateSessionStatus(ctx, r.Client, session, debugv1alpha1.Failed, err.Error())
	}
	return ctrl.Result{}, nil
}
