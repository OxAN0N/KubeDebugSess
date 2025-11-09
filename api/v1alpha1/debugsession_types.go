/*
Copyright 2025.
*/

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// SessionPhase defines the observed phase of the DebugSession's lifecycle.
type SessionPhase string

// These are the possible phases for a DebugSession, reflecting the state machine.
const (
	Pending     SessionPhase = "Pending"
	Injecting   SessionPhase = "Injecting"
	Active      SessionPhase = "Active"
	Retrying    SessionPhase = "Retrying"
	Terminating SessionPhase = "Terminating"
	Completed   SessionPhase = "Completed"
	Failed      SessionPhase = "Failed"
)

// DebugSessionSpec defines the desired state of a DebugSession, as specified by the user.
type DebugSessionSpec struct {
	// TargetPodName is the name of the Pod to which the debug container will be attached.
	// +kubebuilder:validation:Required
	TargetPodName string `json:"targetPodName"`

	// TargetContainerName is the name of a specific container within the target Pod to debug.
	// +kubebuilder:validation:Optional
	TargetContainerName string `json:"targetContainerName,omitempty"`

	// TargetNamespace is the namespace where the target Pod is located.
	// +kubebuilder:validation:Optional
	TargetNamespace string `json:"targetNamespace,omitempty"`

	// DebuggerImage is the container image to use for the debugging session.
	// +kubebuilder:validation:Required
	DebuggerImage string `json:"debuggerImage"`

	// TTL is the maximum seconds for debugging sessions.
	// +kubebuilder:validation:Optional
	// +kubebuilder:default=300
	TTL int32 `json:"ttl,omitempty"`

	// MaxRetryCount is the maximum number of times to retry a session setup for recoverable errors.
	// +kubebuilder:validation:Optional
	// +kubebuilder:default=3
	MaxRetryCount int32 `json:"maxRetryCount,omitempty"`
}

// DebugSessionStatus defines the observed state of a DebugSession, as reported by the controller.
type DebugSessionStatus struct {
	// Phase represents the high-level summary of the session's current lifecycle stage.
	// +kubebuilder:validation:Optional
	Phase SessionPhase `json:"phase,omitempty"`

	// Message provides a human-readable summary of the session's status, including connection instructions.
	// +kubebuilder:validation:Optional
	Message string `json:"message,omitempty"`

	// StartTime is the timestamp when the controller successfully initiated the debug session.
	// +kubebuilder:validation:Optional
	StartTime *metav1.Time `json:"startTime,omitempty"`

	// TerminationTime is the timestamp when the session was completed or failed.
	// +kubebuilder:validation:Optional
	TerminationTime *metav1.Time `json:"terminationTime,omitempty"`

	// DebuggingContainerName is the actual, unique name of the ephemeral container created by the controller.
	// +kubebuilder:validation:Optional
	DebuggingContainerName string `json:"debuggingContainerName,omitempty"`

	// ReadyForAttach indicates if the debug container is running and ready for connection.
	// +kubebuilder:validation:Optional
	ReadyForAttach bool `json:"readyForAttach,omitempty"`

	// OneTimeToken stores a short-lived token for authorizing the session connection.
	// This token must be passed in the Authorization header by the client.
	// +kubebuilder:validation:Optional
	OneTimeToken string `json:"oneTimeToken,omitempty"`

	// RetryCount tracks the number of retries for recoverable errors.
	// +kubebuilder:validation:Optional
	RetryCount int `json:"retryCount,omitempty"`

	// Conditions provides detailed observations of the resource's current state.
	// +listType=map
	// +listMapKey=type
	// +kubebuilder:validation:Optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="TargetPod",type=string,JSONPath=`.spec.targetPodName`
// +kubebuilder:printcolumn:name="Status",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.readyForAttach`
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"
// DebugSession is the Schema for the debugsessions API
type DebugSession struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   DebugSessionSpec   `json:"spec"`
	Status DebugSessionStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// DebugSessionList contains a list of DebugSession
type DebugSessionList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []DebugSession `json:"items"`
}

func init() {
	SchemeBuilder.Register(&DebugSession{}, &DebugSessionList{})
}
