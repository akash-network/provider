package builder

import (
	"errors"
	"fmt"
	"net"

	corev1 "k8s.io/api/core/v1"

	vutil "pkg.akt.dev/node/v2/util/validation"
)

// Settings configures k8s object generation such that it is customized to the
// cluster environment that is being used.
// For instance, GCP requires a different service type than minikube.
type Settings struct {
	// gcp:    NodePort
	// others: ClusterIP
	DeploymentServiceType corev1.ServiceType

	// gcp:    false
	// others: true
	DeploymentIngressStaticHosts bool
	// Ingress domain to map deployments to
	DeploymentIngressDomain string

	// Return load balancer host in lease status command ?
	// gcp:    true
	// others: optional
	DeploymentIngressExposeLBHosts bool

	// Global hostname for arbitrary ports
	ClusterPublicHostname string

	// NetworkPoliciesEnabled determines if NetworkPolicies should be installed.
	NetworkPoliciesEnabled bool

	// APIServerEndpoint is the address of the Kubernetes API server
	// (from the "kubernetes" endpoints in the default namespace, not the ClusterIP).
	// This is needed for network policies because CNIs like Calico evaluate
	// egress rules after DNAT, so the ClusterIP is not what gets matched.
	APIServerEndpoint net.TCPAddr

	CPUCommitLevel     float64
	GPUCommitLevel     float64
	MemoryCommitLevel  float64
	StorageCommitLevel float64

	DeploymentRuntimeClass string

	// Name of the image pull secret to use in pod spec
	DockerImagePullSecretsName string
}

var ErrSettingsValidation = errors.New("settings validation")

func ValidateSettings(settings Settings) error {
	if settings.DeploymentIngressStaticHosts {
		if settings.DeploymentIngressDomain == "" {
			return fmt.Errorf("%w: empty ingress domain", ErrSettingsValidation)
		}

		if !vutil.IsDomainName(settings.DeploymentIngressDomain) {
			return fmt.Errorf("%w: invalid domain name %q", ErrSettingsValidation, settings.DeploymentIngressDomain)
		}
	}

	return nil
}

func NewDefaultSettings() Settings {
	return Settings{
		DeploymentServiceType:          corev1.ServiceTypeClusterIP,
		DeploymentIngressStaticHosts:   false,
		DeploymentIngressExposeLBHosts: false,
		NetworkPoliciesEnabled:         false,
	}
}

type ContextKey string

const SettingsKey = ContextKey("kube-client-settings")
