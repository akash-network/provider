package inventory

import (
	"testing"

	types "github.com/akash-network/akash-api/go/node/types/v1beta3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseCPUAttributes(t *testing.T) {
	tests := []struct {
		name     string
		attrs    types.Attributes
		expected CPUAttributes
		wantErr  bool
	}{
		{
			name: "valid CPU architecture attributes",
			attrs: types.Attributes{
				{
					Key:   "capabilities/cpu/arch/arm64",
					Value: "true",
				},
				{
					Key:   "capabilities/cpu/arch/amd64",
					Value: "true",
				},
			},
			expected: CPUAttributes{
				"cpu": &CPUArchitectureAttributes{
					Architectures: []string{"arm64", "amd64"},
				},
			},
			wantErr: false,
		},
		{
			name: "invalid CPU attribute format",
			attrs: types.Attributes{
				{
					Key:   "invalid/format",
					Value: "true",
				},
			},
			expected: CPUAttributes{},
			wantErr:  true,
		},
		{
			name:     "empty attributes",
			attrs:    types.Attributes{},
			expected: CPUAttributes{},
			wantErr:  false,
		},
		{
			name: "false value should be ignored",
			attrs: types.Attributes{
				{
					Key:   "capabilities/cpu/arch/arm64",
					Value: "false",
				},
			},
			expected: CPUAttributes{},
			wantErr:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ParseCPUAttributes(tt.attrs)

			if tt.wantErr {
				assert.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestCPUArchitectureAttributes_HasArchitecture(t *testing.T) {
	attrs := &CPUArchitectureAttributes{
		Architectures: []string{"arm64", "amd64"},
	}

	assert.True(t, attrs.HasArchitecture("arm64"))
	assert.True(t, attrs.HasArchitecture("amd64"))
	assert.False(t, attrs.HasArchitecture("x86"))

	// Test wildcard support
	wildcardAttrs := &CPUArchitectureAttributes{
		Architectures: []string{"*"},
	}
	assert.True(t, wildcardAttrs.HasArchitecture("arm64"))
	assert.True(t, wildcardAttrs.HasArchitecture("amd64"))
	assert.True(t, wildcardAttrs.HasArchitecture("x86"))
}

func TestParseCPUAttributesFromSDL(t *testing.T) {
	tests := []struct {
		name     string
		attrs    map[string]interface{}
		expected CPUAttributes
		wantErr  bool
	}{
		{
			name: "valid SDL architecture array",
			attrs: map[string]interface{}{
				"arch": []interface{}{"arm64", "amd64"},
			},
			expected: CPUAttributes{
				"cpu": &CPUArchitectureAttributes{
					Architectures: []string{"arm64", "amd64"},
				},
			},
			wantErr: false,
		},
		{
			name: "single architecture as string",
			attrs: map[string]interface{}{
				"arch": "arm64",
			},
			expected: CPUAttributes{
				"cpu": &CPUArchitectureAttributes{
					Architectures: []string{"arm64"},
				},
			},
			wantErr: false,
		},
		{
			name: "no arch attribute",
			attrs: map[string]interface{}{
				"other": "value",
			},
			expected: CPUAttributes{},
			wantErr:  false,
		},
		{
			name: "invalid architecture type",
			attrs: map[string]interface{}{
				"arch": []interface{}{123, "amd64"}, // number instead of string
			},
			expected: CPUAttributes{},
			wantErr:  true,
		},
		{
			name: "invalid architecture format",
			attrs: map[string]interface{}{
				"arch": 123, // number instead of string or array
			},
			expected: CPUAttributes{},
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ParseCPUAttributesFromSDL(tt.attrs)

			if tt.wantErr {
				assert.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestConvertSDLCPUAttributesToKeyValue(t *testing.T) {
	tests := []struct {
		name     string
		sdlAttrs map[string]interface{}
		expected types.Attributes
		wantErr  bool
	}{
		{
			name: "valid SDL architecture array",
			sdlAttrs: map[string]interface{}{
				"arch": []interface{}{"arm64", "amd64"},
			},
			expected: types.Attributes{
				{
					Key:   "capabilities/cpu/arch/arm64",
					Value: "true",
				},
				{
					Key:   "capabilities/cpu/arch/amd64",
					Value: "true",
				},
			},
			wantErr: false,
		},
		{
			name: "single architecture as string",
			sdlAttrs: map[string]interface{}{
				"arch": "arm64",
			},
			expected: types.Attributes{
				{
					Key:   "capabilities/cpu/arch/arm64",
					Value: "true",
				},
			},
			wantErr: false,
		},
		{
			name: "no arch attribute",
			sdlAttrs: map[string]interface{}{
				"other": "value",
			},
			expected: nil,
			wantErr:  false,
		},
		{
			name: "invalid architecture type",
			sdlAttrs: map[string]interface{}{
				"arch": []interface{}{123, "amd64"}, // number instead of string
			},
			expected: nil,
			wantErr:  true,
		},
		{
			name: "invalid architecture format",
			sdlAttrs: map[string]interface{}{
				"arch": 123, // number instead of string or array
			},
			expected: nil,
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ConvertSDLCPUAttributesToKeyValue(tt.sdlAttrs)

			if tt.wantErr {
				assert.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}
