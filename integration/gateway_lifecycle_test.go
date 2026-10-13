//go:build e2e

package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"pkg.akt.dev/go/cli"
	clitestutil "pkg.akt.dev/go/cli/testutil"
	mtypes "pkg.akt.dev/go/node/market/v1"
	mvbeta "pkg.akt.dev/go/node/market/v1beta5"
	providerclient "pkg.akt.dev/go/provider/client"
	"sigs.k8s.io/yaml"

	"github.com/akash-network/provider/cluster/kube/builder"
	providerCmd "github.com/akash-network/provider/cmd/provider-services/cmd"
	ptestutil "github.com/akash-network/provider/testutil/provider"
	"github.com/akash-network/provider/tools/fromctx"
)

type gatewayWorkload struct {
	name       string
	customHost bool
	replicas   int
	bodyLimit  int
	persistent bool
	backend    bool
}

type gatewayProbe struct {
	At     time.Time `json:"at"`
	Host   string    `json:"host"`
	Status int       `json:"status"`
	Error  string    `json:"error,omitempty"`
}

func writeGatewayArtifact(t *testing.T, name string, data []byte) {
	t.Helper()
	output := os.Getenv("AKASH_E2E_ARTIFACT_DIR")
	if output == "" {
		return
	}
	root, err := os.OpenRoot(output)
	require.NoError(t, err)
	defer root.Close()
	require.NoError(t, root.WriteFile(name, data, 0600))
}

func (s *E2EGatewayAPI) captureGatewayState() {
	output := os.Getenv("AKASH_E2E_ARTIFACT_DIR")
	if output == "" {
		return
	}
	capture := func(name string, args ...string) {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		args = append([]string{"--kubeconfig", s.kubeConfigPath}, args...)
		data, err := exec.CommandContext(ctx, "kubectl", args...).CombinedOutput()
		if err != nil {
			data = append(data, []byte("\n"+err.Error())...)
		}
		writeGatewayArtifact(s.T(), name, data)
	}
	capture("resources.yaml", "get", "providerhosts,httproutes,snippetsfilters,gateways,services,endpointslices,pods,pvc", "-A", "-o", "yaml")
	capture("events.yaml", "get", "events", "-A", "-o", "yaml")
	kc := s.ctx.Value(fromctx.CtxKeyKubeClientSet).(kubernetes.Interface)
	pods, err := kc.CoreV1().Pods("").List(context.Background(), metav1.ListOptions{})
	if err != nil {
		s.T().Logf("capture pods: %v", err)
		return
	}
	for _, pod := range pods.Items {
		if pod.Namespace != "nginx-gateway" && pod.Namespace != "akash-gateway" && pod.Namespace != "akash-services" {
			continue
		}
		capture(pod.Namespace+"-"+pod.Name+".log", "logs", "-n", pod.Namespace, pod.Name, "--all-containers", "--timestamps", "--tail=2000")
		if pod.Namespace == "akash-gateway" {
			capture(pod.Name+"-nginx.txt", "exec", "-n", pod.Namespace, pod.Name, "-c", "nginx", "--", "nginx", "-T")
		}
	}
}

func (s *E2EGatewayAPI) awaitReadyWorkload(lid mtypes.LeaseID, count int, excluded map[types.UID]bool) {
	kc := s.ctx.Value(fromctx.CtxKeyKubeClientSet).(kubernetes.Interface)
	s.Require().Eventually(func() bool {
		pods, err := kc.CoreV1().Pods(builder.LidNS(lid)).List(s.ctx, metav1.ListOptions{})
		if err != nil || len(pods.Items) != count {
			return false
		}
		for _, pod := range pods.Items {
			if pod.DeletionTimestamp != nil || excluded[pod.UID] {
				return false
			}
			ready := false
			for _, condition := range pod.Status.Conditions {
				if condition.Type == corev1.PodReady && condition.Status == corev1.ConditionTrue {
					ready = true
				}
			}
			if !ready {
				return false
			}
		}
		return true
	}, 120*time.Second, 500*time.Millisecond, "lease %s must have %d ready replacement pods", lid, count)
}

