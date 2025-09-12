/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// SessionPhase defines the observed phase of the DebugSession's lifecycle.
type SessionPhase string

// These are the possible phases for a DebugSession, reflecting the state machine.
const (
	// Pending: Initial state after creation. Waits for the controller to start processing.
	Pending SessionPhase = "Pending"

	// Injecting: The controller is actively trying to inject the container. Transitions from Pending.
	Injecting SessionPhase = "Injecting"

	// Active: The container is running successfully. Transitions from Injecting or Retrying.
	Active SessionPhase = "Active"

	// Retrying: The container terminated unexpectedly and the controller is attempting to restart it. Transitions from Active.
	Retrying SessionPhase = "Retrying"

	// Terminating: The session has ended (successfully or not) and the controller is cleaning up resources. Transitions from Active.
	Terminating SessionPhase = "Terminating"

	// Completed: The session finished successfully and cleanup is complete. This is a terminal state. Transitions from Terminating.
	Completed SessionPhase = "Completed"

	// Failed: The session failed and will not be retried. This is a terminal state. Transitions from Pending, Injecting, Retrying, or Terminating.
	Failed SessionPhase = "Failed"
)

// DebugSessionSpec defines the desired state of a DebugSession, as specified by the user.
type DebugSessionSpec struct {
	// TargetPodName is the name of the Pod to which the debug container will be attached.
	// This field is required.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	TargetPodName string `json:"targetPodName"`

	// TargetContainerName is the name of a specific container within the target Pod to debug.
	// If omitted, the debug container will be attached to the Pod without sharing a process namespace with a specific container.
	// This field is optional.
	// +kubebuilder:validation:Optional
	TargetContainerName string `json:"targetContainerName,omitempty"`

	// TargetNamespace is the namespace where the target Pod is located.
	// If omitted, the namespace of this DebugSession object will be used by default.
	// This field is optional.
	// +kubebuilder:validation:Optional
	TargetNamespace string `json:"targetNamespace,omitempty"`

	// DebuggerImage is the container image to use for the debugging session.
	// This image must contain all necessary debugging tools (e.g., netshoot, gdb, tcpdump).
	// This field is required.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	DebuggerImage string `json:"debuggerImage"`

	// MaxRetryCount is the maximum number of times to retry a session setup for recoverable errors.
	// If not specified, it defaults to 3.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:default=3
	MaxRetryCount int32 `json:"maxRetryCount,omitempty"`
}

// DebugSessionStatus defines the observed state of a DebugSession, as reported by the controller.
type DebugSessionStatus struct {
	// Phase represents the high-level summary of the session's current lifecycle stage.
	// This field is managed by the controller.
	// +kubebuilder:validation:Optional
	Phase SessionPhase `json:"phase,omitempty"`

	// Message provides a human-readable summary of the session's status, especially for error reporting.
	// This field is managed by the controller.
	// +kubebuilder:validation:Optional
	Message string `json:"message,omitempty"`

	// StartTime is the timestamp when the controller successfully initiated the debug session (e.g., attached the container).
	// This field is managed by the controller.
	// +kubebuilder:validation:Optional
	StartTime *metav1.Time `json:"startTime,omitempty"`

	// TerminationTime is the timestamp when the session was completed or failed.
	// This field is managed by the controller.
	// +kubebuilder:validation:Optional
	TerminationTime *metav1.Time `json:"terminationTime,omitempty"`

	// EphemeralContainerName is the actual, unique name of the ephemeral container created by the controller.
	// This is useful for direct interaction with the container via `kubectl`.
	// This field is managed by the controller.
	// +kubebuilder:validation:Optional
	DebuggingContainerName string `json:"ephemeralContainerName,omitempty"`

	// RetryCount tracks the number of retries for recoverable errors.
	// This field is managed by the controller.
	// +kubebuilder:validation:Optional

	RetryCount int `json:"retryCount,omitempty"`

	// Standard condition types include:
	// - "Available": the resource is fully functional
	// - "Progressing": the resource is being created or updated
	// - "Degraded": the resource failed to reach or maintain its desired state
	//
	// +listType=map
	// +listMapKey=type
	// +kubebuilder:validation:Optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="TargetPod",type=string,JSONPath=`.spec.targetPodName`
// +kubebuilder:printcolumn:name="Status",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"
// DebugSession is the Schema for the debugsessions API
type DebugSession struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +kubebuilder:validation:Optional
	metav1.ObjectMeta `json:"metadata,omitempty,omitzero"`

	// spec defines the desired state of DebugSession
	// +kubebuilder:validation:Required
	Spec DebugSessionSpec `json:"spec"`

	// status defines the observed state of DebugSession
	// +kubebuilder:validation:Optional
	Status DebugSessionStatus `json:"status,omitempty,omitzero"`
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
