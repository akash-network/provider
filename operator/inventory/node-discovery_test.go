package inventory

import (
	"testing"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/api/resource"
)

func TestSanitizeGPUQuantity(t *testing.T) {
	log := logr.Discard()

	assert.Equal(t, int64(10000), sanitizeGPUQuantity(log, "node1", "nvidia.com/gpu", 10000))
	assert.Equal(t, int64(0), sanitizeGPUQuantity(log, "node1", "nvidia.com/gpu", -1))
	assert.Equal(t, int64(0), sanitizeGPUQuantity(log, "node1", "nvidia.com/gpu", -1000))
}

func TestSubAllocatedNLZ_underflow_clamped(t *testing.T) {
	log := logr.Discard()

	allocated := resource.NewQuantity(2, resource.DecimalSI)
	val := resource.NewQuantity(5, resource.DecimalSI)

	subAllocatedNLZ(log, "node1", "nvidia.com/gpu", allocated, *val)

	require.Equal(t, int64(0), allocated.Value(), "allocated underflow must be clamped to 0")
}
