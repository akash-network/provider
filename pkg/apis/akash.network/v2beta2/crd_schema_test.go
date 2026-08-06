package v2beta2

import (
	"bytes"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/kube-openapi/pkg/validation/spec"
	"k8s.io/kube-openapi/pkg/validation/strfmt"
	"k8s.io/kube-openapi/pkg/validation/validate"
)

type manifestCRDSchema struct {
	Metadata struct {
		Name string `json:"name"`
	} `json:"metadata"`
	Spec struct {
		Versions []struct {
			Name   string `json:"name"`
			Schema struct {
				OpenAPIV3Schema spec.Schema `json:"openAPIV3Schema"`
			} `json:"schema"`
		} `json:"versions"`
	} `json:"spec"`
}

func TestManifestKBSCRDSchemaRejectsInvalidSources(t *testing.T) {
	contents, err := os.ReadFile("../crd.yaml")
	require.NoError(t, err)

	var crd manifestCRDSchema
	err = yaml.NewYAMLOrJSONDecoder(bytes.NewReader(contents), 4096).Decode(&crd)
	require.NoError(t, err)
	require.Equal(t, "manifests.akash.network", crd.Metadata.Name)
	require.Len(t, crd.Spec.Versions, 2)

	tenant := map[string]any{
		"url":                       "https://kbs.tenant.example",
		"certificate":               "tenant public certificate",
		"image_security_policy_uri": "kbs:///tenant/security-policy/sha256-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"agent_policy":              "package agent_policy\n\ndefault allow = false\n",
	}
	tests := []struct {
		name  string
		value map[string]any
		valid bool
	}{
		{name: "provider", value: map[string]any{"provider": map[string]any{}}, valid: true},
		{name: "tenant", value: map[string]any{"tenant": tenant}, valid: true},
		{name: "null provider", value: map[string]any{"provider": nil}},
		{name: "null tenant", value: map[string]any{"tenant": nil}},
		{name: "missing source", value: map[string]any{}},
		{name: "mixed sources", value: map[string]any{"provider": map[string]any{}, "tenant": tenant}},
	}

	for _, version := range crd.Spec.Versions {
		version := version
		t.Run(version.Name, func(t *testing.T) {
			kbsSchema := manifestKBSSchema(t, version.Schema.OpenAPIV3Schema)
			for _, test := range tests {
				t.Run(test.name, func(t *testing.T) {
					err := validate.AgainstSchema(&kbsSchema, test.value, strfmt.Default)
					if test.valid {
						require.NoError(t, err)
						return
					}
					require.Error(t, err)
				})
			}
		})
	}
}

func manifestKBSSchema(t *testing.T, root spec.Schema) spec.Schema {
	t.Helper()

	specSchema := requireSchemaProperty(t, root, "spec")
	groupSchema := requireSchemaProperty(t, specSchema, "group")
	servicesSchema := requireSchemaProperty(t, groupSchema, "services")
	require.NotNil(t, servicesSchema.Items)
	require.NotNil(t, servicesSchema.Items.Schema)
	paramsSchema := requireSchemaProperty(t, *servicesSchema.Items.Schema, "params")
	return requireSchemaProperty(t, paramsSchema, "kbs")
}

func requireSchemaProperty(t *testing.T, schema spec.Schema, name string) spec.Schema {
	t.Helper()

	property, ok := schema.Properties[name]
	require.True(t, ok, "CRD schema property %q is missing", name)
	return property
}
