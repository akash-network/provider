package builder

import (
	"errors"
	"fmt"
	"net"

	corev1 "k8s.io/api/core/v1"

	vutil "pkg.akt.dev/node/v2/util/validation"
)

// IngressMode represents the ingress mode for the cluster.
type IngressMode string

const (
	IngressModeIngress IngressMode = "ingress"
	IngressModeGateway IngressMode = "gateway-api"
)

// ParseIngressMode parses a string into an IngressMode, returning an error if the value is invalid.
func ParseIngressMode(s string) (IngressMode, error) {
	switch IngressMode(s) {
	case IngressModeIngress, IngressModeGateway:
		return IngressMode(s), nil
	default:
		return "", fmt.Errorf("invalid ingress-mode %q: must be %q or %q", s, IngressModeIngress, IngressModeGateway)
	}
}

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

	// APIServerEndpoints are the addresses of all Kubernetes API server backends
	// (from the "kubernetes" endpoints in the default namespace, not the ClusterIP).
	// HA control planes have multiple backends; all must be allowed in network
	// policies because CNIs like Calico evaluate egress rules after DNAT.
	APIServerEndpoints []net.TCPAddr

	CPUCommitLevel     float64
	GPUCommitLevel     float64
	MemoryCommitLevel  float64
	StorageCommitLevel float64

	DeploymentRuntimeClass string

	// Name of the image pull secret to use in pod spec
	DockerImagePullSecretsName string

	// Ingress mode: "ingress" or "gateway-api"
	IngressMode IngressMode

	// Gateway name when using gateway-api mode
	GatewayName string

	// Gateway namespace when using gateway-api mode
	GatewayNamespace string

	// Gateway provider when using gateway-api mode
	GatewayProvider string
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
