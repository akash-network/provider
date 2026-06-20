package inventory

import (
	"testing"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

func TestSanitizeResourceQuantity(t *testing.T) {
	log := logr.Discard()

	assert.Equal(t, int64(10000), sanitizeResourceQuantity(log, "node1", "nvidia.com/gpu", 10000))
	assert.Equal(t, int64(0), sanitizeResourceQuantity(log, "node1", "nvidia.com/gpu", -1))
	assert.Equal(t, int64(0), sanitizeResourceQuantity(log, "node1", "memory", -1000))
	assert.Equal(t, int64(0), sanitizeResourceQuantity(log, "node1", "cpu", 0))
}

func TestSubAllocatedNLZ_underflow_clamped(t *testing.T) {
	log := logr.Discard()

	allocated := resource.NewQuantity(2, resource.DecimalSI)
	val := resource.NewQuantity(5, resource.DecimalSI)

	subAllocatedNLZ(log, "node1", "nvidia.com/gpu", allocated, *val)

	require.Equal(t, int64(0), allocated.Value(), "allocated underflow must be clamped to 0")
}

func TestNodeAllocatableChanged_detectsMilliCPUChange(t *testing.T) {
	prev := &corev1.Node{
		Status: corev1.NodeStatus{
			Allocatable: corev1.ResourceList{
				corev1.ResourceCPU: resource.MustParse("500m"),
			},
		},
	}
	curr := &corev1.Node{
		Status: corev1.NodeStatus{
			Allocatable: corev1.ResourceList{
				corev1.ResourceCPU: resource.MustParse("750m"),
			},
		},
	}

	require.True(t, nodeAllocatableChanged(prev, curr))
}
