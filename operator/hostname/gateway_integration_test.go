//go:build gateway_integration

package hostname

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"cosmossdk.io/log"
	sdktypes "github.com/cosmos/cosmos-sdk/types"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	mtypes "pkg.akt.dev/go/node/market/v1"

	"github.com/akash-network/provider/cluster/kube/builder"
	"github.com/akash-network/provider/cluster/kube/gateway"
	providerflags "github.com/akash-network/provider/cmd/provider-services/cmd/flags"
	"github.com/akash-network/provider/operator/common"
	crd "github.com/akash-network/provider/pkg/apis/akash.network/v2beta2"
	akashclientset "github.com/akash-network/provider/pkg/client/clientset/versioned"
	"github.com/akash-network/provider/tools/fromctx"
)

// TestGatewayOperatorRecovery runs against a disposable cluster with the Akash
// CRDs, NGF with snippets enabled, and akash-gateway/akash-gateway installed.
// It requires an explicit kubeconfig and an HTTP URL to the gateway data plane:
//
// AKASH_GATEWAY_TEST_KUBECONFIG=/path/to/kubeconfig \
// AKASH_GATEWAY_TEST_URL=http://127.0.0.1:18080 \
// go test -tags=gateway_integration -run TestGatewayOperatorRecovery -v ./operator/hostname
func TestGatewayOperatorRecovery(t *testing.T) {
	kubeconfig := os.Getenv("AKASH_GATEWAY_TEST_KUBECONFIG")
	gatewayURL := os.Getenv("AKASH_GATEWAY_TEST_URL")
	if kubeconfig == "" || gatewayURL == "" {
		t.Skip("set AKASH_GATEWAY_TEST_KUBECONFIG and AKASH_GATEWAY_TEST_URL for a disposable NGF cluster")
	}

	cfg, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	require.NoError(t, err)
	kc, err := kubernetes.NewForConfig(cfg)
	require.NoError(t, err)
	ac, err := akashclientset.NewForConfig(cfg)
	require.NoError(t, err)
	dc, err := dynamic.NewForConfig(cfg)
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	lease := mtypes.LeaseID{
		Owner:    sdktypes.AccAddress(bytes.Repeat([]byte{1}, 20)).String(),
		Provider: sdktypes.AccAddress(bytes.Repeat([]byte{2}, 20)).String(),
		DSeq:     uint64(time.Now().UnixNano()), GSeq: 1, OSeq: 1,
	}
	ns := builder.LidNS(lease)
	manifestNS := "hostname-recovery-" + strconv.FormatUint(lease.DSeq, 10)
	for _, name := range []string{manifestNS, ns} {
		_, err = kc.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}}, metav1.CreateOptions{})
		require.NoError(t, err)
		t.Cleanup(func() {
			cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cleanupCancel()
			if err := kc.CoreV1().Namespaces().Delete(cleanupCtx, name, metav1.DeleteOptions{}); err != nil {
				t.Logf("cleanup namespace %s: %v", name, err)
			}
		})
	}
	t.Cleanup(func() {
		if !t.Failed() {
			return
		}
		diagnosticCtx, diagnosticCancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer diagnosticCancel()
		for _, resource := range []struct {
			gvr schema.GroupVersionResource
			ns  string
		}{
			{gateway.HTTPRouteGVR, ns},
			{schema.GroupVersionResource{Group: "gateway.nginx.org", Version: "v1alpha1", Resource: "snippetsfilters"}, ns},
			{schema.GroupVersionResource{Group: "gateway.networking.k8s.io", Version: "v1", Resource: "gateways"}, "akash-gateway"},
		} {
			objects, getErr := dc.Resource(resource.gvr).Namespace(resource.ns).List(diagnosticCtx, metav1.ListOptions{})
			if getErr != nil {
				t.Logf("failure diagnostics %s/%s: %v", resource.ns, resource.gvr.Resource, getErr)
				continue
			}
			data, _ := json.Marshal(objects)
			t.Logf("failure diagnostics %s/%s: %s", resource.ns, resource.gvr.Resource, data)
		}
		for _, componentNS := range []string{"nginx-gateway", "akash-gateway"} {
			pods, getErr := kc.CoreV1().Pods(componentNS).List(diagnosticCtx, metav1.ListOptions{})
			if getErr != nil {
				t.Logf("failure diagnostics pods in %s: %v", componentNS, getErr)
				continue
			}
			for _, pod := range pods.Items {
				state, _ := json.Marshal(pod.Status)
				t.Logf("failure diagnostics pod %s/%s: %s", componentNS, pod.Name, state)
				for _, container := range pod.Spec.Containers {
					tail := int64(60)
					data, logErr := kc.CoreV1().Pods(componentNS).GetLogs(pod.Name, &corev1.PodLogOptions{Container: container.Name, TailLines: &tail}).DoRaw(diagnosticCtx)
					t.Logf("failure diagnostics logs %s/%s/%s error=%v:\n%s", componentNS, pod.Name, container.Name, logErr, data)
				}
			}
		}
	})

	labels := map[string]string{"app": "hostname-recovery"}
	one := int32(1)
	_, err = kc.AppsV1().Deployments(ns).Create(ctx, &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "web"},
		Spec: appsv1.DeploymentSpec{
			Replicas: &one, Selector: &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "web", Image: "nginx:1.29-alpine", Ports: []corev1.ContainerPort{{ContainerPort: 80}}}}},
			},
		},
	}, metav1.CreateOptions{})
	require.NoError(t, err)
	_, err = kc.CoreV1().Services(ns).Create(ctx, &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "web"},
		Spec:       corev1.ServiceSpec{Selector: labels, Ports: []corev1.ServicePort{{Name: "http", Port: 80, TargetPort: intstr.FromInt32(80)}}},
	}, metav1.CreateOptions{})
	require.NoError(t, err)
	_, err = ac.AkashV2beta2().Manifests(manifestNS).Create(ctx, &crd.Manifest{
		ObjectMeta: metav1.ObjectMeta{Name: ns},
		Spec: crd.ManifestSpec{
			LeaseID: crd.LeaseID{Owner: lease.Owner, Provider: lease.Provider, DSeq: strconv.FormatUint(lease.DSeq, 10), GSeq: lease.GSeq, OSeq: lease.OSeq},
			Group: crd.ManifestGroup{Name: "web", Services: []crd.ManifestService{{
				Name: "web", Image: "nginx:1.29-alpine", Count: 1,
				Expose: []crd.ManifestServiceExpose{{Port: 80, ExternalPort: 80, Proto: "TCP", Global: true,
					HTTPOptions: crd.ManifestServiceExposeHTTPOptions{MaxBodySize: 1024, ReadTimeout: 5000, SendTimeout: 5000, NextTimeout: 1000, NextTries: 2, NextCases: []string{"error", "timeout"}},
				}},
			}}},
		},
	}, metav1.CreateOptions{})
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		dep, getErr := kc.AppsV1().Deployments(ns).Get(ctx, "web", metav1.GetOptions{})
		return getErr == nil && dep.Status.AvailableReplicas == 1
	}, 90*time.Second, 250*time.Millisecond, "hello backend must be ready before testing routing")

	fault := &gatewayFilterFault{}
	fault.hostname.Store("")
	opConfig := rest.CopyConfig(cfg)
	opConfig.Wrap(func(next http.RoundTripper) http.RoundTripper {
		fault.next = next
		return fault
	})
	oldBuffer := viper.GetString(providerflags.FlagProxyBufferSize)
	viper.Set(providerflags.FlagProxyBufferSize, "16k")
	t.Cleanup(func() { viper.Set(providerflags.FlagProxyBufferSize, oldBuffer) })

	var stopOperator func()
	startOperator := func() {
		opCtx, stop := context.WithCancel(ctx)
		opCtx = context.WithValue(opCtx, fromctx.CtxKeyKubeConfig, opConfig)
		opCtx = context.WithValue(opCtx, fromctx.CtxKeyKubeClientSet, kubernetes.Interface(kc))
		opCtx = context.WithValue(opCtx, fromctx.CtxKeyAkashClientSet, akashclientset.Interface(ac))
		opCtx = context.WithValue(opCtx, fromctx.CtxKeyGatewayConfig, fromctx.GatewayConfig{
			IngressMode: string(builder.IngressModeGateway), Name: "akash-gateway", Namespace: "akash-gateway", Provider: "nginx",
		})
		op, newErr := newHostnameOperator(opCtx, log.NewNopLogger(), manifestNS,
			common.OperatorConfig{RetryDelay: 200 * time.Millisecond, PruneInterval: 2 * time.Second, WebRefreshInterval: time.Second})
		require.NoError(t, newErr)
		done := make(chan error, 1)
		go func() { done <- op.run() }()
		stopOperator = func() {
			stop()
			select {
			case <-done:
			case <-time.After(10 * time.Second):
				t.Error("hostname operator did not stop")
			}
		}
	}
	startOperator()
	t.Cleanup(func() { stopOperator() })
	createHost := func(host string) {
		_, createErr := ac.AkashV2beta2().ProviderHosts(manifestNS).Create(ctx, &crd.ProviderHost{
			ObjectMeta: metav1.ObjectMeta{Name: host},
			Spec: crd.ProviderHostSpec{Owner: lease.Owner, Provider: lease.Provider, Dseq: lease.DSeq, Gseq: lease.GSeq, Oseq: lease.OSeq,
				Hostname: host, ServiceName: "web", ExternalPort: 80},
		}, metav1.CreateOptions{})
		require.NoError(t, createErr)
	}
	client := &http.Client{Timeout: 2 * time.Second}
	request := func(host, method, body string) (int, error) {
		// The URL explicitly selects the disposable test gateway.
		//nolint:gosec // G704: opt-in integration test, never a production request target.
		req, requestErr := http.NewRequestWithContext(ctx, method, gatewayURL, strings.NewReader(body))
		if requestErr != nil {
			return 0, requestErr
		}
		req.Host = host
		//nolint:gosec // G704: request targets the explicitly configured disposable test gateway.
		resp, requestErr := client.Do(req)
		if requestErr != nil {
			return 0, requestErr
		}
		defer resp.Body.Close()
		_, _ = io.Copy(io.Discard, resp.Body)
		return resp.StatusCode, nil
	}
	waitServing := func(host string) {
		t.Helper()
		// NGF leader handover and a new data-plane pod can take over 30 seconds.
		// Established traffic is checked continuously throughout this window.
		waitErr := wait.PollUntilContextTimeout(ctx, 100*time.Millisecond, 60*time.Second, true, func(context.Context) (bool, error) {
			status, requestErr := request(host, http.MethodGet, "")
			return requestErr == nil && status == http.StatusOK, nil
		})
		if waitErr != nil {
			status, requestErr := request(host, http.MethodGet, "")
			t.Fatalf("%s did not recover: status=%d request error=%v wait error=%v", host, status, requestErr, waitErr)
		}
		status, requestErr := request(host, http.MethodPost, strings.Repeat("x", 2048))
		require.NoError(t, requestErr)
		require.Equal(t, http.StatusRequestEntityTooLarge, status, "recovery must retain requested body limit for %s", host)
	}
	waitPlaceholder := func(host string) {
		t.Helper()
		require.Eventually(t, func() bool {
			route, getErr := dc.Resource(gateway.HTTPRouteGVR).Namespace(ns).Get(ctx, host, metav1.GetOptions{})
			if getErr != nil {
				return false
			}
			parents, _, _ := unstructured.NestedSlice(route.Object, "spec", "parentRefs")
			return len(parents) == 0 && fault.failures.Load() > 0
		}, 10*time.Second, 100*time.Millisecond, "expected unfinished detached route for %s", host)
	}

	suffix := "." + strconv.FormatUint(lease.DSeq, 10) + ".test"
	healthy := "healthy" + suffix
	blocked := "blocked" + suffix
	later := "later" + suffix
	restart := "restart" + suffix
	createHost(healthy)
	waitServing(healthy)

	// Keep checking established traffic throughout a persistent failure and restart.
	probeCtx, stopProbe := context.WithCancel(ctx)
	probeResult := make(chan error, 1)
	var successfulProbes atomic.Int64
	go func() {
		ticker := time.NewTicker(50 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-probeCtx.Done():
				probeResult <- nil
				return
			case <-ticker.C:
				status, probeErr := request(healthy, http.MethodGet, "")
				if probeErr != nil || status != http.StatusOK {
					probeResult <- fmt.Errorf("healthy route failed during unrelated recovery: status=%d error=%v", status, probeErr)
					return
				}
				successfulProbes.Add(1)
			}
		}
	}()
	probeChecked := false
	t.Cleanup(func() {
		stopProbe()
		if !probeChecked {
			if probeErr := <-probeResult; probeErr != nil {
				t.Error(probeErr)
			}
		}
	})

	fault.hostname.Store(blocked)
	createHost(blocked)
	waitPlaceholder(blocked)
	createHost(later)
	waitServing(later)
	t.Log("later hostname serves HTTP200 while first hostname filter creation remains forbidden")
	fault.hostname.Store("")
	waitServing(blocked)
	t.Log("failed hostname recovered automatically, GET200 and oversized POST413")

	fault.hostname.Store(restart)
	createHost(restart)
	waitPlaceholder(restart)
	stopOperator()
	fault.hostname.Store("")
	startOperator()
	waitServing(restart)
	t.Log("operator restart repaired detached placeholder, GET200 and oversized POST413")

	// Stop the real NGF controller while leaving its NGINX data plane running.
	// Existing traffic must keep working. A hostname declared during the outage
	// must recover when NGF returns, without touching its ProviderHost or manifest.
	controller, err := kc.AppsV1().Deployments("nginx-gateway").Get(ctx, "nginx-gateway", metav1.GetOptions{})
	require.NoError(t, err)
	replicas := int32(1)
	if controller.Spec.Replicas != nil {
		replicas = *controller.Spec.Replicas
	}
	scaleController := func(scaleCtx context.Context, count int32) error {
		scale, scaleErr := kc.AppsV1().Deployments("nginx-gateway").GetScale(scaleCtx, "nginx-gateway", metav1.GetOptions{})
		if scaleErr != nil {
			return scaleErr
		}
		scale.Spec.Replicas = count
		_, scaleErr = kc.AppsV1().Deployments("nginx-gateway").UpdateScale(scaleCtx, "nginx-gateway", scale, metav1.UpdateOptions{})
		return scaleErr
	}
	t.Cleanup(func() {
		restoreCtx, restoreCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer restoreCancel()
		if restoreErr := scaleController(restoreCtx, replicas); restoreErr != nil {
			t.Errorf("restore NGF controller replicas: %v", restoreErr)
		}
	})
	require.NoError(t, scaleController(ctx, 0))
	require.Eventually(t, func() bool {
		dep, getErr := kc.AppsV1().Deployments("nginx-gateway").Get(ctx, "nginx-gateway", metav1.GetOptions{})
		return getErr == nil && dep.Status.Replicas == 0
	}, 30*time.Second, 100*time.Millisecond)
	outage := "outage" + suffix
	createHost(outage)
	require.Eventually(t, func() bool {
		filterGVR := schema.GroupVersionResource{Group: "gateway.nginx.org", Version: "v1alpha1", Resource: "snippetsfilters"}
		filter, getErr := dc.Resource(filterGVR).Namespace(ns).Get(ctx, outage, metav1.GetOptions{})
		if getErr != nil {
			return false
		}
		_, hasStatus := filter.Object["status"]
		return !hasStatus
	}, 10*time.Second, 100*time.Millisecond, "new filter must remain unaccepted while NGF is stopped")
	status, requestErr := request(healthy, http.MethodGet, "")
	require.NoError(t, requestErr)
	require.Equal(t, http.StatusOK, status, "existing route must survive NGF controller outage")
	require.NoError(t, scaleController(ctx, replicas))
	waitServing(outage)
	t.Log("real NGF controller restart recovered new hostname without redeploy; existing hostname stayed HTTP200")

	filterGVR := schema.GroupVersionResource{Group: "gateway.nginx.org", Version: "v1alpha1", Resource: "snippetsfilters"}
	oldFilter, err := dc.Resource(filterGVR).Namespace(ns).Get(ctx, blocked, metav1.GetOptions{})
	require.NoError(t, err)
	require.NoError(t, dc.Resource(filterGVR).Namespace(ns).Delete(ctx, blocked, metav1.DeleteOptions{}))
	require.Eventually(t, func() bool {
		filter, getErr := dc.Resource(filterGVR).Namespace(ns).Get(ctx, blocked, metav1.GetOptions{})
		return getErr == nil && filter.GetUID() != oldFilter.GetUID()
	}, 15*time.Second, 100*time.Millisecond, "deleted live filter must be recreated without a ProviderHost event")
	waitServing(blocked)
	t.Log("deleted live SnippetsFilter repaired without redeploy, GET200 and oversized POST413")
	for i := 0; i < 20; i++ {
		for _, host := range []string{healthy, blocked, later, restart, outage} {
			status, requestErr := request(host, http.MethodGet, "")
			require.NoError(t, requestErr)
			require.Equal(t, http.StatusOK, status, host)
		}
	}
	stopProbe()
	probeErr := <-probeResult
	probeChecked = true
	require.NoError(t, probeErr)
	t.Logf("healthy route stayed HTTP200 for %d continuous probes; all five routes passed 20 requests after recovery", successfulProbes.Load())
}

