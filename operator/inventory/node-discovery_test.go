package inventory

import (
	"encoding/json"
	"testing"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8stypes "k8s.io/apimachinery/pkg/types"
	v1 "pkg.akt.dev/go/inventory/v1"

	"github.com/akash-network/provider/cluster/kube/builder"
)

type capturedLogEntry struct {
	err    error
	msg    string
	values []interface{}
}

type captureSink struct {
	entries *[]capturedLogEntry
	values  []interface{}
}

func (s *captureSink) Init(logr.RuntimeInfo) {}

func (s *captureSink) Enabled(int) bool {
	return true
}

func (s *captureSink) Info(int, string, ...interface{}) {}

func (s *captureSink) Error(err error, msg string, keysAndValues ...interface{}) {
	values := append([]interface{}{}, s.values...)
	values = append(values, keysAndValues...)
	*s.entries = append(*s.entries, capturedLogEntry{
		err:    err,
		msg:    msg,
		values: values,
	})
}

func (s *captureSink) WithValues(keysAndValues ...interface{}) logr.LogSink {
	values := append([]interface{}{}, s.values...)
	values = append(values, keysAndValues...)

	return &captureSink{
		entries: s.entries,
		values:  values,
	}
}

func (s *captureSink) WithName(string) logr.LogSink {
	return &captureSink{
		entries: s.entries,
		values:  append([]interface{}{}, s.values...),
	}
}

func logValues(values []interface{}) map[string]interface{} {
	result := make(map[string]interface{})
	for i := 0; i+1 < len(values); i += 2 {
		key, ok := values[i].(string)
		if !ok {
			continue
		}
		result[key] = values[i+1]
	}
	return result
}

func TestSubPodAllocatedResourcesLogsAndKeepsNegative(t *testing.T) {
	tests := []struct {
		name            string
		resourceName    corev1.ResourceName
		requestQuantity string
		changeResource  string
		before          string
		subtracted      string
		after           string
		setAllocated    func(*v1.Node)
		allocated       func(*v1.Node) int64
	}{
		{
			name:            "cpu",
			resourceName:    corev1.ResourceCPU,
			requestQuantity: "750m",
			changeResource:  "cpu",
			before:          "500m",
			subtracted:      "750m",
			after:           "-250m",
			setAllocated: func(node *v1.Node) {
				node.Resources.CPU.Quantity.Allocated.SetMilli(500)
			},
			allocated: func(node *v1.Node) int64 {
				return node.Resources.CPU.Quantity.Allocated.MilliValue()
			},
		},
		{
			name:            "gpu",
			resourceName:    builder.ResourceGPUNvidia,
			requestQuantity: "2",
			changeResource:  string(builder.ResourceGPUNvidia),
			before:          "1",
			subtracted:      "2",
			after:           "-1",
			setAllocated: func(node *v1.Node) {
				node.Resources.GPU.Quantity.Allocated.Set(1)
			},
			allocated: func(node *v1.Node) int64 {
				return node.Resources.GPU.Quantity.Allocated.Value()
			},
		},
		{
			name:            "memory",
			resourceName:    corev1.ResourceMemory,
			requestQuantity: "2",
			changeResource:  "memory",
			before:          "1",
			subtracted:      "2",
			after:           "-1",
			setAllocated: func(node *v1.Node) {
				node.Resources.Memory.Quantity.Allocated.Set(1)
			},
			allocated: func(node *v1.Node) int64 {
				return node.Resources.Memory.Quantity.Allocated.Value()
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			node := &v1.Node{
				Name: "node1",
				Resources: v1.NodeResources{
					CPU: v1.CPU{
						Quantity: v1.NewResourcePairMilli(1000, 1000, 0, resource.DecimalSI),
					},
					Memory: v1.Memory{
						Quantity: v1.NewResourcePair(1024, 1024, 0, resource.DecimalSI),
					},
					GPU: v1.GPU{
						Quantity: v1.NewResourcePair(4, 4, 0, resource.DecimalSI),
					},
					EphemeralStorage: v1.NewResourcePair(100, 100, 0, resource.DecimalSI),
					VolumesAttached:  v1.NewResourcePair(0, 0, 0, resource.DecimalSI),
					VolumesMounted:   v1.NewResourcePair(0, 0, 0, resource.DecimalSI),
				},
			}
			tt.setAllocated(node)

			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "lease-ns",
					Name:      "web-0",
					UID:       k8stypes.UID("pod-uid"),
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name: "web",
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									tt.resourceName: resource.MustParse(tt.requestQuantity),
								},
							},
						},
					},
				},
			}
			entries := make([]capturedLogEntry, 0, 1)
			log := logr.New(&captureSink{entries: &entries})

			subPodAllocatedResources(log, node, pod)

			require.Negative(t, tt.allocated(node))
			require.Len(t, entries, 1)
			require.ErrorIs(t, entries[0].err, errNegativeAllocatedResource)

			values := logValues(entries[0].values)
			require.Equal(t, "node1", values["node"])
			require.Equal(t, "lease-ns/web-0", values["pod"])
			require.Equal(t, "pod-uid", values["pod_id"])

			var before v1.Node
			require.NoError(t, json.Unmarshal([]byte(values["inventory_before"].(string)), &before))
			require.Positive(t, tt.allocated(&before))

			var change allocatedResourceChange
			require.NoError(t, json.Unmarshal([]byte(values["change"].(string)), &change))
			require.Equal(t, "node1", change.Node)
			require.Equal(t, "lease-ns/web-0", change.Pod)
			require.Equal(t, "pod-uid", change.PodID)
			require.Equal(t, tt.changeResource, change.Resource)
			require.Equal(t, tt.before, change.AllocatedBefore)
			require.Equal(t, tt.subtracted, change.Subtracted)
			require.Equal(t, tt.after, change.AllocatedAfter)
		})
	}
}
