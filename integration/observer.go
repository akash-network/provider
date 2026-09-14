//go:build e2e

package integration

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

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

// Observer records a baseline of the pods in a lease namespace and detects whether a
// provider restart disturbed them. Compare, an end-of-window List diffed against the
// baseline, is the authoritative verdict and does not depend on the watch, so a dead
// or truncated watch cannot turn a real roll into a passing gate.
//
// Only pod-level facts (a new pod UID, a bumped restart count, a deletion) count as a
// disruption and fail the gate. Namespace Events are attribution only: a Killing or
// Unhealthy event annotates a real roll but never fails the gate on its own, because
// a readiness blip or an event replayed from before the window is not a rolled
// workload.
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

// Watch multiplexes a pod watch and an event watch in a single goroutine, so both
// record without a lock. It runs until ctx is cancelled. A pod watch closing early is
// a hard error (that stream is the verdict, so losing it must fail the gate loudly),
// while an event watch closing is tolerated (events are only attribution).
func (o *Observer) Watch(ctx context.Context, kube kubernetes.Interface) error {
	podWatch, err := kube.CoreV1().Pods(o.namespace).Watch(ctx, metav1.ListOptions{
		LabelSelector: o.selector(),
	})
	if err != nil {
		return err
	}
	defer podWatch.Stop()

	eventWatch, err := kube.CoreV1().Events(o.namespace).Watch(ctx, metav1.ListOptions{})
	if err != nil {
		return err
	}
	defer eventWatch.Stop()

	podCh := podWatch.ResultChan()
	eventCh := eventWatch.ResultChan()

	for {
		select {
		case <-ctx.Done():
			return nil
		case ev, ok := <-podCh:
			if !ok {
				return fmt.Errorf("pod watch closed before the observation window ended")
			}
			o.handlePodEvent(ev)
		case ev, ok := <-eventCh:
			if !ok {
				// Events are best-effort attribution, not the verdict, so an
				// apiserver-side close of this stream must not fail the gate.
				// Drop it and keep the authoritative pod-watch coverage.
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

	// Attribution only: record notable pod events so a real roll can be explained,
	// but never let an event fail the gate (see AssertNoDisruption). Non-pod objects
	// (Ingresses, Services the hostname operator churns) are irrelevant here.
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

// Compare lists the namespace after the observation window and diffs it against the
// baseline. This is the authoritative check: independent of the watch, it catches a
// persistent roll even if the watch closed or missed the transition.
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

	for name, base := range o.baseline {
		if uid, ok := current[name]; !ok || uid != base.uid {
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
