package reconcilers

import (
	"context"
	"fmt"

	debugv1alpha1 "github.com/OxAN0N/KubeDebugSess/api/v1alpha1"
	"github.com/OxAN0N/KubeDebugSess/internal/controller/session_phases"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

func init() {
	session_phases.Register(debugv1alpha1.Injecting, NewInjectingReconciler)
}

func NewInjectingReconciler(c client.Client, cs kubernetes.Interface) session_phases.PhaseReconciler {
	return &InjectingReconciler{
		Client:    c,
		ClientSet: cs,
	}
}

type InjectingReconciler struct {
	client.Client
	ClientSet kubernetes.Interface
}

func (r *InjectingReconciler) Reconcile(ctx context.Context, session *debugv1alpha1.DebugSession) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	logger.Info("Injection Started")

	// TODO: Injection Logic Change -> Check Successfully Injected
	if err := r.injectEphemeralContainer(ctx, session); err != nil {
		return session_phases.UpdateSessionStatus(ctx, r.Client, session,
			debugv1alpha1.Failed, fmt.Sprintf("Inject Failed: %v", err))
	}
	return session_phases.UpdateSessionStatus(ctx, r.Client, session, debugv1alpha1.Active, "Injection Succeded.")
}

func (r *InjectingReconciler) injectEphemeralContainer(ctx context.Context, session *debugv1alpha1.DebugSession) error {
	if session.Spec.TargetNamespace == "" {
		session.Spec.TargetNamespace = session.Namespace
	}

	podName := session.Spec.TargetPodName
	pod := &corev1.Pod{}

	if err := r.Get(ctx, types.NamespacedName{
		Name:      podName,
		Namespace: session.Spec.TargetNamespace,
	}, pod); err != nil {
		return fmt.Errorf("failed to find target Pod: %w", err)
	}

	ec := corev1.EphemeralContainer{
		EphemeralContainerCommon: corev1.EphemeralContainerCommon{
			Name:    fmt.Sprintf("debugger-%s", session.UID),
			Image:   session.Spec.DebuggerImage,
			Command: []string{"/bin/sh"},
			Stdin:   true,
			TTY:     true,
		},
	}

	pod.Spec.EphemeralContainers = append(pod.Spec.EphemeralContainers, ec)

	_, err := r.ClientSet.CoreV1().
		Pods(session.Spec.TargetNamespace).
		UpdateEphemeralContainers(ctx, podName, pod, metav1.UpdateOptions{})

	if err != nil {
		return fmt.Errorf("failed to update ephemeral containers: %w", err)
	}
	return nil
}
