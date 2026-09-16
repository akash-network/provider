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

	// Snapshot the target labels before patching so cleanup can restore them: the gates
	// share one cluster, and a stray SNP label makes a later non-TEE gate misdetect the
	// platform.
	nodes, err := kc.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	require.NoError(t, err)

	labelKeys := []string{platformLabel, kataRuntimeLabel}
	priorLabels := map[string]map[string]*string{} // node -> label -> prior value (nil = absent)
	for i := range nodes.Items {
		node := &nodes.Items[i]
		priorLabels[node.Name] = map[string]*string{}
		for _, k := range labelKeys {
			if v, ok := node.Labels[k]; ok {
				vCopy := v
				priorLabels[node.Name][k] = &vCopy
			} else {
				priorLabels[node.Name][k] = nil
			}
		}
	}

	// DetectTEEPlatform reads the label once at provider startup, so it must land before
	// the process starts.
	patch, err := json.Marshal(map[string]interface{}{
		"metadata": map[string]interface{}{
			"labels": map[string]string{
				platformLabel:    nodeFeatureLabelVal,
				kataRuntimeLabel: nodeFeatureLabelVal,
			},
		},
	})
	require.NoError(t, err)

	var createdRuntimeClasses []string

	// Register rollback BEFORE mutating: a require.NoError below Goexits mid-loop on a
	// transient error, and a cleanup registered afterwards would never run, leaking
	// labels into later gates. Restore failures are logged, not swallowed.
	t.Cleanup(func() {
		cleanupCtx := context.Background()
		for _, name := range createdRuntimeClasses {
			if err := kc.NodeV1().RuntimeClasses().Delete(cleanupCtx, name, metav1.DeleteOptions{}); err != nil && !kerrors.IsNotFound(err) {
				t.Logf("teardown: deleting RuntimeClass %s: %v", name, err)
			}
		}
		for name, labels := range priorLabels {
			// nil marshals to null, which a strategic-merge patch treats as delete.
			revert, err := json.Marshal(map[string]interface{}{
				"metadata": map[string]interface{}{"labels": labels},
			})
			if err != nil {
				t.Errorf("teardown: marshaling label restore for node %s: %v", name, err)
				continue
			}
			if _, err := kc.CoreV1().Nodes().Patch(cleanupCtx, name, types.StrategicMergePatchType, revert, metav1.PatchOptions{}); err != nil {
				t.Errorf("teardown: restoring labels on node %s (leaked TEE labels may corrupt later gates): %v", name, err)
			}
		}
	})

	for name := range priorLabels {
		_, err = kc.CoreV1().Nodes().Patch(ctx, name, types.StrategicMergePatchType, patch, metav1.PatchOptions{})
		require.NoError(t, err)
	}

	for _, name := range []string{"kata-qemu-snp", "kata-qemu-tdx"} {
		_, err = kc.NodeV1().RuntimeClasses().Create(ctx, &nodev1.RuntimeClass{
			ObjectMeta: metav1.ObjectMeta{Name: name},
			Handler:    "runc",
		}, metav1.CreateOptions{})
		switch {
		case err == nil:
			createdRuntimeClasses = append(createdRuntimeClasses, name)
		case kerrors.IsAlreadyExists(err):
			// Pre-existing: leave it in place on cleanup.
		default:
			require.NoError(t, err)
		}
	}
}

// assertPodsRuntimeClass keeps a TEE gate non-vacuous: it fails unless every managed pod
// carries the expected runtimeClassName, not a plain (non-CC) one.
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
