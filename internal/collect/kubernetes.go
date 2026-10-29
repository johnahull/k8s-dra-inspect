// Package collect gathers a read-only Snapshot from a live cluster.
package collect

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	resourcev1 "k8s.io/api/resource/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/pager"

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
		Source:        model.SourceLive,
		Collection:    map[string]model.CollectionStatus{},
		Evidence:      map[string]json.RawMessage{},
	}
	rc := l.Client.ResourceV1()
	ns := scope.Namespace

	snap.Namespace = ns

	if items, err := listAll(ctx, rc.ResourceSlices().List, func(l *resourcev1.ResourceSliceList) []resourcev1.ResourceSlice { return l.Items }); record(&snap, "resourceslices", err) {
		snap.ResourceSlices = items
	}
	if items, err := listAll(ctx, rc.DeviceClasses().List, func(l *resourcev1.DeviceClassList) []resourcev1.DeviceClass { return l.Items }); record(&snap, "deviceclasses", err) {
		snap.DeviceClasses = items
	}
	if items, err := listAll(ctx, rc.ResourceClaims(ns).List, func(l *resourcev1.ResourceClaimList) []resourcev1.ResourceClaim { return l.Items }); record(&snap, "resourceclaims", err) {
		snap.ResourceClaims = items
		scoped(&snap, "resourceclaims", ns)
	}
	if items, err := listAll(ctx, l.Client.CoreV1().Pods(ns).List, func(l *corev1.PodList) []corev1.Pod { return l.Items }); record(&snap, "pods", err) {
		for i := range items {
			snap.Pods = append(snap.Pods, model.NewPodRef(items[i]))
		}
		scoped(&snap, "pods", ns)
	}
	if items, err := listAll(ctx, l.Client.CoreV1().Nodes().List, func(l *corev1.NodeList) []corev1.Node { return l.Items }); record(&snap, "nodes", err) {
		snap.Nodes = items
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
		scoped(snap, key, ns)
	}
}

const pageSize = 500

// listAll pages through a list call and returns every item.
func listAll[L runtime.Object, T any](ctx context.Context, list func(context.Context, metav1.ListOptions) (L, error), items func(L) []T) ([]T, error) {
	p := pager.New(func(ctx context.Context, o metav1.ListOptions) (runtime.Object, error) { return list(ctx, o) })
	p.PageSize = pageSize
	obj, _, err := p.List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	return items(obj.(L)), nil
}

// scoped notes on an ok entry that only one namespace was listed.
func scoped(snap *model.Snapshot, key, ns string) {
	if ns != "" {
		snap.Collection[key] = model.CollectionStatus{State: model.StateOK, Reason: "namespace " + ns + " only"}
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
