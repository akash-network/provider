package inventory

import (
	"testing"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
)

func TestSanitizeGPUQuantity(t *testing.T) {
	log := logr.Discard()

	// positive values should be returned as is
	assert.Equal(t, int64(10000), sanitizeGPUQuantity(log, "node1", "nvidia.com/gpu", 10000))

	// negative values should be clamped to 0
	assert.Equal(t, int64(0), sanitizeGPUQuantity(log, "node1", "nvidia.com/gpu", -1))
	assert.Equal(t, int64(0), sanitizeGPUQuantity(log, "node1", "nvidia.com/gpu", -1000))
}
