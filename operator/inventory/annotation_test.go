package inventory

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"gopkg.in/yaml.v3"
)

type testCase struct {
	name   string
	data   string
	expErr error
}

func TestAnnotationsJson(t *testing.T) {
	testCases := []testCase{
		{
			name:   "v1/valid",
			data:   `{"version":"v1.0.0","storage_classes":["beta2","default"]}`,
			expErr: nil,
		},
		{
			name:   "v1/invalid/missing version",
			data:   `{"storage_classes":["beta2","default"]}`,
			expErr: errCapabilitiesInvalidNoVersion,
		},
		{
			name:   "v1/invalid/bad version",
			data:   `{"version":"bla","storage_classes":["beta2","default"]}`,
			expErr: errCapabilitiesInvalidVersion,
		},
		{
			name:   "v1/invalid/unsupported version",
			data:   `{"version":"v10000.0.0","storage_classes":["beta2","default"]}`,
			expErr: errCapabilitiesUnsupportedVersion,
		},
	}

	for _, test := range testCases {
		t.Run(test.name, func(t *testing.T) {
			caps := &AnnotationCapabilities{}

			err := json.Unmarshal([]byte(test.data), caps)
			if test.expErr == nil {
				assert.NoError(t, err)
			} else {
				assert.ErrorAs(t, err, &test.expErr)
			}
		})
	}
}

func TestAnnotationsYaml(t *testing.T) {
	testCases := []testCase{
		{
			name: "v1/valid",
			data: `---
version: v1.0.0
storage_classes:
  - beta2
  - default`,
			expErr: nil,
		},
		{
			name: "v1/invalid/missing version",
			data: `---
storage_classes:
  - beta2
  - default`,
			expErr: errCapabilitiesInvalidNoVersion,
		},
		{
			name: "v1/invalid/bad version",
			data: `---
version: bla
storage_classes:
  - beta2
  - default`,
			expErr: errCapabilitiesInvalidVersion,
		},
		{
			name: "v1/invalid/unsupported version",
			data: `---
version: v10000.0.0
storage_classes:
  - beta2
  - default`,
			expErr: errCapabilitiesUnsupportedVersion,
		},
	}

	for _, test := range testCases {
		t.Run(test.name, func(t *testing.T) {
			caps := &AnnotationCapabilities{}

			err := yaml.Unmarshal([]byte(test.data), caps)
			if test.expErr == nil {
				assert.NoError(t, err)
			} else {
				assert.ErrorAs(t, err, &test.expErr)
			}
		})
	}
}
