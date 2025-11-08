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

// wsconn is a wrapper around *websocket.Conn that implements the io.ReadWriter interface.
type wsconn struct {
	conn       *websocket.Conn
	readBuffer []byte
}

// Read implements the io.Reader interface for the websocket connection.
func (w *wsconn) Read(p []byte) (n int, err error) {
	if len(w.readBuffer) > 0 {
		n = copy(p, w.readBuffer)
		w.readBuffer = w.readBuffer[n:]
		return n, nil
	}
	_, message, err := w.conn.ReadMessage()
	if err != nil {
		if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
			log.Printf("websocket close error: %v", err)
		}
		return 0, io.EOF
	}
	n = copy(p, message)
	if n < len(message) {
		w.readBuffer = message[n:]
	}
	return n, nil
}

// Write implements the io.Writer interface for the websocket connection.
func (w *wsconn) Write(p []byte) (n int, err error) {
	err = w.conn.WriteMessage(websocket.BinaryMessage, p)
	if err != nil {
		return 0, err
	}
	return len(p), nil
}

// terminalSizeQueue implements the remotecommand.TerminalSizeQueue interface.
type terminalSizeQueue struct {
	ch chan remotecommand.TerminalSize
}

// Next returns the new terminal size from the channel.
func (q *terminalSizeQueue) Next() *remotecommand.TerminalSize {
	size, ok := <-q.ch
	if !ok {
		return nil
	}
	return &size
}

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

type Server struct {
	Clientset *kubernetes.Clientset
	RESTCfg   *rest.Config
	K8sClient client.Client
}

func NewServer(clientset *kubernetes.Clientset, restCfg *rest.Config, k8sClient client.Client) *Server {
	return &Server{
		Clientset: clientset,
		RESTCfg:   restCfg,
		K8sClient: k8sClient,
	}
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	ns := q.Get("ns")
	podName := q.Get("pod")
	containerName := q.Get("container")

	if ns == "" || podName == "" || containerName == "" {
		http.Error(w, "Missing required query parameters", http.StatusBadRequest)
		return
	}
	authHeader := r.Header.Get("Authorization")
	tokenParts := strings.Split(authHeader, " ")
	if len(tokenParts) != 2 || !strings.EqualFold(tokenParts[0], "bearer") {
		http.Error(w, "Invalid Authorization header", http.StatusUnauthorized)
		return
	}
	receivedToken := tokenParts[1]
	sessionUID := strings.TrimPrefix(containerName, "debugger-")
	var debugSession debugv1alpha1.DebugSession
	found := false
	sessionList := &debugv1alpha1.DebugSessionList{}
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
	if !debugSession.Status.ReadyForAttach || debugSession.Status.OneTimeToken != receivedToken {
		http.Error(w, "Unauthorized: Invalid or expired token", http.StatusUnauthorized)
		return
	}

	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("Failed to upgrade connection for pod %s: %v", podName, err)
		return
	}
	defer ws.Close()

	if err := s.stream(r.Context(), ns, podName, containerName, ws); err != nil {
		log.Printf("Stream error for pod %s/%s: %v", ns, podName, err)
		_ = ws.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseInternalServerErr, err.Error()))
	}
}

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

	// Goroutine to handle client input (WebSocket -> stdin)
	go func() {
		defer stdinWriter.Close()
		for {
			_, payload, err := ws.ReadMessage()
			if err != nil {
				return
			}
			payload = append(payload, '\n')
			if _, err := stdinWriter.Write(payload); err != nil {
				return
			}
		}
	}()

	streamer := &wsconn{conn: ws}
	resizeChan := make(chan remotecommand.TerminalSize, 1)
	resizeQueue := &terminalSizeQueue{ch: resizeChan}
	resizeChan <- remotecommand.TerminalSize{Width: 120, Height: 40}

	err = executor.StreamWithContext(ctx, remotecommand.StreamOptions{
		Stdin:             stdinReader,
		Stdout:            streamer,
		Stderr:            streamer,
		Tty:               true,
		TerminalSizeQueue: resizeQueue,
	})

	return err
}
