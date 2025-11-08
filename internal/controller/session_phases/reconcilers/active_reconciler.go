package reconcilers

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
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

// ActionHandler is a function type for handling different container states.
type ActionHandler func(context.Context, *debugv1alpha1.DebugSession, string) (ctrl.Result, error)

func init() {
	session_phases.Register(debugv1alpha1.Active, NewActiveReconciler)
}

// NewActiveReconciler creates a new reconciler for the Active phase.
func NewActiveReconciler(client client.Client, cs kubernetes.Interface) session_phases.PhaseReconciler {
	r := &ActiveReconciler{
		Client:    client,
		Clientset: cs,
	}
	r.actionHandlers = map[session_phases.ReasonAction]ActionHandler{
		session_phases.ActionRetry:   r.handleRetry,
		session_phases.ActionFail:    r.handleFail,
		session_phases.ActionSucceed: r.handleSucceed,
		session_phases.ActionWait:    r.handleWait,
	}
	return r
}

// ActiveReconciler handles DebugSession resources in the Active phase.
type ActiveReconciler struct {
	client.Client
	Clientset      kubernetes.Interface
	actionHandlers map[session_phases.ReasonAction]ActionHandler
}

// Reconcile checks the ephemeral container status, generates a token when ready, and handles state transitions.
func (r *ActiveReconciler) Reconcile(ctx context.Context, session *debugv1alpha1.DebugSession) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	if session.Spec.TargetNamespace == "" {
		session.Spec.TargetNamespace = session.Namespace
	}

	pod := &corev1.Pod{}
	podKey := types.NamespacedName{Name: session.Spec.TargetPodName, Namespace: session.Spec.TargetNamespace}
	if err := r.Get(ctx, podKey, pod); err != nil {
		if errors.IsNotFound(err) {
			return session_phases.UpdateSessionStatus(ctx, r.Client, session, debugv1alpha1.Failed, "Target pod not found.")
		}
		return ctrl.Result{}, err
	}

	debuggerContainerName := fmt.Sprintf("debugger-%s", session.UID)
	session.Status.DebuggingContainerName = debuggerContainerName

	for _, containerStatus := range pod.Status.EphemeralContainerStatuses {
		if containerStatus.Name == debuggerContainerName {
			// If container is running and not yet ready, generate token and update status.
			if containerStatus.State.Running != nil && !session.Status.ReadyForAttach {
				logger.Info("Ephemeral container is running. Generating one-time session token.")

				token, err := generateSecureToken(32) // Generate a 32-byte (64-char hex) token
				if err != nil {
					logger.Error(err, "Failed to generate session token")
					return session_phases.UpdateSessionStatus(ctx, r.Client, session, debugv1alpha1.Failed, "Failed to generate token")
				}

				session.Status.ReadyForAttach = true
				session.Status.OneTimeToken = token
				session.Status.Message = buildConnectionString(session) // Build the user-friendly connection guide

				if err := r.Status().Update(ctx, session); err != nil {
					logger.Error(err, "Failed to update session status with token")
					return ctrl.Result{}, err
				}
				return ctrl.Result{}, nil
			}

			// Analyze the container status for further actions (retry, fail, etc.)
			action, message := session_phases.AnalyzeContainerStatus(containerStatus)
			if handler, ok := r.actionHandlers[action]; ok {
				if action != session_phases.ActionWait {
					session.Status.ReadyForAttach = false // No longer ready if not waiting
				}
				return handler(ctx, session, message)
			}
			return ctrl.Result{}, nil
		}
	}

	logger.Info("Ephemeral container status not found yet, requeueing.")
	return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
}

// buildConnectionString creates the two-step connection instructions for the user.
func buildConnectionString(session *debugv1alpha1.DebugSession) string {
	bastionHost := os.Getenv("BASTION_HOST")
	if bastionHost == "" {
		bastionHost = "your-user@bastion.example.com" // Default bastion host
	}
	proxyServiceHost := "kubedebugsess-proxy-svc.kubedebugsess-system.svc"
	proxyServicePort := "80"
	localPort := "8080"

	instructions := fmt.Sprintf(`Session is ready. Open TWO terminals and follow the steps:

--- Terminal 1: Create a secure tunnel ---
1. Run this command and leave it running. It forwards local port %s to the debug proxy via the bastion host.
   ssh -L %s:%s:%s %s

--- Terminal 2: Connect to the debug session ---
2. Once the tunnel is active, run this command in a new terminal. It uses the one-time token for authorization.
   websocat --header= "Authorization: Bearer %s" "ws://localhost:%s/attach?ns=%s&pod=%s&container=%s"`,
		localPort, localPort, proxyServiceHost, proxyServicePort, bastionHost,
		session.Status.OneTimeToken,
		localPort,
		session.Spec.TargetNamespace,
		session.Spec.TargetPodName,
		session.Status.DebuggingContainerName,
	)
	return instructions
}

// generateSecureToken creates a cryptographically secure, random hex string.
func generateSecureToken(length int) (string, error) {
	bytes := make([]byte, length)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}

// --- Handler functions for different container states ---
func (r *ActiveReconciler) handleRetry(ctx context.Context, session *debugv1alpha1.DebugSession, message string) (ctrl.Result, error) {
	session.Status.RetryCount = 1
	return session_phases.UpdateSessionStatus(ctx, r.Client, session, debugv1alpha1.Retrying, message)
}

func (r *ActiveReconciler) handleFail(ctx context.Context, session *debugv1alpha1.DebugSession, message string) (ctrl.Result, error) {
	return session_phases.UpdateSessionStatus(ctx, r.Client, session, debugv1alpha1.Failed, message)
}

func (r *ActiveReconciler) handleSucceed(ctx context.Context, session *debugv1alpha1.DebugSession, message string) (ctrl.Result, error) {
	return session_phases.UpdateSessionStatus(ctx, r.Client, session, debugv1alpha1.Terminating, message)
}

func (r *ActiveReconciler) handleWait(ctx context.Context, session *debugv1alpha1.DebugSession, message string) (ctrl.Result, error) {
	return ctrl.Result{}, nil
}
