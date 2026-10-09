//go:build e2e

package integration

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"

	"github.com/akash-network/provider/cluster/kube/builder"
)

var disruptionReasons = map[string]struct{}{
	"Killing":          {},
	"BackOff":          {},
	"Unhealthy":        {},
	"FailedMount":      {},
	"Evicted":          {},
	"SuccessfulDelete": {},
}

type PodState struct {
	uid           string
	restartCounts map[string]int32
}

type Disruption struct {
	namespace string
	pod       string
	kind      string // "pod-replaced" | "container-restart" | "deleted" | "event"
	reason    string
}

// Observer detects whether a provider restart disturbed the pods in a lease namespace.
// Two invariants: Compare (an end-of-window List diff) is the authoritative verdict and
// never depends on the watch; and only pod-level facts fail the gate, while Events are
// attribution only (see AssertNoDisruption).
type Observer struct {
	namespace   string
	baseline    map[string]PodState // key: pod name
	disruptions []Disruption
	events      []Disruption
	seen        map[string]struct{}
}

func NewObserver(namespace string) *Observer {
	return &Observer{
		namespace: namespace,
		baseline:  map[string]PodState{},
		seen:      map[string]struct{}{},
	}
}

func (o *Observer) selector() string {
	return builder.AkashManagedLabelName + "=true"
}

func (o *Observer) Snapshot(ctx context.Context, kube kubernetes.Interface) error {
	pods, err := kube.CoreV1().Pods(o.namespace).List(ctx, metav1.ListOptions{
		LabelSelector: o.selector(),
	})
	if err != nil {
		return err
	}

	for i := range pods.Items {
		pod := &pods.Items[i]
		o.baseline[pod.Name] = PodState{
			uid:           string(pod.UID),
			restartCounts: containerRestartCounts(pod),
		}
	}

	return nil
}

// Watch records pod and event streams in one goroutine until ctx is cancelled.
func (o *Observer) Watch(ctx context.Context, kube kubernetes.Interface) error {
	podWatch, err := kube.CoreV1().Pods(o.namespace).Watch(ctx, metav1.ListOptions{
		LabelSelector: o.selector(),
	})
	if err != nil {
		return err
	}
	// Closure form: podWatch is reassigned on re-establish below.
	defer func() { podWatch.Stop() }()

	// Events are attribution only, so failing to start their watch must not fail the gate.
	var eventCh <-chan watch.Event
	if eventWatch, err := kube.CoreV1().Events(o.namespace).Watch(ctx, metav1.ListOptions{}); err != nil {
		fmt.Fprintf(os.Stderr, "[observer] cannot watch events for %s: %v; continuing with pod-watch only\n", o.namespace, err)
	} else {
		defer eventWatch.Stop()
		eventCh = eventWatch.ResultChan()
	}

	podCh := podWatch.ResultChan()

	// Re-establish the pod watch if the apiserver closes it, backed off and capped so a
	// persistently-closing apiserver cannot spin hot. Delivering an event resets the
	// counter, so the cap trips only on a storm, not on closes spread across the window.
	const (
		reconnectBackoff = time.Second
		maxReconnects    = 30
	)
	reconnects := 0

	for {
		select {
		case <-ctx.Done():
			return nil
		case ev, ok := <-podCh:
			if !ok {
				// A close racing our own ctx cancel is a normal end of window.
				if ctx.Err() != nil {
					return nil
				}
				reconnects++
				if reconnects > maxReconnects {
					fmt.Fprintf(os.Stderr, "[observer] pod watch for %s closed %d times; giving up, Compare remains authoritative\n", o.namespace, reconnects)
					return nil
				}
				select {
				case <-ctx.Done():
					return nil
				case <-time.After(reconnectBackoff):
				}
				pw, err := kube.CoreV1().Pods(o.namespace).Watch(ctx, metav1.ListOptions{
					LabelSelector: o.selector(),
				})
				if err != nil {
					if ctx.Err() != nil {
						return nil
					}
					return fmt.Errorf("re-establishing pod watch: %w", err)
				}
				fmt.Fprintf(os.Stderr, "[observer] pod watch for %s re-established (attempt %d)\n", o.namespace, reconnects)
				podWatch.Stop()
				podWatch = pw
				podCh = pw.ResultChan()
				continue
			}
			reconnects = 0
			o.handlePodEvent(ev)
		case ev, ok := <-eventCh:
			if !ok {
				fmt.Fprintf(os.Stderr, "[observer] event watch for %s closed; continuing with pod-watch only\n", o.namespace)
				eventCh = nil
				continue
			}
			o.handleClusterEvent(ev)
		}
	}
}

