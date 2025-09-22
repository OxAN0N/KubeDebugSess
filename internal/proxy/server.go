package proxy

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"

	debugv1alpha1 "github.com/OxAN0N/KubeDebugSess/api/v1alpha1"

	"github.com/gorilla/websocket"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var upgrader = websocket.Upgrader{
	// In production, you should validate the origin.
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

// Server implements the HTTP handler for the debug proxy.
type Server struct {
	Clientset *kubernetes.Clientset
	RESTCfg   *rest.Config
	K8sClient client.Client // Client to read Custom Resources like DebugSession
}

// NewServer creates a new proxy server.
func NewServer(clientset *kubernetes.Clientset, restCfg *rest.Config, k8sClient client.Client) *Server {
	return &Server{
		Clientset: clientset,
		RESTCfg:   restCfg,
		K8sClient: k8sClient,
	}
}

// ServeHTTP handles the incoming HTTP request, including token authorization.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	ns := q.Get("ns")
	podName := q.Get("pod")
	containerName := q.Get("container")

	if ns == "" || podName == "" || containerName == "" {
		http.Error(w, "Missing required query parameters: ns, pod, container", http.StatusBadRequest)
		return
	}

	// --- Authorization Logic: Validate the one-time token ---
	authHeader := r.Header.Get("Authorization")
	tokenParts := strings.Split(authHeader, " ")
	if len(tokenParts) != 2 || !strings.EqualFold(tokenParts[0], "bearer") {
		http.Error(w, "Invalid or missing Authorization header. Expected 'Bearer <token>'.", http.StatusUnauthorized)
		return
	}
	receivedToken := tokenParts[1]

	// Find the corresponding DebugSession to validate the token.
	// We derive the session UID from the container name.
	sessionUID := strings.TrimPrefix(containerName, "debugger-")
	var debugSession debugv1alpha1.DebugSession
	found := false
	sessionList := &debugv1alpha1.DebugSessionList{}

	// List all sessions and find the one with the matching UID.
	// For performance in a large cluster, you might index this.
	if err := s.K8sClient.List(r.Context(), sessionList); err != nil {
		log.Printf("Error listing debug sessions: %v", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	for _, sess := range sessionList.Items {
		if string(sess.UID) == sessionUID {
			debugSession = sess
			found = true
			break
		}
	}

	if !found {
		http.Error(w, "Debug session not found", http.StatusNotFound)
		return
	}

	// Check if the session is ready and the token matches.
	if !debugSession.Status.ReadyForAttach || debugSession.Status.OneTimeToken != receivedToken {
		http.Error(w, "Unauthorized: Invalid or expired token", http.StatusUnauthorized)
		return
	}
	// --- End of Authorization Logic ---

	// Upgrade the connection to a WebSocket
	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("Failed to upgrade connection for pod %s: %v", podName, err)
		return
	}
	defer ws.Close()

	// Stream between the WebSocket and the container
	if err := s.stream(r.Context(), ns, podName, containerName, ws); err != nil {
		log.Printf("Stream error for pod %s/%s: %v", ns, podName, err)
		// Try to send a close message to the client before returning.
		_ = ws.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseInternalServerErr, err.Error()))
	}
}

// stream handles the bidirectional data flow between the WebSocket and the K8s attach stream.
func (s *Server) stream(ctx context.Context, ns, podName, containerName string, ws *websocket.Conn) error {
	req := s.Clientset.CoreV1().RESTClient().
		Post().
		Resource("pods").
		Name(podName).
		Namespace(ns).
		SubResource("attach").
		Param("container", containerName).
		Param("stdin", "true").
		Param("stdout", "true").
		Param("stderr", "true").
		Param("tty", "true")

	executor, err := remotecommand.NewSPDYExecutor(s.RESTCfg, "POST", req.URL())
	if err != nil {
		return fmt.Errorf("failed to create SPDY executor: %w", err)
	}

	stdinReader, stdinWriter := io.Pipe()
	stdoutReader, stdoutWriter := io.Pipe()

	// Goroutine to read from WebSocket and write to container's stdin
	go func() {
		defer stdinWriter.Close()
		for {
			_, payload, err := ws.ReadMessage()
			if err != nil {
				return
			}
			if _, err := stdinWriter.Write(payload); err != nil {
				return
			}
		}
	}()

	// Goroutine to read from container's stdout and write to WebSocket
	go func() {
		defer stdoutReader.Close()
		buf := make([]byte, 2048)
		for {
			n, err := stdoutReader.Read(buf)
			if n > 0 {
				if err := ws.WriteMessage(websocket.BinaryMessage, buf[:n]); err != nil {
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()

	// Start the streaming
	err = executor.Stream(remotecommand.StreamOptions{
		Stdin:  stdinReader,
		Stdout: stdoutWriter,
		Stderr: stdoutWriter,
		Tty:    true,
	})

	return err
}
