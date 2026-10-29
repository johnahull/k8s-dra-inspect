// Package collect gathers a read-only Snapshot from a live cluster.
package collect

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/johnahull/k8s-dra-inspect/internal/model"
)

var vmiGVR = schema.GroupVersionResource{Group: "kubevirt.io", Version: "v1", Resource: "virtualmachineinstances"}

// Scope limits namespaced collection; empty Namespace means all namespaces.
type Scope struct{ Namespace string }

// Live collects a Snapshot from a cluster using only get/list calls.
type Live struct {
	Client    kubernetes.Interface
	Dynamic   dynamic.Interface
	Discovery discovery.DiscoveryInterface
	Now       func() time.Time
	Scope     Scope
}

var _ model.Source = (*Live)(nil)

// NewClients builds a Live from a kubeconfig path (empty for the default
// loading rules) and context name.
func NewClients(kubeconfig, kubeContext string, timeout time.Duration) (*Live, error) {
	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	rules.ExplicitPath = kubeconfig
	cfg, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		rules, &clientcmd.ConfigOverrides{CurrentContext: kubeContext}).ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("load kubeconfig: %w", err)
	}
	cfg.Timeout = timeout
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, err
	}
	dyn, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return nil, err
	}
	return &Live{Client: cs, Dynamic: dyn, Discovery: cs.Discovery(), Now: time.Now}, nil
}

// Snapshot implements model.Source using the configured Scope.
func (l *Live) Snapshot(ctx context.Context) (model.Snapshot, error) {
	return l.Collect(ctx, l.Scope)
}

// Collect lists every resource type. A failing resource never aborts the run:
// it stays empty and Collection records why.
func (l *Live) Collect(ctx context.Context, scope Scope) (model.Snapshot, error) {
	snap := model.Snapshot{
		SchemaVersion: model.SchemaVersion,
		GeneratedAt:   l.Now(),
		Source:        "live",
		Collection:    map[string]model.CollectionStatus{},
		Evidence:      map[string]json.RawMessage{},
	}
	rc := l.Client.ResourceV1()
	ns := scope.Namespace

	if list, err := rc.ResourceSlices().List(ctx, metav1.ListOptions{}); record(&snap, "resourceslices", err) {
		snap.ResourceSlices = list.Items
	}
	if list, err := rc.DeviceClasses().List(ctx, metav1.ListOptions{}); record(&snap, "deviceclasses", err) {
		snap.DeviceClasses = list.Items
	}
	if list, err := rc.ResourceClaims(ns).List(ctx, metav1.ListOptions{}); record(&snap, "resourceclaims", err) {
		snap.ResourceClaims = list.Items
	}
	if list, err := l.Client.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{}); record(&snap, "pods", err) {
		for _, p := range list.Items {
			snap.Pods = append(snap.Pods, model.NewPodRef(p))
		}
	}
	if list, err := l.Client.CoreV1().Nodes().List(ctx, metav1.ListOptions{}); record(&snap, "nodes", err) {
		snap.Nodes = list.Items
	}
	l.collectVMIs(ctx, &snap, ns)

	snap.Normalize()
	return snap, nil
}

func (l *Live) collectVMIs(ctx context.Context, snap *model.Snapshot, ns string) {
	const key = "virtualmachineinstances"
	_, err := l.Discovery.ServerResourcesForGroupVersion("kubevirt.io/v1")
	if apierrors.IsNotFound(err) {
		snap.Collection[key] = model.CollectionStatus{State: model.StateNotFound, Reason: "kubevirt.io/v1 not served"}
		return
	}
	if !record(snap, key, err) {
		return
	}
	list, err := l.Dynamic.Resource(vmiGVR).Namespace(ns).List(ctx, metav1.ListOptions{})
	if record(snap, key, err) {
		snap.VMIs = list.Items
	}
}

// record stores the outcome for key and reports whether the list succeeded.
func record(snap *model.Snapshot, key string, err error) bool {
	st := model.CollectionStatus{State: model.StateOK}
	switch {
	case err == nil:
	case apierrors.IsForbidden(err):
		st = model.CollectionStatus{State: model.StateForbidden, Reason: err.Error()}
	case apierrors.IsNotFound(err):
		st = model.CollectionStatus{State: model.StateNotFound, Reason: err.Error()}
	default:
		st = model.CollectionStatus{State: model.StateError, Reason: err.Error()}
	}
	snap.Collection[key] = st
	return err == nil
}
