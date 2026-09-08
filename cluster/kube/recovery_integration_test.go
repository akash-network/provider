//go:build k8s_integration

package kube

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"pkg.akt.dev/go/sdl"
	"pkg.akt.dev/go/testutil"

	"github.com/akash-network/provider/cluster/kube/builder"
	"github.com/akash-network/provider/cluster/kube/clientcommon"
	ctypes "github.com/akash-network/provider/cluster/types/v1beta3"
	providerflags "github.com/akash-network/provider/cmd/provider-services/cmd/flags"
	crd "github.com/akash-network/provider/pkg/apis/akash.network/v2beta2"
	akashclient "github.com/akash-network/provider/pkg/client/clientset/versioned"
	"github.com/akash-network/provider/tools/fromctx"
)

type recoveryWorkloadState struct {
	Templates   map[string]corev1.PodTemplateSpec
	Generations map[string]int64
	Pods        map[string]types.UID
	Restarts    map[string]int32
}

// This test uses real API-server resource versions and controllers. A fake
// client does not advance resourceVersion and cannot reproduce the regression.
// Both a Deployment and a StatefulSet must survive repeated recovery, including
// metadata left by an older provider, and still roll out a tenant update.
func TestDeploymentRecoveryKeepsPodsRunning(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		name := "fresh"
		if legacy {
			name = "legacy version labels"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			defer cancel()
			log := testutil.Logger(t)
			cfg, err := clientcommon.OpenKubeConfig(providerflags.KubeConfigDefaultPath, log)
			require.NoError(t, err)
			kc, err := kubernetes.NewForConfig(cfg)
			require.NoError(t, err)
			ac, err := akashclient.NewForConfig(cfg)
			require.NoError(t, err)
			ctx = context.WithValue(ctx, builder.SettingsKey, builder.NewDefaultSettings())
			ctx = context.WithValue(ctx, fromctx.CtxKeyKubeConfig, cfg)
			ctx = context.WithValue(ctx, fromctx.CtxKeyKubeClientSet, kubernetes.Interface(kc))
			ctx = context.WithValue(ctx, fromctx.CtxKeyAkashClientSet, akashclient.Interface(ac))

			ns, err := kc.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{GenerateName: "provider-recovery-test-"},
			}, metav1.CreateOptions{})
			require.NoError(t, err)
			t.Cleanup(func() {
				require.NoError(t, kc.CoreV1().Namespaces().Delete(context.Background(), ns.Name, metav1.DeleteOptions{}))
			})
			lid := testutil.LeaseID(t)
			leaseNS := builder.LidNS(lid)
			_, err = kc.CoreV1().Namespaces().Get(ctx, leaseNS, metav1.GetOptions{})
			require.True(t, kerrors.IsNotFound(err), "test must use a new lease namespace")
			t.Cleanup(func() {
				err := kc.CoreV1().Namespaces().Delete(context.Background(), leaseNS, metav1.DeleteOptions{})
				if !kerrors.IsNotFound(err) {
					require.NoError(t, err)
				}
			})

			data, err := sdl.ReadFile("../../testdata/deployment/deployment-v2-storage-default.yaml")
			require.NoError(t, err)
			manifest, err := data.Manifest()
			require.NoError(t, err)
			group := manifest.GetGroups()[0]
			cparams := make(crd.ReservationClusterSettings)
			for i := range group.Services {
				group.Services[i].Image = "registry.k8s.io/pause:3.10"
				group.Services[i].Expose = nil
				group.Services[i].Resources.Endpoints = nil
				cparams[group.Services[i].Resources.ID] = nil
			}
			newClient := func() Client {
				c, err := NewClient(ctx, log, ns.Name)
				require.NoError(t, err)
				return c
			}
			c := newClient()
			require.NoError(t, c.Deploy(ctx, &ctypes.Deployment{Lid: lid, MGroup: &group, CParams: cparams}))

			readState := func() recoveryWorkloadState {
				return readRecoveryWorkloads(t, ctx, kc, leaseNS)
			}
			readManifest := func() *crd.Manifest {
				m, err := ac.AkashV2beta2().Manifests(ns.Name).Get(ctx, leaseNS, metav1.GetOptions{})
				require.NoError(t, err)
				return m
			}
			readState()
			if legacy {
				// Simulate an old provider's first recovery: the saved Manifest
				// version has advanced while the pod templates still carry the
				// previous version. Adopting this state must not roll either pod.
				m := readManifest()
				version := m.ResourceVersion
				m.Labels[builder.AkashManifestResourceVersion] = version
				_, err = ac.AkashV2beta2().Manifests(ns.Name).Update(ctx, m, metav1.UpdateOptions{})
				require.NoError(t, err)
				d, err := kc.AppsV1().Deployments(leaseNS).Get(ctx, "bew", metav1.GetOptions{})
				require.NoError(t, err)
				d.Spec.Template.Labels[builder.AkashManifestResourceVersion] = version
				_, err = kc.AppsV1().Deployments(leaseNS).Update(ctx, d, metav1.UpdateOptions{})
				require.NoError(t, err)
				s, err := kc.AppsV1().StatefulSets(leaseNS).Get(ctx, "web", metav1.GetOptions{})
				require.NoError(t, err)
				s.Spec.Template.Labels[builder.AkashManifestResourceVersion] = version
				_, err = kc.AppsV1().StatefulSets(leaseNS).Update(ctx, s, metav1.UpdateOptions{})
				require.NoError(t, err)
			}

			before := readState()
			assertStableRecovery := func() {
				stored := readManifest()
				state := readState()
				for restart := 1; restart <= 3; restart++ {
					c = newClient()
					deployments, err := c.Deployments(ctx)
					require.NoError(t, err)
					require.Len(t, deployments, 1)
					require.NoError(t, c.Deploy(ctx, deployments[0]))
					require.Equal(t, stored.ResourceVersion, readManifest().ResourceVersion,
						"recovery %d rewrote an unchanged Manifest", restart)
					require.Equal(t, state, readState(), "recovery %d changed running workloads", restart)
				}
			}
			assertStableRecovery()

			// Follow the same input path as a tenant update after startup: use
			// the recovered reservation settings with a newly received manifest.
			deployments, err := c.Deployments(ctx)
			require.NoError(t, err)
			require.Len(t, deployments, 1)
			storedVersion := readManifest().ResourceVersion
			require.NoError(t, c.Deploy(ctx, &ctypes.Deployment{
				Lid: lid, MGroup: &group, CParams: deployments[0].ClusterParams(),
			}))
			require.Equal(t, storedVersion, readManifest().ResourceVersion, "resending an unchanged manifest must be a no-op")
			require.Equal(t, before, readState(), "resending an unchanged manifest must keep both pods")
			for i := range group.Services {
				group.Services[i].Env = append(group.Services[i].Env, "RECOVERY_UPDATE=applied")
			}
			require.NoError(t, c.Deploy(ctx, &ctypes.Deployment{
				Lid: lid, MGroup: &group, CParams: deployments[0].ClusterParams(),
			}))
			after := readState()
			for service, template := range after.Templates {
				require.Contains(t, template.Spec.Containers[0].Env, corev1.EnvVar{Name: "RECOVERY_UPDATE", Value: "applied"})
				require.NotEqual(t, before.Pods[service], after.Pods[service], "tenant update must replace %s pod", service)
			}
			for _, service := range readManifest().Spec.Group.Services {
				require.Contains(t, service.Env, "RECOVERY_UPDATE=applied")
			}
			assertStableRecovery()
		})
	}
}