func (s *E2EGatewayAPI) gatewayRequest(host, method, path string, body []byte) (int, string, error) {
	req, err := http.NewRequestWithContext(s.ctx, method, fmt.Sprintf("http://%s:%s%s", s.appHost, s.appPort, path), bytes.NewReader(body))
	if err != nil {
		return 0, "", err
	}
	req.Host = host
	client := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{DisableKeepAlives: true}}
	defer client.CloseIdleConnections()
	resp, err := client.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	return resp.StatusCode, string(data), err
}

func (s *E2EGatewayAPI) awaitGateway(host, expected string) {
	s.Require().Eventually(func() bool {
		status, body, err := s.gatewayRequest(host, "GET", "/", nil)
		return err == nil && status == 200 && strings.Contains(strings.ToLower(body), strings.ToLower(expected))
	}, 120*time.Second, 500*time.Millisecond, "hostname %s must serve its own workload %s", host, expected)
}

func (s *E2EGatewayAPI) createGatewayLease(dseq uint64, file string) mtypes.LeaseID {
	res, err := clitestutil.ExecDeploymentCreate(s.ctx, s.cctx, cli.TestFlags().With(file).WithFrom(s.addrTenant.String()).WithDSeq(dseq).Append(cliFlags)...)
	s.Require().NoError(err)
	s.Require().NoError(s.network.WaitForNextBlock())
	clitestutil.ValidateTxSuccessful(s.ctx, s.T(), s.cctx, res.Bytes())
	var bidID mtypes.BidID
	s.Require().Eventually(func() bool {
		res, err := clitestutil.ExecQueryBids(s.ctx, s.cctx, cli.TestFlags().WithOutputJSON().WithOwner(s.addrTenant.String()).WithDSeq(dseq)...)
		if err != nil {
			return false
		}
		var bids mvbeta.QueryBidsResponse
		if err := s.cctx.Codec.UnmarshalJSON(res.Bytes(), &bids); err != nil {
			return false
		}
		for _, bid := range bids.Bids {
			if bid.Bid.ID.DSeq == dseq {
				bidID = bid.Bid.ID
				return true
			}
		}
		return false
	}, 90*time.Second, time.Second, "provider must bid on deployment %d", dseq)
	res, err = clitestutil.ExecCreateLease(s.ctx, s.cctx, cli.TestFlags().WithGasAuto().WithOutputJSON().WithFrom(s.addrTenant.String()).WithBidID(bidID)...)
	s.Require().NoError(err)
	s.Require().NoError(s.network.WaitForNextBlock())
	clitestutil.ValidateTxSuccessful(s.ctx, s.T(), s.cctx, res.Bytes())
	lid := mtypes.LeaseID(bidID)
	_, err = ptestutil.ExecSendManifest(s.ctx, s.cctx, cli.TestFlags().With(file).WithHome(s.validator.ClientCtx.HomeDir).WithFrom(s.addrTenant.String()).WithDSeq(dseq).WithOutputJSON()...)
	s.Require().NoError(err)
	return lid
}

func (s *E2EGatewayAPI) gatewayLeaseHost(lid mtypes.LeaseID) string {
	var host string
	s.Require().Eventually(func() bool {
		result, err := providerCmd.ExecProviderLeaseStatus(s.ctx, s.cctx, cli.TestFlags().WithHome(s.validator.ClientCtx.HomeDir).WithFrom(s.addrTenant.String()).WithDSeq(lid.DSeq).WithGSeq(lid.GSeq).WithOSeq(lid.OSeq).WithProvider(lid.Provider)...)
		if err != nil {
			return false
		}
		var status providerclient.LeaseStatus
		if json.Unmarshal(result.Bytes(), &status) != nil {
			return false
		}
		service := status.Services["web"]
		if service == nil {
			return false
		}
		for _, uri := range service.URIs {
			if strings.HasSuffix(uri, ".localtest.me") {
				host = uri
				return true
			}
		}
		return false
	}, 90*time.Second, time.Second, "provider must publish generated URI for %s", lid)
	return host
}

