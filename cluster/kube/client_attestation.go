package kube

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/net"

	mtypes "pkg.akt.dev/go/node/market/v1"

	"github.com/akash-network/provider/cluster/kube/builder"
	ctypes "github.com/akash-network/provider/cluster/types/v1beta3"
)

const (
	attestationSidecarContainerName = "akash-attestation-sidecar"
	attestationSidecarPort          = "8790"
	intelTDXLabelKey                = "intel.feature.node.kubernetes.io/tdx"
	amdSNPLabelKey                  = "amd.feature.node.kubernetes.io/snp"
)

// AttestationQuote forwards an attestation quote request to the sidecar running
// inside a confidential compute pod. It uses the K8s API server's pod proxy
// subresource to reach the sidecar, which works both in-cluster and out-of-cluster
// (e.g., macOS local dev via Kind where pod IPs aren't routable from the host).
//
// The request path is: provider → K8s API server → kubelet → pod:8790/quote
// This follows the same pattern as Exec (which also goes through the API server).
func (c *client) AttestationQuote(ctx context.Context, leaseID mtypes.LeaseID, service string, podIndex uint, requestBody []byte) ([]byte, int, error) {
	namespace := builder.LidNS(leaseID)

	if err := c.leaseExists(ctx, leaseID); err != nil {
		return nil, http.StatusNotFound, err
	}

	podName, err := c.findAttestationSidecarPod(ctx, namespace, service, podIndex)
	if err != nil {
		return nil, http.StatusNotFound, err
	}

	// Use the K8s API server's pod proxy to POST to the sidecar's /quote endpoint.
	// Path: /api/v1/namespaces/{ns}/pods/{scheme}:{pod}:{port}/proxy/quote
	body, err := c.kc.CoreV1().RESTClient().Post().
		Namespace(namespace).
		Resource("pods").
		SubResource("proxy").
		Name(net.JoinSchemeNamePort("https", podName, attestationSidecarPort)).
		Suffix("quote").
		SetHeader("Content-Type", "application/json").
		Body(requestBody).
		DoRaw(ctx)

	if err != nil {
		code := http.StatusBadGateway
		if se := new(apierrors.StatusError); errors.As(err, &se) {
			code = int(se.ErrStatus.Code)
		}
		return nil, code, fmt.Errorf("proxy to attestation sidecar: %w", err)
	}

	return body, http.StatusOK, nil
}

// findAttestationSidecarPod finds a running pod in the lease namespace that
// contains the attestation sidecar container. Returns the pod name.
//
// When service is non-empty, pods are filtered by the manifest-service label
// and sorted by name for deterministic replica selection via podIndex (same
// pattern as Exec). When service is empty, returns the first running pod with
// the sidecar (legacy behavior).
func (c *client) findAttestationSidecarPod(ctx context.Context, namespace string, service string, podIndex uint) (string, error) {
	labelSelector := fmt.Sprintf("%s=true", builder.AkashManagedLabelName)
	if service != "" {
		labelSelector = fmt.Sprintf("%s=%s", builder.AkashManifestServiceLabelName, service)
	}

	pods, err := wrapKubeCall("pods-list", func() (*corev1.PodList, error) {
		return c.kc.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
			LabelSelector: labelSelector,
		})
	})
	if err != nil {
		return "", fmt.Errorf("list pods in %s: %w", namespace, err)
	}

	// Filter to running pods that have the sidecar container.
	var candidates []corev1.Pod
	for _, pod := range pods.Items {
		if pod.Status.Phase != corev1.PodRunning {
			continue
		}
		for _, container := range pod.Spec.Containers {
			if container.Name == attestationSidecarContainerName {
				candidates = append(candidates, pod)
				break
			}
		}
	}

	if len(candidates) == 0 {
		if service != "" {
			return "", fmt.Errorf("no running pod with attestation sidecar for service %q in namespace %s", service, namespace)
		}
		return "", fmt.Errorf("no running pod with attestation sidecar in namespace %s", namespace)
	}

	if service == "" {
		return candidates[0].Name, nil
	}

	sort.Sort(sortablePods(candidates))

	if podIndex >= uint(len(candidates)) {
		return "", fmt.Errorf("podIndex %d out of range: service %q has %d attestation-capable pod(s)", podIndex, service, len(candidates))
	}

	return candidates[podIndex].Name, nil
}

// DetectTEEPlatform scans K8s nodes to determine the TEE platform available.
// Returns TEEPlatformTDX if any node has the TDX label, TEEPlatformSNP for SNP,
// or TEEPlatformNone if no CC-capable nodes are found.
func (c *client) DetectTEEPlatform(ctx context.Context) ctypes.TEEPlatform {
	nodes, err := c.kc.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return ctypes.TEEPlatformNone
	}

	for _, node := range nodes.Items {
		if node.Labels[intelTDXLabelKey] == "true" {
			return ctypes.TEEPlatformTDX
		}
		if node.Labels[amdSNPLabelKey] == "true" {
			return ctypes.TEEPlatformSNP
		}
	}

	return ctypes.TEEPlatformNone
}
