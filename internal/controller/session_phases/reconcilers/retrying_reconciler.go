package reconcilers

import (
	"context"
	"fmt"
	"time"

	debugv1alpha1 "github.com/OxAN0N/KubeDebugSess/api/v1alpha1"
	"github.com/OxAN0N/KubeDebugSess/internal/controller/session_phases"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// RetryingReconciler는 Retrying 상태의 DebugSession을 처리합니다.
type RetryingReconciler struct {
	client.Client
	ClientSet      kubernetes.Interface
	actionHandlers map[session_phases.ReasonAction]ActionHandler // Action별 핸들러 함수를 저장하는 맵
}

func init() {
	session_phases.Register(debugv1alpha1.Retrying, NewRetryingReconciler)
}

func NewRetryingReconciler(c client.Client, cs kubernetes.Interface) session_phases.PhaseReconciler {
	r := &RetryingReconciler{
		Client:    c,
		ClientSet: cs,
	}
	// TODO: Refactor for OCP
	r.actionHandlers = map[session_phases.ReasonAction]ActionHandler{
		session_phases.ActionWait:    r.handleResolved,
		session_phases.ActionSucceed: r.handleResolved,
		session_phases.ActionFail:    r.handleFail,
		session_phases.ActionRetry:   r.handleRetry,
	}
	return r
}

// Reconcile은 Retrying 상태의 주 로직을 담당합니다.
func (r *RetryingReconciler) Reconcile(ctx context.Context, session *debugv1alpha1.DebugSession) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	if session.Spec.TargetNamespace == "" {
		session.Spec.TargetNamespace = session.Namespace
	}

	// 1. Pod 상태를 다시 확인합니다.
	pod := &corev1.Pod{}
	podKey := types.NamespacedName{Name: session.Spec.TargetPodName, Namespace: session.Spec.TargetNamespace}
	if err := r.Get(ctx, podKey, pod); err != nil {
		return session_phases.UpdateSessionStatus(ctx, r.Client, session, debugv1alpha1.Failed, "Target pod not found during retry.")
	}

	// 2. 디버깅 컨테이너의 상태를 분석합니다.
	debuggerContainerName := fmt.Sprintf("debugger-%s", session.UID)
	for _, cs := range pod.Status.EphemeralContainerStatuses {
		if cs.Name == debuggerContainerName {
			action, message := session_phases.AnalyzeContainerStatus(cs)

			// 3. 분석된 Action에 맞는 핸들러를 동적으로 호출합니다.
			if handler, ok := r.actionHandlers[action]; ok {
				return handler(ctx, session, message)
			}

			// 핸들러가 정의되지 않은 경우, 재시도를 계속합니다.
			logger.Info("No handler defined for action, continuing retry.", "action", action)
			return r.handleRetry(ctx, session, "unknown reason")
		}
	}

	return session_phases.UpdateSessionStatus(ctx, r.Client, session, debugv1alpha1.Failed, "Debugger container disappeared during retry.")
}

// --- Action별 핸들러 함수들 ---

// handleResolved는 문제가 해결된 상태를 처리합니다.
func (r *RetryingReconciler) handleResolved(ctx context.Context, session *debugv1alpha1.DebugSession, message string) (ctrl.Result, error) {
	log.FromContext(ctx).Info("Problem resolved during retry. Transitioning to Active.", "reason", message)
	session.Status.RetryCount = 0
	return session_phases.UpdateSessionStatus(ctx, r.Client, session, debugv1alpha1.Active, "Session is now active.")
}

// handleFail은 복구 불가능한 실패 상태를 처리합니다.
func (r *RetryingReconciler) handleFail(ctx context.Context, session *debugv1alpha1.DebugSession, message string) (ctrl.Result, error) {
	log.FromContext(ctx).Info("Unrecoverable error occurred during retry. Transitioning to Failed.", "reason", message)
	return session_phases.UpdateSessionStatus(ctx, r.Client, session, debugv1alpha1.Failed, message)
}

// handleRetry는 문제가 지속되어 재시도가 필요한 상태를 처리합니다.
func (r *RetryingReconciler) handleRetry(ctx context.Context, session *debugv1alpha1.DebugSession, message string) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// 최대 재시도 횟수를 확인합니다.
	if int32(session.Status.RetryCount) >= session.Spec.MaxRetryCount {
		logger.Info("Max retries reached. Transitioning to Failed.", "MaxRetries", session.Spec.MaxRetryCount)
		return session_phases.UpdateSessionStatus(ctx, r.Client, session, debugv1alpha1.Failed, "Failed after max retries.")
	}

	// 재시도 횟수를 증가시키고 지수 백오프 대기 시간을 계산합니다.
	session.Status.RetryCount++
	waitDuration := time.Second * 5 * (1 << (session.Status.RetryCount - 1)) // 5s, 10s, 20s, 40s...
	if waitDuration > time.Minute {
		waitDuration = time.Minute // 최대 대기 시간은 1분으로 제한
	}

	logger.Info("Problem persists. Waiting for next retry.", "RetryCount", session.Status.RetryCount, "WaitDuration", waitDuration)

	session.Status.Message = fmt.Sprintf("Retrying... (%d/%d), Reason: %s", session.Status.RetryCount, session.Spec.MaxRetryCount, message)
	if err := r.Status().Update(ctx, session); err != nil {
		return ctrl.Result{}, err
	}

	// 계산된 시간 이후에 다시 Reconcile 하도록 예약합니다.
	return ctrl.Result{RequeueAfter: waitDuration}, nil
}
