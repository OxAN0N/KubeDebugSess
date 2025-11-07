package reconcilers

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	debugv1alpha1 "github.com/OxAN0N/KubeDebugSess/api/v1alpha1"
	"github.com/OxAN0N/KubeDebugSess/internal/controller/session_phases"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

type TerminatingReconciler struct {
	client.Client
	ClientSet kubernetes.Interface
	S3Client  *s3.Client
	S3Bucket  string
}

func init() {
	session_phases.Register(debugv1alpha1.Terminating, NewTerminatingReconciler)
}

func NewTerminatingReconciler(c client.Client, cs kubernetes.Interface) session_phases.PhaseReconciler {
	region := os.Getenv("AWS_REGION")
	bucket := os.Getenv("S3_BUCKET_NAME")
	accessKey := os.Getenv("AWS_ACCESS_KEY_ID")
	secretKey := os.Getenv("AWS_SECRET_ACCESS_KEY")

	var cfg aws.Config
	var err error

	cfg, err = config.LoadDefaultConfig(context.Background(),
		config.WithRegion(region),
	)
	if err != nil {
		panic(fmt.Sprintf("failed to load default AWS config: %v", err))
	}

	if accessKey != "" && secretKey != "" {
		cfg.Credentials = aws.NewCredentialsCache(
			credentials.NewStaticCredentialsProvider(accessKey, secretKey, ""),
		)
	}

	s3Client := s3.NewFromConfig(cfg)

	return &TerminatingReconciler{
		Client:    c,
		ClientSet: cs,
		S3Client:  s3Client,
		S3Bucket:  bucket,
	}
}

func (r *TerminatingReconciler) Reconcile(ctx context.Context, session *debugv1alpha1.DebugSession) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.Info("Starting cleanup for Terminating session.")

	if err := r.cleanupEphemeralContainer(ctx, session); err != nil {
		logger.Error(err, "Failed to cleanup ephemeral container.")
		return session_phases.UpdateSessionStatus(ctx, r.Client, session, debugv1alpha1.Failed, err.Error())
	}

	logger.Info("Successfully terminated debugging session. Transitioning to Completed.")
	now := metav1.NewTime(time.Now())
	session.Status.TerminationTime = &now

	return session_phases.UpdateSessionStatus(ctx, r.Client, session, debugv1alpha1.Completed, "Termination Completed")
}

func (r *TerminatingReconciler) cleanupEphemeralContainer(ctx context.Context, session *debugv1alpha1.DebugSession) error {
	logger := log.FromContext(ctx)

	pod, err := r.getTargetPod(ctx, session)
	if err != nil {
		return err
	}

	debuggerName := fmt.Sprintf("debugger-%s", session.UID)
	if !r.isEphemeralContainerPresent(pod, debuggerName) {
		return fmt.Errorf("debugger container '%s' not found in pod '%s'", debuggerName, pod.Name)
	}

	logData, err := r.fetchEphemeralLogs(ctx, pod, debuggerName)
	if err != nil {
		return fmt.Errorf("failed to fetch ephemeral logs: %w", err)
	}

	s3Key, err := r.uploadLogsToS3(ctx, pod, debuggerName, logData)
	if err != nil {
		return fmt.Errorf("failed to upload logs to S3: %w", err)
	}

	if err := r.Status().Update(ctx, session); err != nil {
		logger.Error(err, "Failed to update session with log URL")
	}

	logger.Info("Ephemeral container cleanup complete",
		"pod", pod.Name, "container", debuggerName, "s3Key", s3Key)

	return nil
}

func (r *TerminatingReconciler) getTargetPod(ctx context.Context, session *debugv1alpha1.DebugSession) (*corev1.Pod, error) {
	if session.Spec.TargetNamespace == "" {
		session.Spec.TargetNamespace = session.Namespace
	}

	pod := &corev1.Pod{}
	key := types.NamespacedName{
		Name:      session.Spec.TargetPodName,
		Namespace: session.Spec.TargetNamespace,
	}

	if err := r.Get(ctx, key, pod); err != nil {
		if errors.IsNotFound(err) {
			return nil, fmt.Errorf("target pod '%s' not found", key.Name)
		}
		return nil, fmt.Errorf("failed to get target pod: %w", err)
	}

	return pod, nil
}

func (r *TerminatingReconciler) isEphemeralContainerPresent(pod *corev1.Pod, containerName string) bool {
	for _, ec := range pod.Spec.EphemeralContainers {
		if ec.Name == containerName {
			return true
		}
	}
	return false
}

func (r *TerminatingReconciler) fetchEphemeralLogs(ctx context.Context, pod *corev1.Pod, containerName string) ([]byte, error) {
	logger := log.FromContext(ctx)
	logger.Info("Fetching logs for ephemeral container", "container", containerName)

	opts := &corev1.PodLogOptions{
		Container:  containerName,
		Timestamps: true,
	}

	req := r.ClientSet.CoreV1().Pods(pod.Namespace).GetLogs(pod.Name, opts)
	stream, err := req.Stream(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to open log stream: %w", err)
	}
	defer stream.Close()

	var logs bytes.Buffer
	buf := make([]byte, 4096)
	for {
		n, err := stream.Read(buf)
		if n > 0 {
			logs.Write(buf[:n])
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("error reading log stream: %w", err)
		}
	}

	rawLogs := logs.Bytes()
	cleaned := r.cleanLogData(rawLogs)

	logger.Info("Fetched and cleaned ephemeral container logs", "rawSize", len(rawLogs), "cleanSize", len(cleaned))
	return cleaned, nil
}

func (r *TerminatingReconciler) cleanLogData(data []byte) []byte {
	var cleaned []byte
	inEscape := false

	for i := 0; i < len(data); i++ {
		b := data[i]

		if b == 0x1b {
			inEscape = true
			continue
		}

		if inEscape {
			if (b >= 'A' && b <= 'Z') || (b >= 'a' && b <= 'z') || b == '~' {
				inEscape = false
			}
			continue
		}

		if b == '\r' || b == '\x07' || b == '\x08' {
			continue
		}

		cleaned = append(cleaned, b)
	}

	// 연속 공백/개행 정리 (선택)
	cleaned = bytes.ReplaceAll(cleaned, []byte("\n\n\n"), []byte("\n\n"))
	return cleaned
}

func (r *TerminatingReconciler) uploadLogsToS3(ctx context.Context, pod *corev1.Pod, containerName string, data []byte) (string, error) {
	s3Key := fmt.Sprintf("debug-sessions/%s/%s-%d.log", pod.Namespace, containerName, time.Now().Unix())

	_, err := r.S3Client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: &r.S3Bucket,
		Key:    &s3Key,
		Body:   bytes.NewReader(data),
	})
	if err != nil {
		return "", fmt.Errorf("S3 upload failed: %w", err)
	}

	return s3Key, nil
}