func readRecoveryWorkloads(t *testing.T, ctx context.Context, kc kubernetes.Interface, ns string) recoveryWorkloadState {
	t.Helper()
	var state recoveryWorkloadState
	require.EventuallyWithT(t, func(collect *assert.CollectT) {
		state = recoveryWorkloadState{
			Templates: make(map[string]corev1.PodTemplateSpec), Generations: make(map[string]int64),
			Pods: make(map[string]types.UID), Restarts: make(map[string]int32),
		}
		d, err := kc.AppsV1().Deployments(ns).Get(ctx, "bew", metav1.GetOptions{})
		require.NoError(collect, err)
		require.Equal(collect, d.Generation, d.Status.ObservedGeneration)
		require.EqualValues(collect, 1, d.Status.UpdatedReplicas)
		require.EqualValues(collect, 1, d.Status.AvailableReplicas)
		state.Templates[d.Name], state.Generations[d.Name] = d.Spec.Template, d.Generation
		s, err := kc.AppsV1().StatefulSets(ns).Get(ctx, "web", metav1.GetOptions{})
		require.NoError(collect, err)
		require.Equal(collect, s.Generation, s.Status.ObservedGeneration)
		require.Equal(collect, s.Status.CurrentRevision, s.Status.UpdateRevision)
		require.EqualValues(collect, 1, s.Status.ReadyReplicas)
		state.Templates[s.Name], state.Generations[s.Name] = s.Spec.Template, s.Generation
		pods, err := kc.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{})
		require.NoError(collect, err)
		require.Len(collect, pods.Items, 2)
		for _, pod := range pods.Items {
			require.Nil(collect, pod.DeletionTimestamp)
			require.Equal(collect, corev1.PodRunning, pod.Status.Phase)
			service := pod.Labels[builder.AkashManifestServiceLabelName]
			state.Pods[service] = pod.UID
			require.NotEmpty(collect, pod.Status.ContainerStatuses)
			for _, container := range pod.Status.ContainerStatuses {
				require.True(collect, container.Ready)
				state.Restarts[service+"/"+container.Name] = container.RestartCount
			}
		}
		require.Len(collect, state.Pods, 2)
	}, time.Minute, 250*time.Millisecond, "Deployment and StatefulSet must finish reconciling")
	return state
}
