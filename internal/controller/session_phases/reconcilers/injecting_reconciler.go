package reconcilers

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"strconv"

	debugv1alpha1 "github.com/OxAN0N/KubeDebugSess/api/v1alpha1"
	"github.com/OxAN0N/KubeDebugSess/internal/controller/session_phases"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/utils/ptr"
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

	if session.Spec.TargetNamespace == "" {
		session.Spec.TargetNamespace = session.Namespace
	}

	podName := session.Spec.TargetPodName
	pod := &corev1.Pod{}

	if err := r.Get(ctx, types.NamespacedName{
		Name:      podName,
		Namespace: session.Spec.TargetNamespace,
	}, pod); err != nil {
		return session_phases.UpdateSessionStatus(ctx, r.Client, session, debugv1alpha1.Failed, "Failed to find Target Pod")
	}

	if session.Spec.TargetContainerName == "" {
		if len(pod.Spec.Containers) > 0 {
			session.Spec.TargetContainerName = pod.Spec.Containers[0].Name
		} else {
			return session_phases.UpdateSessionStatus(ctx, r.Client, session, debugv1alpha1.Failed, "Failed to find Target Container")
		}
	}

	nodeIP, nodePort, err := r.checkInjectingCondition(ctx, pod)
	if err != nil {
		return session_phases.UpdateSessionStatus(ctx, r.Client, session,
			debugv1alpha1.Failed, fmt.Sprintf("Inject Failed: %v", err))
	}

	if _, err := r.setUpDebugSess(ctx, session); err != nil {
		return session_phases.UpdateSessionStatus(ctx, r.Client, session,
			debugv1alpha1.Failed, fmt.Sprintf("Setup Failed: %v", err))
	}

	logger.Info("Injection Started")
	if err := r.injectEphemeralContainer(ctx, session, pod); err != nil {
		return session_phases.UpdateSessionStatus(ctx, r.Client, session,
			debugv1alpha1.Failed, fmt.Sprintf("Inject Failed: %v", err))
	}
	return session_phases.UpdateSessionStatus(ctx, r.Client, session, debugv1alpha1.Active, buildConnectionString(session, nodeIP, nodePort))
}

func (r *InjectingReconciler) checkInjectingCondition(ctx context.Context, pod *corev1.Pod) (string, string, error) {
	logger := log.FromContext(ctx)

	if pod.Spec.ShareProcessNamespace == nil || !*pod.Spec.ShareProcessNamespace {
		return "", "", fmt.Errorf("pod.Spec.ShareProcessNamespace is false")
	}

	nodeIP, nodePort, err := getProxyServiceNodeInfo(ctx, r.ClientSet)
	if err != nil {
		logger.Error(err, "Failed to get proxy NodePort info")
		return nodeIP, nodePort, err
	}

	return nodeIP, nodePort, nil
}

func (r *InjectingReconciler) setUpDebugSess(ctx context.Context, session *debugv1alpha1.DebugSession) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	token, err := generateSecureToken(32)
	if err != nil {
		logger.Error(err, "Failed to generate session token")
		return ctrl.Result{}, err
	}

	session.Status.OneTimeToken = token

	if err := r.Status().Update(ctx, session); err != nil {
		logger.Error(err, "Failed to update session status with token")
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *InjectingReconciler) injectEphemeralContainer(ctx context.Context, session *debugv1alpha1.DebugSession, pod *corev1.Pod) error {
	debugScript := `
    trap 'exit 0' EXIT TERM INT
    ( sleep ${TTL:-300} && exit 0 ) &
    exec /bin/sh -i
	`

	debuggerName := fmt.Sprintf("debugger-%s", session.UID)

	ec := corev1.EphemeralContainer{
		EphemeralContainerCommon: corev1.EphemeralContainerCommon{
			Name:    debuggerName,
			Image:   session.Spec.DebuggerImage,
			Command: []string{"/bin/sh"},
			Args:    []string{"-c", debugScript},
			Stdin:   true,
			TTY:     true,
			Env: []corev1.EnvVar{
				{Name: "TTL", Value: strconv.Itoa(int(session.Spec.TTL))},
			},
		},
		TargetContainerName: session.Spec.TargetContainerName,
	}

	ec.SecurityContext = buildSecurityContext(session.Spec.DebugSecurity)

	pod.Spec.EphemeralContainers = append(pod.Spec.EphemeralContainers, ec)
	if _, err := r.ClientSet.CoreV1().
		Pods(session.Spec.TargetNamespace).
		UpdateEphemeralContainers(ctx, pod.Name, pod, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("failed to update ephemeral containers: %w", err)
	}

	session.Status.DebuggingContainerName = debuggerName
	if err := r.Status().Update(ctx, session); err != nil {
		return fmt.Errorf("failed to update session status with debugging container name: %w", err)
	}

	return nil
}