func writeGatewaySDL(t *testing.T, test gatewayWorkload) string {
	t.Helper()
	storage := []any{map[string]any{"size": "128Mi"}}
	service := map[string]any{
		"image": "provider-validation-app:local",
		"env":   []string{"IDENTITY=" + test.name},
	}
	expose := map[string]any{"port": 8080, "as": 80, "to": []any{map[string]any{"global": true}}}
	if test.customHost {
		expose["accept"] = []string{test.name + ".localhost"}
	}
	if test.bodyLimit > 0 {
		expose["http_options"] = map[string]any{"max_body_size": test.bodyLimit, "read_timeout": 5000, "send_timeout": 5000, "next_tries": 3, "next_timeout": 3000}
	}
	service["expose"] = []any{expose}
	if test.persistent {
		storage = append(storage, map[string]any{"name": "data", "size": "128Mi", "attributes": map[string]any{"persistent": true, "class": "default"}})
		service["params"] = map[string]any{"storage": map[string]any{"data": map[string]any{"mount": "/data"}}}
		service["env"] = append(service["env"].([]string), "STORAGE_DIR=/data")
	}
	compute := map[string]any{"resources": map[string]any{"cpu": map[string]any{"units": "0.1"}, "memory": map[string]any{"size": "128Mi"}, "storage": storage}}
	services := map[string]any{"web": service}
	computes := map[string]any{"web": compute}
	prices := map[string]any{"web": map[string]any{"denom": "uact", "amount": 10000}}
	deployments := map[string]any{"web": map[string]any{"local": map[string]any{"profile": "web", "count": test.replicas}}}
	if test.backend {
		service["env"] = append(service["env"].([]string), "BACKEND_URL=http://backend:8080/")
		services["backend"] = map[string]any{"image": "provider-validation-app:local", "env": []string{"IDENTITY=internal-backend"}, "expose": []any{map[string]any{"port": 8080, "to": []any{map[string]any{"service": "web"}}}}}
		computes["backend"] = map[string]any{"resources": map[string]any{"cpu": map[string]any{"units": "0.1"}, "memory": map[string]any{"size": "128Mi"}, "storage": []any{map[string]any{"size": "128Mi"}}}}
		prices["backend"] = map[string]any{"denom": "uact", "amount": 10000}
		deployments["backend"] = map[string]any{"local": map[string]any{"profile": "backend", "count": 1}}
	}
	doc := map[string]any{"version": "2.0", "services": services, "profiles": map[string]any{"compute": computes, "placement": map[string]any{"local": map[string]any{"pricing": prices}}}, "deployment": deployments}
	data, err := yaml.Marshal(doc)
	require.NoError(t, err)
	file := filepath.Join(t.TempDir(), test.name+".yaml")
	require.NoError(t, os.WriteFile(file, data, 0600))
	writeGatewayArtifact(t, test.name+".yaml", data)
	return file
}

