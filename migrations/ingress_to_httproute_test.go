package migrations

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIngressToHTTPRouteMigration_Name(t *testing.T) {
	m := NewIngressToHTTPRouteMigration()
	assert.Equal(t, "ingress-to-httproute-001", m.Name())
}

func TestIngressToHTTPRouteMigration_Description(t *testing.T) {
	m := NewIngressToHTTPRouteMigration()
	assert.Contains(t, m.Description(), "Ingress")
	assert.Contains(t, m.Description(), "HTTPRoute")
}

func TestIngressToHTTPRouteMigration_FromVersion(t *testing.T) {
	m := NewIngressToHTTPRouteMigration()
	assert.Equal(t, "0.6.5", m.FromVersion())
}

func TestIngressToHTTPRouteMigration_ConvertAnnotations(t *testing.T) {
	m := &ingressToHTTPRouteMigration{}

	tests := []struct {
		name               string
		ingressAnnotations map[string]string
		expectedKeys       []string
		expectedValues     map[string]string
	}{
		{
			name: "converts timeout annotations",
			ingressAnnotations: map[string]string{
				"nginx.ingress.kubernetes.io/proxy-read-timeout": "120",
				"nginx.ingress.kubernetes.io/proxy-send-timeout": "60",
			},
			expectedKeys: []string{
				"nginx.org/proxy-read-timeout",
				"nginx.org/proxy-send-timeout",
			},
			expectedValues: map[string]string{
				"nginx.org/proxy-read-timeout": "120",
				"nginx.org/proxy-send-timeout": "60",
			},
		},
		{
			name: "converts body size annotation",
			ingressAnnotations: map[string]string{
				"nginx.ingress.kubernetes.io/proxy-body-size": "10m",
			},
			expectedKeys: []string{
				"nginx.org/client-max-body-size",
			},
			expectedValues: map[string]string{
				"nginx.org/client-max-body-size": "10m",
			},
		},
		{
			name: "converts next upstream annotations",
			ingressAnnotations: map[string]string{
				"nginx.ingress.kubernetes.io/proxy-next-upstream-tries":   "5",
				"nginx.ingress.kubernetes.io/proxy-next-upstream-timeout": "30",
				"nginx.ingress.kubernetes.io/proxy-next-upstream":         "error timeout http_500",
			},
			expectedKeys: []string{
				"nginx.org/proxy-next-upstream-tries",
				"nginx.org/proxy-next-upstream-timeout",
				"nginx.org/proxy-next-upstream",
			},
			expectedValues: map[string]string{
				"nginx.org/proxy-next-upstream-tries":   "5",
				"nginx.org/proxy-next-upstream-timeout": "30s",
				"nginx.org/proxy-next-upstream":         "error timeout http_500",
			},
		},
		{
			name:               "uses defaults for missing annotations",
			ingressAnnotations: map[string]string{},
			expectedKeys: []string{
				"nginx.org/proxy-read-timeout",
				"nginx.org/proxy-send-timeout",
				"nginx.org/client-max-body-size",
				"nginx.org/proxy-next-upstream-tries",
			},
			expectedValues: map[string]string{
				"nginx.org/proxy-read-timeout":        "60",
				"nginx.org/proxy-send-timeout":        "60",
				"nginx.org/client-max-body-size":      "1m",
				"nginx.org/proxy-next-upstream-tries": "3",
			},
		},
		{
			name: "skips zero timeout",
			ingressAnnotations: map[string]string{
				"nginx.ingress.kubernetes.io/proxy-next-upstream-timeout": "0",
			},
			expectedKeys: []string{
				"nginx.org/proxy-read-timeout",
				"nginx.org/proxy-send-timeout",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := m.convertAnnotations(tt.ingressAnnotations)

			for _, key := range tt.expectedKeys {
				_, exists := result[key]
				assert.True(t, exists, "expected key %s to exist", key)
			}

			for key, expectedValue := range tt.expectedValues {
				assert.Equal(t, expectedValue, result[key], "unexpected value for key %s", key)
			}
		})
	}
}

func TestIngressToHTTPRouteMigration_GetAnnotationInt(t *testing.T) {
	m := &ingressToHTTPRouteMigration{}

	tests := []struct {
		name         string
		annotations  map[string]string
		key          string
		defaultValue int
		expected     int
	}{
		{
			name:         "returns integer value",
			annotations:  map[string]string{"key": "42"},
			key:          "key",
			defaultValue: 10,
			expected:     42,
		},
		{
			name:         "returns default for missing key",
			annotations:  map[string]string{},
			key:          "key",
			defaultValue: 10,
			expected:     10,
		},
		{
			name:         "handles float values",
			annotations:  map[string]string{"key": "42.7"},
			key:          "key",
			defaultValue: 10,
			expected:     43,
		},
		{
			name:         "returns default for invalid value",
			annotations:  map[string]string{"key": "invalid"},
			key:          "key",
			defaultValue: 10,
			expected:     10,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := m.getAnnotationInt(tt.annotations, tt.key, tt.defaultValue)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestIngressToHTTPRouteMigration_GetAnnotationValue(t *testing.T) {
	m := &ingressToHTTPRouteMigration{}

	tests := []struct {
		name         string
		annotations  map[string]string
		key          string
		defaultValue string
		expected     string
	}{
		{
			name:         "returns existing value",
			annotations:  map[string]string{"key": "value"},
			key:          "key",
			defaultValue: "default",
			expected:     "value",
		},
		{
			name:         "returns default for missing key",
			annotations:  map[string]string{},
			key:          "key",
			defaultValue: "default",
			expected:     "default",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := m.getAnnotationValue(tt.annotations, tt.key, tt.defaultValue)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestIngressToHTTPRouteMigration_BuildHTTPRoute(t *testing.T) {
	m := &ingressToHTTPRouteMigration{}

	labels := map[string]string{
		"akash.network": "true",
	}
	annotations := map[string]string{
		"nginx.ingress.kubernetes.io/proxy-read-timeout": "120",
	}

	route := m.buildHTTPRoute(
		"test-route",
		labels,
		annotations,
		"test-gateway",
		"gateway-ns",
		"example.com",
		"backend-svc",
		8080,
	)

	require.NotNil(t, route)
	assert.Equal(t, "test-route", route.Name)
	assert.Equal(t, "HTTPRoute", route.Kind)
	assert.Equal(t, "gateway.networking.k8s.io/v1", route.APIVersion)

	require.Len(t, route.Spec.ParentRefs, 1)
	assert.Equal(t, "test-gateway", string(route.Spec.ParentRefs[0].Name))
	assert.Equal(t, "gateway-ns", string(*route.Spec.ParentRefs[0].Namespace))

	require.Len(t, route.Spec.Hostnames, 1)
	assert.Equal(t, "example.com", string(route.Spec.Hostnames[0]))

	require.Len(t, route.Spec.Rules, 1)
	require.Len(t, route.Spec.Rules[0].BackendRefs, 1)
	assert.Equal(t, "backend-svc", string(route.Spec.Rules[0].BackendRefs[0].Name))
	assert.Equal(t, int32(8080), int32(*route.Spec.Rules[0].BackendRefs[0].Port))

	assert.Equal(t, "120", route.Annotations["nginx.org/proxy-read-timeout"])
}

func TestIngressToHTTPRouteMigration_Registered(t *testing.T) {
	m := Get("ingress-to-httproute-001")
	require.NotNil(t, m)
	assert.Equal(t, "ingress-to-httproute-001", m.Name())
}