// buildConnectionString creates the user instructions for connecting to the debug proxy.
func buildConnectionString(session *debugv1alpha1.DebugSession, nodeIP, nodePort string) string {
	bastionHost := os.Getenv("BASTION_HOST")
	if bastionHost == "" {
		bastionHost = "your-user@bastion.example.com"
	}
	localPort := "8080"

	return fmt.Sprintf(`Session is ready. Open TWO terminals and follow the steps:

--- Terminal 1: Create a secure tunnel ---
1. Run this command and leave it running. It forwards local port %s to the debug proxy via the bastion host.
   ssh -L %s:%s:%s %s

--- Terminal 2: Connect to the debug session ---
2. Once the tunnel is active, run this command in a new terminal. It uses the one-time token for authorization.
   websocat --no-line --binary --header="Authorization: Bearer %s" "ws://localhost:%s/attach?ns=%s&pod=%s&container=%s"`,
		localPort, localPort, nodeIP, nodePort, bastionHost,
		session.Status.OneTimeToken,
		localPort,
		session.Spec.TargetNamespace,
		session.Spec.TargetPodName,
		session.Status.DebuggingContainerName,
	)
}

// generateSecureToken creates a cryptographically secure, random hex string.
func generateSecureToken(length int) (string, error) {
	bytes := make([]byte, length)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}

func getProxyServiceNodeInfo(ctx context.Context, clientset kubernetes.Interface) (string, string, error) {
	svc, err := clientset.CoreV1().Services("kubedebugsess-system").Get(ctx, "kubedebugsess-proxy-svc", metav1.GetOptions{})
	if err != nil {
		return "", "", fmt.Errorf("failed to get service: %w", err)
	}

	if len(svc.Spec.Ports) == 0 {
		return "", "", fmt.Errorf("no ports found in service")
	}

	nodePort := fmt.Sprintf("%d", svc.Spec.Ports[0].NodePort)
	nodeList, err := clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return "", "", fmt.Errorf("failed to list nodes: %w", err)
	}
	if len(nodeList.Items) == 0 {
		return "", "", fmt.Errorf("no nodes found in cluster")
	}

	var nodeIP string
	for _, addr := range nodeList.Items[0].Status.Addresses {
		if addr.Type == corev1.NodeExternalIP {
			nodeIP = addr.Address
			break
		}
		if addr.Type == corev1.NodeInternalIP && nodeIP == "" {
			nodeIP = addr.Address
		}
	}
	if nodeIP == "" {
		nodeIP = "127.0.0.1"
	}

	return nodeIP, nodePort, nil
}

// --- helpers ---

func buildSecurityContext(sec *debugv1alpha1.DebugSecurityContext) *corev1.SecurityContext {
	defaultUID := int64(1000)
	defaultGID := int64(1000)
	defaultRunAsNonRoot := true
	defaultPrivileged := false
	defaultAllowPrivEsc := false
	defaultReadOnlyRootFS := true

	sc := &corev1.SecurityContext{
		RunAsNonRoot:             ptr.To(defaultRunAsNonRoot),
		RunAsUser:                &defaultUID,
		RunAsGroup:               &defaultGID,
		Privileged:               ptr.To(defaultPrivileged),
		AllowPrivilegeEscalation: ptr.To(defaultAllowPrivEsc),
		ReadOnlyRootFilesystem:   ptr.To(defaultReadOnlyRootFS),
		Capabilities: &corev1.Capabilities{
			Drop: []corev1.Capability{"ALL"},
			Add:  []corev1.Capability{},
		},
	}

	if sec != nil {
		if sec.RunAsNonRoot != nil {
			sc.RunAsNonRoot = sec.RunAsNonRoot
		}
		if sec.RunAsUser != nil {
			sc.RunAsUser = sec.RunAsUser
		}
		if sec.RunAsGroup != nil {
			sc.RunAsGroup = sec.RunAsGroup
		}
		if sec.Privileged != nil {
			sc.Privileged = sec.Privileged
		}
		if sec.AllowPrivilegeEscalation != nil {
			sc.AllowPrivilegeEscalation = sec.AllowPrivilegeEscalation
		}
		if sec.ReadOnlyRootFilesystem != nil {
			sc.ReadOnlyRootFilesystem = sec.ReadOnlyRootFilesystem
		}

		// Capabilities는 조금 더 정교하게 병합
		if sec.Capabilities != nil {
			add := sec.Capabilities.Add
			drop := sec.Capabilities.Drop
			if add == nil {
				add = []corev1.Capability{}
			}
			if drop == nil {
				drop = []corev1.Capability{}
			}
			sc.Capabilities = &corev1.Capabilities{
				Add:  add,
				Drop: drop,
			}
		}
	}

	return sc
}