// Exercises actual chain transactions, provider bidding, manifest delivery and HTTP.
// Keep traffic to the first deployment flowing throughout the later operations.
func (s *E2EGatewayAPI) TestLocalGatewayLifecycle() {
	defer s.captureGatewayState()
	kc := s.ctx.Value(fromctx.CtxKeyKubeClientSet).(kubernetes.Interface)
	ns, err := kc.CoreV1().Namespaces().Get(s.ctx, "kube-system", metav1.GetOptions{})
	s.Require().NoError(err)
	s.Require().Equal("true", ns.Labels["akash.network/local-validation"], "failure injection requires the disposable cluster marker")

	helloPath, err := filepath.Abs("../testdata/gateway-validation/hello-world.yaml")
	s.Require().NoError(err)
	helloID := s.createGatewayLease(300, helloPath)
	helloHost := s.gatewayLeaseHost(helloID)
	s.awaitGateway(helloHost, "akash")
	s.T().Logf("full local-chain hello-world: lease=%s hostname=%s HTTP200", helloID, helloHost)

	var mu sync.Mutex
	probes := make([]gatewayProbe, 0)
	probeCtx, stopProbes := context.WithCancel(s.ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-probeCtx.Done():
				return
			case <-ticker.C:
				status, body, err := s.gatewayRequest(helloHost, "GET", "/", nil)
				row := gatewayProbe{At: time.Now().UTC(), Host: helloHost, Status: status}
				if err != nil {
					row.Error = err.Error()
				} else if !strings.Contains(strings.ToLower(body), "akash") {
					row.Error = "unexpected response body"
				}
				mu.Lock()
				probes = append(probes, row)
				mu.Unlock()
			}
		}
	}()
	defer func() {
		stopProbes()
		<-done
		mu.Lock()
		defer mu.Unlock()
		data, err := json.MarshalIndent(probes, "", "  ")
		s.Assert().NoError(err)
		writeGatewayArtifact(s.T(), "continuous-http.json", data)
		failures := 0
		for _, p := range probes {
			if p.Status != 200 || p.Error != "" {
				failures++
			}
		}
		s.T().Logf("continuous hello-world traffic: %d requests, %d failures", len(probes), failures)
		s.Assert().Zero(failures, "unrelated healthy deployment must remain reachable")
	}()

	cases := []gatewayWorkload{
		{name: "custom-options", customHost: true, replicas: 1, bodyLimit: 1024},
		{name: "replicas", replicas: 2},
		{name: "multi-service", replicas: 1, backend: true},
		{name: "persistent", replicas: 1, persistent: true},
	}
	for i, test := range cases {
		lid := s.createGatewayLease(uint64(301+i), writeGatewaySDL(s.T(), test))
		host := s.gatewayLeaseHost(lid)
		podCount := test.replicas
		if test.backend {
			podCount++
		}
		s.awaitReadyWorkload(lid, podCount, nil)
		s.awaitGateway(host, test.name)
		if test.customHost {
			s.awaitGateway(test.name+".localhost", test.name)
		}
		for n := 0; n < 20; n++ {
			status, body, err := s.gatewayRequest(host, "GET", "/", nil)
			s.Require().NoError(err)
			s.Require().Equal(200, status)
			s.Require().Contains(body, test.name)
		}
		if test.replicas == 2 {
			pods, err := kc.CoreV1().Pods(builder.LidNS(lid)).List(s.ctx, metav1.ListOptions{})
			s.Require().NoError(err)
			oldUIDs := make(map[types.UID]bool)
			for _, pod := range pods.Items {
				oldUIDs[pod.UID] = true
			}
			test.name += "-updated"
			updatedFile := writeGatewaySDL(s.T(), test)
			res, err := clitestutil.ExecDeploymentUpdate(s.ctx, s.cctx, cli.TestFlags().With(updatedFile).WithFrom(s.addrTenant.String()).WithDSeq(lid.DSeq).Append(cliFlags)...)
			s.Require().NoError(err)
			s.Require().NoError(s.network.WaitForNextBlock())
			clitestutil.ValidateTxSuccessful(s.ctx, s.T(), s.cctx, res.Bytes())
			_, err = ptestutil.ExecSendManifest(s.ctx, s.cctx, cli.TestFlags().With(updatedFile).WithHome(s.validator.ClientCtx.HomeDir).WithFrom(s.addrTenant.String()).WithDSeq(lid.DSeq).WithOutputJSON()...)
			s.Require().NoError(err)
			s.awaitReadyWorkload(lid, 2, oldUIDs)
			s.awaitGateway(host, test.name)
			s.Require().Equal(host, s.gatewayLeaseHost(lid), "manifest update must preserve the generated URI")
		}
		if test.bodyLimit > 0 {
			status, body, err := s.gatewayRequest(host, "POST", "/echo", []byte("small body"))
			s.Require().NoError(err)
			s.Require().Equal(200, status)
			s.Require().Equal("small body", body)
			status, _, err = s.gatewayRequest(host, "POST", "/echo", bytes.Repeat([]byte("x"), 2048))
			s.Require().NoError(err)
			s.Require().Equal(413, status)
			status, body, err = s.gatewayRequest(host, "GET", "/stream", nil)
			s.Require().NoError(err)
			s.Require().Equal(200, status)
			s.Require().Equal("a\nb\nc\n", body)
		}
		if test.backend {
			status, body, err := s.gatewayRequest(host, "GET", "/backend", nil)
			s.Require().NoError(err)
			s.Require().Equal(200, status)
			s.Require().Contains(body, "internal-backend")
		}
		if test.persistent {
			status, _, err := s.gatewayRequest(host, "POST", "/value", []byte("survives-pod-replacement"))
			s.Require().NoError(err)
			s.Require().Equal(200, status)
			pods, err := kc.CoreV1().Pods(builder.LidNS(lid)).List(s.ctx, metav1.ListOptions{})
			s.Require().NoError(err)
			s.Require().NotEmpty(pods.Items)
			oldUIDs := make(map[types.UID]bool)
			for _, pod := range pods.Items {
				oldUIDs[pod.UID] = true
				s.Require().NoError(kc.CoreV1().Pods(pod.Namespace).Delete(s.ctx, pod.Name, metav1.DeleteOptions{}))
			}
			s.awaitReadyWorkload(lid, 1, oldUIDs)
			s.awaitGateway(host, test.name)
			s.Require().Eventually(func() bool {
				status, body, err := s.gatewayRequest(host, "GET", "/value", nil)
				return err == nil && status == 200 && body == "survives-pod-replacement"
			}, 120*time.Second, 500*time.Millisecond, "PVC content must survive pod replacement")
		}
		// Remove the actual filter referenced by this lease's generated hostname.
		routes, err := s.dc.Resource(httpRouteGVR).Namespace(builder.LidNS(lid)).List(s.ctx, metav1.ListOptions{})
		s.Require().NoError(err)
		s.Require().NotEmpty(routes.Items)
		routeName := host
		s.Require().NoError(s.dc.Resource(snippetsFilterGVR).Namespace(builder.LidNS(lid)).Delete(s.ctx, routeName, metav1.DeleteOptions{}))
		s.Require().Eventually(func() bool {
			_, err := s.dc.Resource(snippetsFilterGVR).Namespace(builder.LidNS(lid)).Get(s.ctx, routeName, metav1.GetOptions{})
			return err == nil
		}, 90*time.Second, 500*time.Millisecond)
		s.awaitGateway(host, test.name)
		s.T().Logf("PASS workload=%s lease=%s hostname=%s; original URI recovered after filter deletion", test.name, lid, host)
	}
	// Restart only the NGF controller; the data plane and all existing routes remain.
	patch := []byte(fmt.Sprintf(`{"spec":{"template":{"metadata":{"annotations":{"validation-restart":"%d"}}}}}`, time.Now().UnixNano()))
	_, err = kc.AppsV1().Deployments("nginx-gateway").Patch(s.ctx, "nginx-gateway", types.MergePatchType, patch, metav1.PatchOptions{})
	s.Require().NoError(err)
	newTest := gatewayWorkload{name: "during-controller-restart", replicas: 1}
	lid := s.createGatewayLease(305, writeGatewaySDL(s.T(), newTest))
	host := s.gatewayLeaseHost(lid)
	s.awaitGateway(host, newTest.name)
	s.T().Logf("PASS new deployment during NGF controller restart: lease=%s hostname=%s HTTP200", lid, host)
}
