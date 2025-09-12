package reconcilers

import (
	"context"
	default_errors "errors"
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

func init() {
	session_phases.Register(debugv1alpha1.Pending, NewPendingReconciler)
	session_phases.Register("", NewPendingReconciler)
}

func NewPendingReconciler(client client.Client, cs kubernetes.Interface) session_phases.PhaseReconciler {
	return &PendingReconciler{Client: client, Clientset: cs}
}

type PendingReconciler struct {
	client.Client
	Clientset kubernetes.Interface
}

func (r *PendingReconciler) Reconcile(ctx context.Context, session *debugv1alpha1.DebugSession) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// 시나리오 1: 세션이 처음 생성되었는가? -> Pending 상태로 초기화한다.
	if session.Status.Phase == "" {
		logger.Info("New session found, initializing to Pending.")
		return session_phases.UpdateSessionStatus(ctx, r.Client, session, debugv1alpha1.Pending, "DebugSession created.")
	}

	// 시나리오 2: 전제 조건(네임스페이스, 파드, 컨테이너 상태)이 모두 만족되었는가?
	logger.Info("Validating prerequisites for the session.")

	err := r.validatePrerequisites(ctx, session)
	if err != nil {
		var requeueErr *session_phases.RequeueError // 커스텀 에러 타입을 받을 변수 선언

		// errors.As를 사용해 err가 RequeueError 타입인지 확인합니다.
		if default_errors.As(err, &requeueErr) {
			logger.Info("Pod is not ready yet, requeueing.", "reason", requeueErr.Reason)
			return ctrl.Result{RequeueAfter: requeueErr.RequeueAfter}, nil
		}

		// 그 외의 유효성 검사 실패는 Failed 상태로 변경
		logger.Info("Prerequisite validation failed.")
		return session_phases.UpdateSessionStatus(ctx, r.Client, session, debugv1alpha1.Failed, err.Error())
	}

	// 시나리오 3: 모든 조건을 만족했는가? -> 다음 단계(Injecting)로 넘어간다.
	logger.Info("All prerequisites are satisfied. Transitioning to the next phase.")
	return session_phases.UpdateSessionStatus(ctx, r.Client, session, debugv1alpha1.Injecting, "Prerequisites validated successfully.")
}

// validatePrerequisites는 디버그 세션 주입에 필요한 모든 전제 조건들을 검사합니다.
// 모든 조건이 충족되면 nil을 반환합니다.
// 조건이 충족되지 않으면, 실패 원인을 담은 에러를 반환합니다.
func (r *PendingReconciler) validatePrerequisites(ctx context.Context, session *debugv1alpha1.DebugSession) error {

	if session.Spec.TargetNamespace == "" {
		session.Spec.TargetNamespace = session.Namespace
	}

	// 1. Namespace 검사
	namespace := &corev1.Namespace{}
	namespaceKey := types.NamespacedName{Name: session.Spec.TargetNamespace}
	if err := r.Get(ctx, namespaceKey, namespace); err != nil {
		if errors.IsNotFound(err) {
			return fmt.Errorf("target namespace '%s' not found", session.Spec.TargetNamespace)
		}
		return err
	}

	// 2. Pod 검사
	pod := &corev1.Pod{}
	podKey := types.NamespacedName{Name: session.Spec.TargetPodName, Namespace: session.Spec.TargetNamespace}
	if err := r.Get(ctx, podKey, pod); err != nil {
		if errors.IsNotFound(err) {
			return fmt.Errorf("target pod '%s' not found", session.Spec.TargetPodName)
		}
		return err
	}

	// 3. Pod 상태 검사
	if pod.Status.Phase != corev1.PodRunning {
		if pod.Status.Phase == corev1.PodSucceeded || pod.Status.Phase == corev1.PodFailed {
			return fmt.Errorf("target pod is not running (current phase: %s)", pod.Status.Phase)
		}
		return &session_phases.RequeueError{
			Reason:       fmt.Sprintf("pod is not running yet (current phase: %s)", pod.Status.Phase),
			RequeueAfter: 30 * time.Second,
		}
	}

	if session.Spec.TargetContainerName == "" {
		if len(pod.Spec.Containers) > 0 {
			session.Spec.TargetContainerName = pod.Spec.Containers[0].Name
			log.FromContext(ctx).Info("TargetContainerName defaulted to first container", "containerName", session.Spec.TargetContainerName)
		} else {
			return fmt.Errorf("cannot default container name, pod has no containers")
		}
	}

	// 4. Container 검사
	if !findContainerInPod(pod, session.Spec.TargetContainerName) {
		return fmt.Errorf("target container '%s' not found in pod", session.Spec.TargetContainerName)
	}

	return nil
}

func findContainerInPod(pod *corev1.Pod, containerName string) bool {
	for _, container := range pod.Spec.Containers {
		if container.Name == containerName {
			return true
		}
	}
	return false
}