// gatewayFilterFault injects a per-host API permission failure; the API server,
// operator watch, NGF controller, NGINX data plane, and other hosts remain real.
type gatewayFilterFault struct {
	next     http.RoundTripper
	hostname atomic.Value
	failures atomic.Int64
}

func (f *gatewayFilterFault) RoundTrip(req *http.Request) (*http.Response, error) {
	host := f.hostname.Load().(string)
	if host != "" && strings.Contains(req.URL.Path, "/snippetsfilters") && (req.Method == http.MethodPost || req.Method == http.MethodPut || req.Method == http.MethodPatch) {
		body, err := io.ReadAll(req.Body)
		if err != nil {
			return nil, err
		}
		_ = req.Body.Close()
		req.Body = io.NopCloser(bytes.NewReader(body))
		var object metav1.PartialObjectMetadata
		if err := json.Unmarshal(body, &object); err != nil {
			return nil, err
		}
		if object.Name == host || strings.HasSuffix(req.URL.Path, "/"+host) {
			f.failures.Add(1)
			return &http.Response{StatusCode: http.StatusForbidden, Header: http.Header{"Content-Type": []string{"application/json"}},
				Body: io.NopCloser(strings.NewReader(`{"kind":"Status","apiVersion":"v1","status":"Failure","reason":"Forbidden","message":"injected hostname filter permission failure","code":403}`)), Request: req}, nil
		}
	}
	return f.next.RoundTrip(req)
}
