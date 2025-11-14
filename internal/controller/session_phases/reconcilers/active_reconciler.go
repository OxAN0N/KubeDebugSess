package reconcilers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
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
			if containerStatus.State.Running != nil && !session.Status.ReadyForAttach {

				session.Status.ReadyForAttach = true
				sendWebhookIfConfigured(session)
				if err := r.Status().Update(ctx, session); err != nil {
					logger.Error(err, "Failed to Update before Attach")
					return ctrl.Result{}, err
				}
				return ctrl.Result{}, nil
			}

			action, message := session_phases.AnalyzeContainerStatus(containerStatus)
			if handler, ok := r.actionHandlers[action]; ok {
				if action != session_phases.ActionWait {
					session.Status.ReadyForAttach = false
				}
				return handler(ctx, session, message)
			}
			return ctrl.Result{}, nil
		}
	}

	logger.Info("Ephemeral container status not found yet, requeueing.")
	return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
}

// sendWebhookIfConfigured sends the session message to a webhook if WEBHOOK_URL is set.
// Slack / Discord detection is done by inspecting the webhook domain.
func sendWebhookIfConfigured(session *debugv1alpha1.DebugSession) {
	webhookURL := os.Getenv("WEBHOOK_URL")
	if webhookURL == "" {
		return
	}

	payload := buildWebhookPayload(webhookURL, session)

	data, err := json.Marshal(payload)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to marshal webhook payload: %v\n", err)
		return
	}

	go func() {
		req, err := http.NewRequest("POST", webhookURL, bytes.NewBuffer(data))
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to create webhook request: %v\n", err)
			return
		}
		req.Header.Set("Content-Type", "application/json")

		client := &http.Client{Timeout: 5 * time.Second}
		resp, err := client.Do(req)
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to send webhook: %v\n", err)
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			fmt.Fprintf(os.Stderr, "webhook returned non-2xx status: %s\n", resp.Status)
		}
	}()
}

// buildWebhookPayload builds the message body depending on webhook domain type.
func buildWebhookPayload(webhookURL string, session *debugv1alpha1.DebugSession) interface{} {
	msg := session.Status.Message
	ns := session.Spec.TargetNamespace
	pod := session.Spec.TargetPodName
	container := session.Status.DebuggingContainerName

	switch {
	case strings.Contains(webhookURL, "hooks.slack.com"):
		return map[string]interface{}{
			"text": fmt.Sprintf(
				"*KubeDebugSess – Debug session ready*\nNamespace: `%s`\nPod: `%s`\nContainer: `%s`\n\n```%s```",
				ns, pod, container, msg),
		}

	case strings.Contains(webhookURL, "discord.com/api/webhooks"):
		return map[string]interface{}{
			"embeds": []map[string]interface{}{
				{
					"title":       "🐳 KubeDebugSess – Debug session ready",
					"description": fmt.Sprintf("**Namespace:** `%s`\n**Pod:** `%s`\n**Container:** `%s`\n\n```\n%s\n```", ns, pod, container, msg),
					"color":       0x00bfff,
					"timestamp":   time.Now().UTC().Format(time.RFC3339),
				},
			},
		}

	default:
		return map[string]interface{}{
			"namespace": ns,
			"pod":       pod,
			"container": container,
			"message":   msg,
			"timestamp": time.Now().UTC().Format(time.RFC3339),
		}
	}
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