func (o *Observer) handlePodEvent(ev watch.Event) {
	pod, ok := ev.Object.(*corev1.Pod)
	if !ok {
		return
	}

	base, known := o.baseline[pod.Name]

	if !o.knownUID(string(pod.UID)) {
		o.record(pod.Name, "pod-replaced", "")
		return
	}

	if ev.Type == watch.Deleted || pod.DeletionTimestamp != nil {
		o.record(pod.Name, "deleted", "")
	}

	if known {
		for _, cs := range pod.Status.ContainerStatuses {
			if cs.RestartCount > base.restartCounts[cs.Name] {
				o.record(pod.Name, "container-restart", terminationReason(cs))
			}
		}
	}
}

func (o *Observer) handleClusterEvent(ev watch.Event) {
	event, ok := ev.Object.(*corev1.Event)
	if !ok {
		return
	}

	if event.InvolvedObject.Kind != "Pod" {
		return
	}

	if _, ok := disruptionReasons[event.Reason]; !ok {
		return
	}

	key := "event|" + event.InvolvedObject.Name + "|" + event.Reason
	if _, ok := o.seen[key]; ok {
		return
	}
	o.seen[key] = struct{}{}
	o.events = append(o.events, Disruption{
		namespace: o.namespace,
		pod:       event.InvolvedObject.Name,
		kind:      "event",
		reason:    strings.TrimSpace(event.Reason + " " + event.Message),
	})
}

// Compare diffs the namespace against the baseline after the window. It is the
// authoritative verdict: catches a persistent roll even if the watch missed it.
func (o *Observer) Compare(ctx context.Context, kube kubernetes.Interface) error {
	pods, err := kube.CoreV1().Pods(o.namespace).List(ctx, metav1.ListOptions{
		LabelSelector: o.selector(),
	})
	if err != nil {
		return err
	}

	current := map[string]string{} // pod name -> uid
	for i := range pods.Items {
		pod := &pods.Items[i]
		current[pod.Name] = string(pod.UID)

		counts := containerRestartCounts(pod)
		base, known := o.baseline[pod.Name]
		if !known || base.uid != string(pod.UID) {
			o.record(pod.Name, "pod-replaced", "absent from baseline after restart")
			continue
		}
		for name, count := range counts {
			if count > base.restartCounts[name] {
				o.record(pod.Name, "container-restart",
					fmt.Sprintf("%s restarts %d -> %d", name, base.restartCounts[name], count))
			}
		}
	}

	for name := range o.baseline {
		// A name reappearing under a new UID is already a "pod-replaced" above; only a
		// fully-absent name is a deletion. Guarding on absence avoids double-reporting.
		if _, ok := current[name]; !ok {
			o.record(name, "deleted", "baseline pod gone after restart")
		}
	}

	return nil
}

func (o *Observer) record(pod, kind, reason string) {
	key := pod + "|" + kind
	if _, ok := o.seen[key]; ok {
		return
	}
	o.seen[key] = struct{}{}

	o.disruptions = append(o.disruptions, Disruption{
		namespace: o.namespace,
		pod:       pod,
		kind:      kind,
		reason:    reason,
	})
}

func (o *Observer) knownUID(uid string) bool {
	for _, state := range o.baseline {
		if state.uid == uid {
			return true
		}
	}
	return false
}

func (o *Observer) AssertNoDisruption(t *testing.T) {
	t.Helper()

	if len(o.disruptions) == 0 {
		return
	}

	var b strings.Builder
	for _, d := range o.disruptions {
		fmt.Fprintf(&b, "\n  ns=%s pod=%s kind=%s reason=%q", d.namespace, d.pod, d.kind, d.reason)
	}
	if len(o.events) > 0 {
		b.WriteString("\n  correlated events:")
		for _, e := range o.events {
			fmt.Fprintf(&b, "\n    pod=%s reason=%q", e.pod, e.reason)
		}
	}

	require.Fail(t, "workload disrupted during the provider event", b.String())
}

func containerRestartCounts(pod *corev1.Pod) map[string]int32 {
	counts := map[string]int32{}
	for _, cs := range pod.Status.ContainerStatuses {
		counts[cs.Name] = cs.RestartCount
	}
	return counts
}

func terminationReason(cs corev1.ContainerStatus) string {
	if term := cs.LastTerminationState.Terminated; term != nil {
		return fmt.Sprintf("%s (exit %d)", term.Reason, term.ExitCode)
	}
	return ""
}
