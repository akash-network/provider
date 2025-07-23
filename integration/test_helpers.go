//go:build e2e

package integration

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

const (
	providerTemplate = `host: %s
jwt-host: %s
attributes:
  - key: region
    value: us-west
  - key: moniker
    value: akash
  - key: capabilities/storage/1/persistent
    value: true
  - key: capabilities/storage/1/class
    value: default
  - key: capabilities/storage/2/persistent
    value: true
  - key: capabilities/storage/2/class
    value: beta2
  - key: capabilities/storage/3/persistent
    value: false
  - key: capabilities/storage/3/class
    value: ram
`
)

type queryOpt struct {
	body *bytes.Buffer
}

type queryOption func(*queryOpt)

func queryWithBody(val []byte) queryOption {
	return func(opt *queryOpt) {
		if len(val) > 0 {
			opt.body = bytes.NewBuffer(val)
		}
	}
}

// skip integration-only tests.
// using build tags breaks tooling for compilation, etc...
func integrationTestOnly(t testing.TB) {
	t.Helper()
	val, found := os.LookupEnv("TEST_INTEGRATION")
	if !found || val != "true" {
		t.Skip("SKIPPING INTEGRATION TEST")
	}
}

// nolint: unparam
func queryAppWithRetries(t *testing.T, appURL string, appHost string, limit int, opts ...queryOption) *http.Response {
	t.Helper()

	opt := &queryOpt{
		body: bytes.NewBuffer([]byte{}),
	}

	for _, o := range opts {
		o(opt)
	}

	req, err := http.NewRequest("GET", appURL, opt.body)
	require.NoError(t, err)
	req.Host = appHost
	req.Header.Add("Cache-Control", "no-cache")
	req.Header.Add("Connection", "keep-alive")
	tr := &http.Transport{
		DisableKeepAlives: false,
		DialContext: (&net.Dialer{
			Timeout:       1 * time.Second,
			KeepAlive:     1 * time.Second,
			FallbackDelay: time.Duration(-1),
		}).DialContext,
	}
	httpClient := &http.Client{
		Transport: tr,
	}

	var resp *http.Response
	const delay = 1 * time.Second
	for i := 0; i != limit; i++ {
		resp, err = httpClient.Do(req)
		if resp != nil {
			t.Log("GET: ", appURL, resp.StatusCode)
		}
		if err != nil {
			time.Sleep(delay)
			continue
		}
		if resp != nil && resp.StatusCode == http.StatusOK {
			break
		}
		time.Sleep(delay)
	}
	require.NoError(t, err)

	return resp
}
func queryApp(t *testing.T, appURL string, limit int) {
	t.Helper()
	queryAppWithHostname(t, appURL, limit, "test.localhost")
}

// nolint: unparam
func queryAppWithHostname(t *testing.T, appURL string, limit int, hostname string, opts ...queryOption) {
	t.Helper()
	// Assert provider launches app in kind cluster

	opt := &queryOpt{
		body: bytes.NewBuffer([]byte{}),
	}

	for _, o := range opts {
		o(opt)
	}

	req, err := http.NewRequest("GET", appURL, opt.body)

	require.NoError(t, err)
	req.Host = hostname // NOTE: cannot be inserted as a req.Header element, that is overwritten by this req.Host field.
	req.Header.Add("Cache-Control", "no-cache")
	req.Header.Add("Connection", "keep-alive")
	// Assert that the service is accessible. Unfortunately brittle single request.
	tr := &http.Transport{
		DisableKeepAlives: false,
	}
	httpClient := &http.Client{
		Transport: tr,
	}

	// retry mechanism
	var resp *http.Response
	for i := 0; i < limit; i++ {
		time.Sleep(1 * time.Second) // reduce absurdly long wait period
		resp, err = httpClient.Do(req)
		if err != nil {
			t.Log(err)
			continue
		}
		if resp != nil && resp.StatusCode == http.StatusOK {
			err = nil
			break
		}
	}
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Contains(t, string(body), "The Future of The Cloud is Decentralized")
}

// appEnv asserts that there is an addressable docker container for KinD
func appEnv(t *testing.T) (string, string) {
	t.Helper()
	host := os.Getenv("KUBE_INGRESS_IP")
	require.NotEmpty(t, host)
	appPort := os.Getenv("KUBE_INGRESS_PORT")
	require.NotEmpty(t, appPort)
	return host, appPort
}

type Observer struct {
	initialUIDs     map[string]struct{}
	initialRestarts map[string]int32
	namespace       string
}

type ErrUnexpectedStateChange struct {
	reason string
}

func (e ErrUnexpectedStateChange) Error() string {
	return fmt.Sprintf("unexpected state change: %s", e.reason)
}

func NewObserver(namespace string) *Observer {
	return &Observer{
		initialUIDs:     make(map[string]struct{}),
		initialRestarts: make(map[string]int32),
		namespace:       namespace,
	}
}

func (o *Observer) Observe(kubeClient kubernetes.Interface) error {
	initialPods, err := kubeClient.CoreV1().Pods(o.namespace).List(context.Background(), metav1.ListOptions{
		LabelSelector: "akash.network/manifest-service=web",
	})
	if err != nil {
		return err
	}

	for _, pod := range initialPods.Items {
		o.initialUIDs[string(pod.UID)] = struct{}{}
		for _, containerStatus := range pod.Status.ContainerStatuses {
			o.initialRestarts[pod.Name] = containerStatus.RestartCount
		}
	}

	return nil
}

func (o *Observer) podRestartedOrNewPodCreated(pod corev1.Pod) bool {
	if o.newPodCreated(pod) {
		return true
	}

	for _, containerStatus := range pod.Status.ContainerStatuses {
		if containerStatus.RestartCount > o.initialRestarts[pod.Name] {
			return true
		}
	}
	return false
}

func (o *Observer) newPodCreated(pod corev1.Pod) bool {
	_, exists := o.initialUIDs[string(pod.UID)]
	return !exists
}

func (o *Observer) VerifyNewPodCreate(kubeClient kubernetes.Interface) error {
	timeout := time.After(30 * time.Second)
	tick := time.Tick(5 * time.Second)
	for {
		select {
		case <-timeout:
			return errors.New("timeout waiting for deployment update")
		case <-tick:
			updatedPods, err := kubeClient.CoreV1().Pods(o.namespace).List(context.Background(), metav1.ListOptions{
				LabelSelector: "akash.network/manifest-service=web",
			})
			if err != nil {
				return err
			}

			newPodCreated := false
			for _, pod := range updatedPods.Items {
				// Check if this is a new pod
				newPodCreated = o.newPodCreated(pod)
				if newPodCreated {
					break
				}
			}

			if newPodCreated {
				return nil
			}
		}
	}
}

func (o *Observer) VerifyNoChangeOccurred(kubeClient kubernetes.Interface) error {
	timeout := time.After(30 * time.Second)
	tick := time.Tick(5 * time.Second)
	for {
		select {
		case <-timeout:
			return nil
		case <-tick:
			updatedPods, err := kubeClient.CoreV1().Pods(o.namespace).List(context.Background(), metav1.ListOptions{
				LabelSelector: "akash.network/manifest-service=web",
			})
			if err != nil {
				return err
			}

			for _, pod := range updatedPods.Items {
				if o.podRestartedOrNewPodCreated(pod) {
					return ErrUnexpectedStateChange{"container restarted or new pod created"}
				}
			}
		}
	}
}
