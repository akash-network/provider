package migrations

import (
	"bytes"
	"context"
	"fmt"
	"io"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/dynamic"

	akashnetwork "github.com/akash-network/provider/pkg/apis/akash.network"
)

var crdGVR = schema.GroupVersionResource{
	Group:    "apiextensions.k8s.io",
	Version:  "v1",
	Resource: "customresourcedefinitions",
}

func ApplyCRDs(ctx context.Context, dc dynamic.Interface) ([]string, error) {
	decoder := yaml.NewYAMLOrJSONDecoder(bytes.NewReader(akashnetwork.CRDManifest), 4096)

	var names []string

	for {
		obj := map[string]interface{}{}
		if err := decoder.Decode(&obj); err != nil {
			if err == io.EOF {
				break
			}
			return nil, fmt.Errorf("decoding CRD manifest: %w", err)
		}

		if len(obj) == 0 {
			continue
		}

		u := &unstructured.Unstructured{Object: obj}

		if _, err := dc.Resource(crdGVR).Apply(ctx, u.GetName(), u, metav1.ApplyOptions{FieldManager: "akash-provider", Force: true}); err != nil {
			return nil, fmt.Errorf("applying CRD %s: %w", u.GetName(), err)
		}

		names = append(names, u.GetName())
	}

	return names, nil
}
