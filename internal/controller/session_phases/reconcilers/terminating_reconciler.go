package reconcilers

import (
	"context"
	"fmt"
	"time"

	debugv1alpha1 "github.com/OxAN0N/KubeDebugSess/api/v1alpha1"
	"github.com/OxAN0N/KubeDebugSess/internal/controller/session_phases"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// TerminatingReconciler는 Terminating 상태의 DebugSession을 처리합니다.
type TerminatingReconciler struct {
	client.Client
	ClientSet kubernetes.Interface
}

func init() {
	session_phases.Register(debugv1alpha1.Terminating, NewTerminatingReconciler)
}

func NewTerminatingReconciler(c client.Client, cs kubernetes.Interface) session_phases.PhaseReconciler {
	return &TerminatingReconciler{
		Client:    c,
		ClientSet: cs,
	}
}

// Reconcile은 디버깅 컨테이너를 제거하는 정리 로직을 수행합니다.
func (r *TerminatingReconciler) Reconcile(ctx context.Context, session *debugv1alpha1.DebugSession) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.Info("Starting cleanup for Terminating session.")

	if err := r.cleanupEphemeralContainer(ctx, session); err != nil {
		logger.Error(err, "Failed to cleanup ephemeral container.")
		return session_phases.UpdateSessionStatus(ctx, r.Client, session, debugv1alpha1.Failed, err.Error())
	}

	// 2. 정리 작업이 성공하면, Completed 상태로 전환합니다.
	logger.Info("Successfully removed ephemeral container. Transitioning to Completed.")
	now := metav1.NewTime(time.Now())
	session.Status.TerminationTime = &now

	return session_phases.UpdateSessionStatus(ctx, r.Client, session, debugv1alpha1.Completed, "Termination Completed")
}

// cleanupEphemeralContainer는 Pod에서 Ephemeral Container를 제거하는 로직을 담당합니다.
func (r *TerminatingReconciler) cleanupEphemeralContainer(ctx context.Context, session *debugv1alpha1.DebugSession) error {

	if session.Spec.TargetNamespace == "" {
		session.Spec.TargetNamespace = session.Namespace
	}

	pod := &corev1.Pod{}
	podKey := types.NamespacedName{Name: session.Spec.TargetPodName, Namespace: session.Spec.TargetNamespace}
	err := r.Get(ctx, podKey, pod)
	if err != nil {
		if errors.IsNotFound(err) {
			return fmt.Errorf("target pod '%s' not found, the session has failed", podKey.Name)
		}
		return fmt.Errorf("failed to get target pod for cleanup: %w", err)
	}

	debuggerName := fmt.Sprintf("debugger-%s", session.UID)
	foundIndex := -1
	for i, ec := range pod.Spec.EphemeralContainers {
		if ec.Name == debuggerName {
			foundIndex = i
			break
		}
	}

	if foundIndex == -1 {
		return fmt.Errorf("debugger container '%s' not found in pod '%s' during cleanup, the session has failed unexpectedly", debuggerName, pod.Name)
	}

	pod.Spec.EphemeralContainers = append(pod.Spec.EphemeralContainers[:foundIndex], pod.Spec.EphemeralContainers[foundIndex+1:]...)

	_, err = r.ClientSet.CoreV1().
		Pods(pod.Namespace).
		UpdateEphemeralContainers(ctx, pod.Name, pod, metav1.UpdateOptions{})

	if err != nil {
		return fmt.Errorf("failed to update ephemeral containers to remove debugger: %w", err)
	}

	return nil
}
