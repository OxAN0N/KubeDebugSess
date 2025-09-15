package reconcilers

import (
	"context"
	"fmt"
	"time"

	debugv1alpha1 "github.com/OxAN0N/KubeDebugSess/api/v1alpha1"
	"github.com/OxAN0N/KubeDebugSess/internal/controller/session_phases"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// ActionHandler는 ReasonAction에 따라 실행될 함수의 타입을 정의합니다.
type ActionHandler func(context.Context, *debugv1alpha1.DebugSession, string) (ctrl.Result, error)

func init() {
	session_phases.Register(debugv1alpha1.Active, NewActiveReconciler)
}

func NewActiveReconciler(client client.Client, cs kubernetes.Interface) session_phases.PhaseReconciler {
	r := &ActiveReconciler{
		Client:    client,
		ClientSet: cs,
	}
	// TODO: Refactor for OCP
	r.actionHandlers = map[session_phases.ReasonAction]ActionHandler{
		session_phases.ActionRetry:   r.handleRetry,
		session_phases.ActionFail:    r.handleFail,
		session_phases.ActionSucceed: r.handleSucceed,
		session_phases.ActionWait:    r.handleWait,
	}
	return r
}

type ActiveReconciler struct {
	client.Client
	ClientSet      kubernetes.Interface
	actionHandlers map[session_phases.ReasonAction]ActionHandler
}

func (r *ActiveReconciler) Reconcile(ctx context.Context, session *debugv1alpha1.DebugSession) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	logger.Info("Active Reconciler Started")
	if session.Spec.TargetNamespace == "" {
		session.Spec.TargetNamespace = session.Namespace
	}

	pod := &corev1.Pod{}
	podKey := types.NamespacedName{Name: session.Spec.TargetPodName, Namespace: session.Spec.TargetNamespace}
	if err := r.Get(ctx, podKey, pod); err != nil {
		if errors.IsNotFound(err) {
			return session_phases.UpdateSessionStatus(ctx, r.Client, session, debugv1alpha1.Failed, "Target pod not found.")
		}
	}

	debuggerContainerName := fmt.Sprintf("debugger-%s", session.UID)

	if len(pod.Status.EphemeralContainerStatuses) > 0 {
		for _, containerStatus := range pod.Status.EphemeralContainerStatuses {
			if containerStatus.Name == debuggerContainerName {
				action, message := session_phases.AnalyzeContainerStatus(containerStatus)

				if handler, ok := r.actionHandlers[action]; ok {
					return handler(ctx, session, message)
				}

				logger.Info("No handler defined for action, waiting.", "action", action)
				return ctrl.Result{}, nil
			}
		}
	}

	for _, container := range pod.Spec.EphemeralContainers {
		if container.Name == debuggerContainerName {
			logger.Info("Ephemeral Container Statuses are not updated yet")
			return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
		}

	}

	logger.Info("Can not find EphemeralContainerStatues")
	return session_phases.UpdateSessionStatus(ctx, r.Client, session, debugv1alpha1.Failed, "Debugger container not found.")
}

// handleRetry는 재시도가 필요한 상태를 처리합니다.
func (r *ActiveReconciler) handleRetry(ctx context.Context, session *debugv1alpha1.DebugSession, message string) (ctrl.Result, error) {
	session.Status.RetryCount = 1 // 재시도 카운트 시작
	return session_phases.UpdateSessionStatus(ctx, r.Client, session, debugv1alpha1.Retrying, message)
}

// handleFail은 즉시 실패가 필요한 상태를 처리합니다.
func (r *ActiveReconciler) handleFail(ctx context.Context, session *debugv1alpha1.DebugSession, message string) (ctrl.Result, error) {
	return session_phases.UpdateSessionStatus(ctx, r.Client, session, debugv1alpha1.Failed, message)
}

// handleSucceed는 성공적으로 완료된 상태를 처리합니다.
func (r *ActiveReconciler) handleSucceed(ctx context.Context, session *debugv1alpha1.DebugSession, message string) (ctrl.Result, error) {
	return session_phases.UpdateSessionStatus(ctx, r.Client, session, debugv1alpha1.Terminating, message)
}

// handleWait은 정상 대기가 필요한 상태를 처리합니다.
func (r *ActiveReconciler) handleWait(ctx context.Context, session *debugv1alpha1.DebugSession, message string) (ctrl.Result, error) {
	// 정상 상태이므로 아무것도 하지 않고 다음 Reconcile을 기다립니다.
	return ctrl.Result{}, nil
}
