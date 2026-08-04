package builder

import (
	"bytes"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"

	corev1 "k8s.io/api/core/v1"
	kvalidation "k8s.io/apimachinery/pkg/util/validation"

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

	// CCInitData configures the public Trustee connection and measured guest
	// policy used by confidential workloads that contain sealed environment
	// values, tenant-signed persistent-volume key references, or KBS registry
	// credential references. It never holds a KBS administrator credential or
	// secret plaintext.
	CCInitData *CCInitDataSettings

	// CCPersistentStorageClasses is the operator-maintained set of Block
	// storage classes qualified to return deterministically sanitized volumes.
	// An empty set disables confidential persistent storage.
	CCPersistentStorageClasses map[string]struct{}
}

// CCInitDataSettings contains the operator-controlled, non-secret inputs from
// which the provider deterministically constructs Kata initdata.
type CCInitDataSettings struct {
	KBSURL                 string
	KBSCertificate         string
	ImageSecurityPolicyURI string
	AgentPolicy            string
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

	if settings.CCInitData != nil {
		if err := validateCCInitDataSettings(*settings.CCInitData); err != nil {
			return fmt.Errorf("%w: confidential-compute initdata: %w", ErrSettingsValidation, err)
		}
	}

	for storageClass := range settings.CCPersistentStorageClasses {
		if errs := kvalidation.IsDNS1123Subdomain(storageClass); len(errs) != 0 {
			return fmt.Errorf(
				"%w: invalid confidential persistent storage class %q: %s",
				ErrSettingsValidation,
				storageClass,
				strings.Join(errs, "; "),
			)
		}
	}
	if len(settings.CCPersistentStorageClasses) != 0 && settings.CCInitData == nil {
		return fmt.Errorf(
			"%w: confidential persistent storage classes require confidential-compute initdata",
			ErrSettingsValidation,
		)
	}

	return nil
}

func validateCCInitDataSettings(settings CCInitDataSettings) error {
	kbsURL, err := url.Parse(settings.KBSURL)
	if err != nil || kbsURL.Scheme != "https" || kbsURL.Host == "" {
		return errors.New("KBS URL must be an HTTPS origin")
	}
	if kbsURL.User != nil || kbsURL.Path != "" || kbsURL.RawPath != "" || kbsURL.Opaque != "" ||
		kbsURL.RawQuery != "" || kbsURL.ForceQuery || kbsURL.Fragment != "" {
		return errors.New("KBS URL must not contain credentials, a path, query, or fragment")
	}

	if strings.ContainsAny(settings.KBSCertificate, "\x00\r") {
		return errors.New("KBS certificate contains unsupported control characters")
	}
	certificate := bytes.TrimSpace([]byte(settings.KBSCertificate))
	certificates := 0
	for len(certificate) != 0 {
		if !bytes.HasPrefix(certificate, []byte("-----BEGIN CERTIFICATE-----")) {
			return errors.New("KBS certificate must contain only PEM certificates")
		}
		block, rest := pem.Decode(certificate)
		if block == nil || block.Type != "CERTIFICATE" || len(block.Headers) != 0 {
			return errors.New("KBS certificate must contain only PEM certificates")
		}
		if _, err := x509.ParseCertificate(block.Bytes); err != nil {
			return fmt.Errorf("parse KBS certificate: %w", err)
		}
		certificates++
		certificate = bytes.TrimSpace(rest)
	}
	if certificates == 0 || certificates > 5 {
		return errors.New("KBS certificate chain must contain between one and five certificates")
	}

	policyURI, err := url.Parse(settings.ImageSecurityPolicyURI)
	if err != nil || policyURI.Scheme != "kbs" || policyURI.Host != "" || policyURI.RawPath != "" ||
		policyURI.Opaque != "" || policyURI.RawQuery != "" || policyURI.ForceQuery || policyURI.Fragment != "" {
		return errors.New("image security policy URI must be a kbs:/// resource URI")
	}
	parts := strings.Split(strings.TrimPrefix(policyURI.Path, "/"), "/")
	if len(parts) != 3 || parts[0] == "" || parts[1] != "security-policy" || !strings.HasPrefix(parts[2], "sha256-") {
		return errors.New("image security policy URI must be content addressed")
	}
	digest := strings.TrimPrefix(parts[2], "sha256-")
	decodedDigest, err := hex.DecodeString(digest)
	if err != nil || len(decodedDigest) != 32 || digest != strings.ToLower(digest) {
		return errors.New("image security policy URI must end in a lowercase SHA-256 digest")
	}

	if len(settings.AgentPolicy) == 0 || len(settings.AgentPolicy) > 1024*1024 {
		return errors.New("agent policy must be between one byte and one MiB")
	}
	if !strings.Contains(settings.AgentPolicy, "package agent_policy") {
		return errors.New("agent policy must declare package agent_policy")
	}
	if !isInitDataTOMLSafe(settings.AgentPolicy) {
		return errors.New("agent policy cannot be represented safely in initdata TOML")
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
