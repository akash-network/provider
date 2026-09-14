//go:build e2e

package integration

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	nodev1 "k8s.io/api/node/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"

	"pkg.akt.dev/go/testutil"

	"github.com/akash-network/provider/cluster/kube/builder"
	clientcommon "github.com/akash-network/provider/cluster/kube/clientcommon"
	providerflags "github.com/akash-network/provider/cmd/provider-services/cmd/flags"
)

const (
	snpNodeLabel        = "amd.feature.node.kubernetes.io/snp"
	tdxNodeLabel        = "intel.feature.node.kubernetes.io/tdx"
	kataRuntimeLabel    = "katacontainers.io/kata-runtime"
	nodeFeatureLabelVal = "true"
)

var teePlatformNodeLabels = map[string]string{
	"snp": snpNodeLabel,
	"tdx": tdxNodeLabel,
}

func applyTEEMock(t *testing.T, platform string) {
	t.Helper()

	platformLabel, ok := teePlatformNodeLabels[platform]
	require.Truef(t, ok, "unknown TEE platform %q", platform)

	cfg, err := clientcommon.OpenKubeConfig(providerflags.KubeConfigDefaultPath, testutil.Logger(t))
	require.NoError(t, err)

	kc, err := kubernetes.NewForConfig(cfg)
	require.NoError(t, err)

	ctx := context.Background()

	// DetectTEEPlatform reads the node label exactly once at provider startup, so
	// these labels must land before the provider process starts, not at test time.
	patch, err := json.Marshal(map[string]interface{}{
		"metadata": map[string]interface{}{
			"labels": map[string]string{
				platformLabel:    nodeFeatureLabelVal,
				kataRuntimeLabel: nodeFeatureLabelVal,
			},
		},
	})
	require.NoError(t, err)

	nodes, err := kc.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	require.NoError(t, err)

	for i := range nodes.Items {
		_, err = kc.CoreV1().Nodes().Patch(ctx, nodes.Items[i].Name, types.StrategicMergePatchType, patch, metav1.PatchOptions{})
		require.NoError(t, err)
	}

	for _, name := range []string{"kata-qemu-snp", "kata-qemu-tdx"} {
		_, err = kc.NodeV1().RuntimeClasses().Create(ctx, &nodev1.RuntimeClass{
			ObjectMeta: metav1.ObjectMeta{Name: name},
			Handler:    "runc",
		}, metav1.CreateOptions{})
		if err != nil && !kerrors.IsAlreadyExists(err) {
			require.NoError(t, err)
		}
	}
}

// assertPodsRuntimeClass fails unless every managed pod in the namespace carries the
// expected runtimeClassName. This is what makes a TEE gate non-vacuous: without it a
// gate could pass even if the provider silently scheduled a plain (non-CC) pod.
func assertPodsRuntimeClass(t *testing.T, kube kubernetes.Interface, namespace, expected string) {
	t.Helper()

	pods, err := kube.CoreV1().Pods(namespace).List(context.Background(), metav1.ListOptions{
		LabelSelector: builder.AkashManagedLabelName + "=true",
	})
	require.NoError(t, err)
	require.NotEmpty(t, pods.Items, "no managed pods in %s", namespace)

	for i := range pods.Items {
		rc := pods.Items[i].Spec.RuntimeClassName
		require.NotNilf(t, rc, "pod %s has no runtimeClassName, expected %q", pods.Items[i].Name, expected)
		require.Equalf(t, expected, *rc, "pod %s runtimeClassName", pods.Items[i].Name)
	}
}
