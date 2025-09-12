package session_phases

import "time"

// RequeueError는 재시도가 필요함을 나타내는 커스텀 에러입니다.
// 재시도 사유(Reason)와 재시도 간격(RequeueAfter)을 포함합니다.
type RequeueError struct {
	Reason       string
	RequeueAfter time.Duration
}

// Error 메서드를 구현하여 error 인터페이스를 만족시킵니다.
func (e *RequeueError) Error() string {
	return e.Reason
}
