//go:build recovery_upgrade

package kube

import (
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/akash-network/provider/cluster/kube/builder"
	akashfake "github.com/akash-network/provider/pkg/client/clientset/versioned/fake"
)

// Run via script/test-provider-recovery-upgrade.sh. Missing baseline artifacts
// are a failure, never a skipped upgrade check.
func TestDeploymentUpgradeFromSnapshots(t *testing.T) {
	dir := os.Getenv("PROVIDER_RECOVERY_IMPORT_DIR")
	require.NotEmpty(t, dir, "run script/test-provider-recovery-upgrade.sh BASE_REVISION")
	root, err := os.OpenRoot(dir)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, root.Close()) })
	paths, err := fs.Glob(root.FS(), "recovery-*.json")
	require.NoError(t, err)
	require.NotEmpty(t, paths, "base revision did not export any workloads")
	for _, path := range paths {
		data, err := root.ReadFile(path)
		require.NoError(t, err)
		var snapshot recoverySnapshot
		require.NoError(t, json.Unmarshal(data, &snapshot))
		t.Run(snapshot.Name, func(t *testing.T) {
			c := nadFakeClient(t)
			// Retain operator overrides while using the candidate's defaults.
			// Reusing all old settings would hide a new default that rolls pods.
			settings := builder.NewDefaultSettings()
			current := reflect.ValueOf(&settings).Elem()
			stored, defaults := reflect.ValueOf(snapshot.Settings), reflect.ValueOf(snapshot.Defaults)
			for i := 0; i < stored.NumField(); i++ {
				if !reflect.DeepEqual(stored.Field(i).Interface(), defaults.Field(i).Interface()) {
					current.Field(i).Set(stored.Field(i))
				}
			}
			ctx := context.WithValue(context.Background(), builder.SettingsKey, settings)
			for _, network := range snapshot.Networks {
				_, err := c.dc.Resource(nadGVR).Namespace(network.GetNamespace()).Create(ctx, &network, metav1.CreateOptions{})
				require.NoError(t, err)
			}
			objects := make([]runtime.Object, 0, 1+len(snapshot.Deployments)+len(snapshot.StatefulSets))
			objects = append(objects, &snapshot.Namespace)
			for i := range snapshot.Deployments {
				objects = append(objects, &snapshot.Deployments[i])
			}
			for i := range snapshot.StatefulSets {
				objects = append(objects, &snapshot.StatefulSets[i])
			}
			c.kc = fake.NewClientset(objects...)
			c.ac = akashfake.NewSimpleClientset(&snapshot.Manifest)
			c.ns = snapshot.Manifest.Namespace
			deployment, err := snapshot.Manifest.Deployment()
			require.NoError(t, err)
			checkRecoveredDeployment(t, ctx, c, deployment)
		})
	}
}
