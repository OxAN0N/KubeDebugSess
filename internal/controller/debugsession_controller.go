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

package controller

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	debugv1alpha1 "github.com/OxAN0N/KubeDebugSess/api/v1alpha1"
	"github.com/OxAN0N/KubeDebugSess/internal/controller/session_phases"
)

// DebugSessionReconciler reconciles a DebugSession object
type DebugSessionReconciler struct {
	client.Client
	ClientSet        kubernetes.Interface
	Scheme           *runtime.Scheme
	PhaseReconcilers map[debugv1alpha1.SessionPhase]session_phases.PhaseReconciler
}

const targetPodIndexKey = "targetPodIndexKey"

// +kubebuilder:rbac:groups=ajou.oxan0n.me,resources=debugsessions,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=ajou.oxan0n.me,resources=debugsessions/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=ajou.oxan0n.me,resources=debugsessions/finalizers,verbs=update
// +kubebuilder:rbac:groups=ajou.oxan0n.me,resources=registrycredentials,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=ajou.oxan0n.me,resources=registrycredentials/status,verbs=get;update;patch
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch;patch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
func (r *DebugSessionReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var debugSession debugv1alpha1.DebugSession
	if err := r.Get(ctx, req.NamespacedName, &debugSession); err != nil {
		logger.Info("Reconciling DebugSession")
		return ctrl.Result{}, nil
	}

	reconciler, ok := r.PhaseReconcilers[debugSession.Status.Phase]
	if !ok {
		logger.Info("Reconciling DebugSession")
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	}

	return reconciler.Reconcile(ctx, &debugSession)
}

func (r *DebugSessionReconciler) findSessionsForPod(ctx context.Context, pod client.Object) []reconcile.Request {
	logger := log.FromContext(ctx)
	attachedSessions := &debugv1alpha1.DebugSessionList{}
	podKey := fmt.Sprintf("%s/%s", pod.GetNamespace(), pod.GetName())

	if err := r.List(ctx, attachedSessions, client.MatchingFields{targetPodIndexKey: podKey}); err != nil {
		logger.Error(err, "failed to list attached debug sessions using index", "podKey", podKey)
		return []reconcile.Request{}
	}

	requests := make([]reconcile.Request, len(attachedSessions.Items))
	logger.Info("Found attached sessions for pod", "count", len(requests), "pod", pod.GetName())

	for i, item := range attachedSessions.Items {
		requests[i] = reconcile.Request{
			NamespacedName: types.NamespacedName{
				Name:      item.GetName(),
				Namespace: item.GetNamespace(),
			},
		}
	}
	return requests
}

// SetupWithManager sets up the controller with the Manager.
func (r *DebugSessionReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.PhaseReconcilers = session_phases.GetReconcilers(mgr.GetClient(), r.ClientSet)

	if err := mgr.GetFieldIndexer().IndexField(context.Background(), &debugv1alpha1.DebugSession{}, targetPodIndexKey, func(rawObj client.Object) []string {
		session := rawObj.(*debugv1alpha1.DebugSession)
		targetNamespace := session.Spec.TargetNamespace
		if targetNamespace == "" {
			targetNamespace = session.Namespace
		}
		return []string{fmt.Sprintf("%s/%s", targetNamespace, session.Spec.TargetPodName)}
	}); err != nil {
		return err
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&debugv1alpha1.DebugSession{}).
		Watches(
			&corev1.Pod{},
			handler.EnqueueRequestsFromMapFunc(r.findSessionsForPod),
		).
		Complete(r)
}
