package session_phases

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
)

// ReasonAction은 컨테이너 상태 분석 후 수행할 행동을 정의합니다.
type ReasonAction int

const (
	ActionWait    ReasonAction = iota // 정상적인 일시적 상태, 대기합니다.
	ActionRetry                       // 복구 가능한 오류, 재시도를 시작/계속합니다.
	ActionFail                        // 복구 불가능한 오류, 즉시 실패 처리합니다.
	ActionSucceed                     // 성공적으로 완료되었습니다.
)

// waitingReasonMap은 Waiting 상태의 Reason별 행동을 정의합니다.
var waitingReasonMap = map[string]ReasonAction{
	"ContainerCreating":          ActionWait, //TODO : handle it on injecting reconciler
	"ImagePullBackOff":           ActionRetry,
	"RegistryUnavailable":        ActionRetry,
	"CrashLoopBackOff":           ActionRetry,
	"CreateContainerError":       ActionRetry,
	"RunContainerError":          ActionRetry,
	"NetworkPluginNotReady":      ActionRetry,
	"ErrImagePull":               ActionFail,
	"InvalidImageName":           ActionFail,
	"CreateContainerConfigError": ActionFail,
}

// terminatedReasonMap은 Terminated 상태의 Reason별 행동을 정의합니다.
var terminatedReasonMap = map[string]ReasonAction{
	"Completed":          ActionSucceed,
	"Error":              ActionFail,
	"OOMKilled":          ActionFail,
	"ContainerCannotRun": ActionFail,
	"DeadlineExceeded":   ActionFail,
}

// AnalyzeContainerStatus는 ContainerStatus를 분석하여 수행할 Action을 반환합니다.
func AnalyzeContainerStatus(status corev1.ContainerStatus) (action ReasonAction, message string) {
	if status.State.Running != nil {
		return ActionWait, "Session is running."
	}

	if status.State.Waiting != nil {
		reason := status.State.Waiting.Reason
		action, ok := waitingReasonMap[reason]
		if !ok {
			return ActionFail, fmt.Sprintf("Unknown waiting reason '%s'. Attempting to retry.", reason)
		}
		return action, fmt.Sprintf("Container is waiting. Reason: %s", reason)
	}

	if status.State.Terminated != nil {
		reason := status.State.Terminated.Reason
		action, ok := terminatedReasonMap[reason]
		if !ok {
			return ActionFail, fmt.Sprintf("Container terminated with unknown reason '%s'.", reason)
		}
		return action, fmt.Sprintf("Container terminated. Reason: %s", reason)
	}

	// 어떠한 상태도 아닐 경우, 안전하게 대기합니다.
	return ActionWait, "Container status is not yet determined."
}
