package migrations

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	clienttesting "k8s.io/client-go/testing"
)

var expectedCRDNames = []string{
	"manifests.akash.network",
	"providerhosts.akash.network",
	"providerleasedips.akash.network",
}

// The fake dynamic client's object tracker resolves an apply patch through
// StrategicMergePatch, which apimachinery does not support for unstructured
// content, so a reactor serves the applies instead of the tracker.
func newCRDFake() *dynamicfake.FakeDynamicClient {
	dc := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(
		runtime.NewScheme(),
		map[schema.GroupVersionResource]string{crdGVR: "CustomResourceDefinitionList"},
	)

	dc.PrependReactor("patch", crdGVR.Resource, func(action clienttesting.Action) (bool, runtime.Object, error) {
		applied := &unstructured.Unstructured{Object: map[string]interface{}{}}
		if err := json.Unmarshal(action.(clienttesting.PatchAction).GetPatch(), &applied.Object); err != nil {
			return true, nil, err
		}
		return true, applied, nil
	})

	return dc
}

func appliedDefinitions(t *testing.T, dc *dynamicfake.FakeDynamicClient) []clienttesting.PatchActionImpl {
	t.Helper()

	var applies []clienttesting.PatchActionImpl
	for _, action := range dc.Actions() {
		patch, ok := action.(clienttesting.PatchActionImpl)
		if ok && patch.GetPatchType() == types.ApplyPatchType {
			applies = append(applies, patch)
		}
	}

	return applies
}

func TestApplyCRDsAppliesEveryDefinition(t *testing.T) {
	dc := newCRDFake()

	names, err := ApplyCRDs(context.Background(), dc)
	require.NoError(t, err)
	require.Equal(t, expectedCRDNames, names)

	applies := appliedDefinitions(t, dc)
	require.Len(t, applies, len(expectedCRDNames))

	for i, apply := range applies {
		require.Equal(t, crdGVR, apply.GetResource())
		require.Equal(t, expectedCRDNames[i], apply.GetName())

		var sent map[string]interface{}
		require.NoError(t, json.Unmarshal(apply.GetPatch(), &sent))
		require.Equal(t, "apiextensions.k8s.io/v1", sent["apiVersion"])
		require.Equal(t, "CustomResourceDefinition", sent["kind"])
		require.NotEmpty(t, sent["spec"])
	}
}

func TestApplyCRDsRepeatedRunIssuesIdenticalRequests(t *testing.T) {
	ctx := context.Background()
	dc := newCRDFake()

	firstNames, err := ApplyCRDs(ctx, dc)
	require.NoError(t, err)
	first := appliedDefinitions(t, dc)

	dc.ClearActions()

	secondNames, err := ApplyCRDs(ctx, dc)
	require.NoError(t, err)
	second := appliedDefinitions(t, dc)

	require.Equal(t, firstNames, secondNames)
	require.Len(t, second, len(first))

	for i := range first {
		require.Equal(t, first[i].GetName(), second[i].GetName())
		require.Equal(t, first[i].GetPatch(), second[i].GetPatch())
	}
}

func TestApplyCRDsNeverDeletes(t *testing.T) {
	ctx := context.Background()
	dc := newCRDFake()

	_, err := ApplyCRDs(ctx, dc)
	require.NoError(t, err)

	_, err = ApplyCRDs(ctx, dc)
	require.NoError(t, err)

	for _, action := range dc.Actions() {
		require.NotEqual(t, "delete", action.GetVerb())
		require.NotEqual(t, "deletecollection", action.GetVerb())
	}
}
