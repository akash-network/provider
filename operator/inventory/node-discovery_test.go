package inventory

import (
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"

	v1 "pkg.akt.dev/go/inventory/v1"

	"github.com/akash-network/provider/cluster/kube/builder"
)

func readyNode(taints ...corev1.Taint) *corev1.Node {
	return &corev1.Node{
		Status: corev1.NodeStatus{
			Conditions: []corev1.NodeCondition{
				{Type: corev1.NodeReady, Status: corev1.ConditionTrue},
			},
		},
		Spec: corev1.NodeSpec{Taints: taints},
	}
}

func TestGenerateLabels_Taints(t *testing.T) {
	tests := []struct {
		name     string
		node     *corev1.Node
		excluded bool
	}{
		{
			name:     "ready, untainted node is included",
			node:     readyNode(),
			excluded: false,
		},
		{
			name:     "NoSchedule taint excludes the node",
			node:     readyNode(corev1.Taint{Key: "node-role.kubernetes.io/control-plane", Effect: corev1.TaintEffectNoSchedule}),
			excluded: true,
		},
		{
			name:     "NoExecute taint excludes the node",
			node:     readyNode(corev1.Taint{Key: "CriticalAddonsOnly", Effect: corev1.TaintEffectNoExecute}),
			excluded: true,
		},
		{
			name:     "PreferNoSchedule taint does not exclude the node",
			node:     readyNode(corev1.Taint{Key: "soft", Effect: corev1.TaintEffectPreferNoSchedule}),
			excluded: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			labels, _ := generateLabels(Config{}, tt.node, v1.Node{}, storageClasses{})

			_, managed := labels[builder.AkashManagedLabelName]
			if tt.excluded {
				assert.False(t, managed, "tainted node must not be marked akash-managed")
			} else {
				assert.True(t, managed, "schedulable node must be marked akash-managed")
			}
		})
	}
}
