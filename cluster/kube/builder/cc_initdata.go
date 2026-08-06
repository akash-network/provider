package builder

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	mani "pkg.akt.dev/go/manifest/v2beta3"
)

const (
	ccInitDataAnnotation            = "io.katacontainers.config.hypervisor.cc_init_data"
	AkashCCInitDataSHA256Annotation = "akash.network/cc-initdata-sha256"
	ccInitDataVersion               = "0.1.0"
	ccInitDataMaxRawBytes           = 1024 * 1024
	ccInitDataMaxEncodedBytes       = 240 * 1024
	ccInitDataAAKey                 = "aa.toml"
	ccInitDataCDHKey                = "cdh.toml"
	ccInitDataPolicyKey             = "policy.rego"
	ccInitDataSecureVolumesKey      = "akash-secure-volumes.json"
)

type ccPersistentStorageDescriptor struct {
	Version       string                         `json:"version"`
	ContainerName string                         `json:"containerName"`
	Volumes       []ccPersistentVolumeDescriptor `json:"volumes"`
}

type ccPersistentVolumeDescriptor struct {
	DevicePath string `json:"devicePath"`
	KeyRef     string `json:"keyRef"`
	MountPath  string `json:"mountPath"`
	ReadOnly   bool   `json:"readOnly"`
	VolumeID   string `json:"volumeId"`
}

func (b *Workload) confidentialInitDataAnnotation() (string, string, error) {
	service := &b.group.Services[b.serviceIdx]
	hasSealedEnvironment, err := serviceHasSealedEnvironment(service.Env)
	if err != nil {
		return "", "", err
	}
	required := hasSealedEnvironment || len(b.secureVolumes) != 0 || b.registryCredentialsURI != ""
	kbsSelection := serviceKBSSelection(service)
	required = required || kbsSelection != nil
	if !required {
		return "", "", nil
	}

	sparams := b.sparams[b.serviceIdx]
	if sparams == nil || !sparams.RuntimeClass.Is(WithCC()) {
		return "", "", fmt.Errorf("sealed workload data requires a confidential runtime")
	}
	settings, err := b.resolveCCInitDataSettings(kbsSelection)
	if err != nil {
		return "", "", err
	}

	algorithm := ""
	switch {
	case sparams.RuntimeClass.Is(WithSNP()):
		algorithm = "sha256"
	case sparams.RuntimeClass.Is(WithTDX()):
		algorithm = "sha384"
	default:
		return "", "", fmt.Errorf("sealed workload data uses an unsupported confidential runtime %q", sparams.RuntimeClass)
	}

	raw, err := buildConfidentialInitData(
		settings,
		algorithm,
		service.Name,
		b.secureVolumes,
		b.registryCredentialsURI,
	)
	if err != nil {
		return "", "", err
	}
	if len(raw) > ccInitDataMaxRawBytes {
		return "", "", fmt.Errorf("confidential-compute initdata exceeds the one MiB guest limit")
	}

	var compressed bytes.Buffer
	writer, err := gzip.NewWriterLevel(&compressed, gzip.BestCompression)
	if err != nil {
		return "", "", fmt.Errorf("create initdata compressor: %w", err)
	}
	if _, err := writer.Write(raw); err != nil {
		return "", "", fmt.Errorf("compress initdata: %w", err)
	}
	if err := writer.Close(); err != nil {
		return "", "", fmt.Errorf("finish initdata compression: %w", err)
	}

	annotation := base64.StdEncoding.EncodeToString(compressed.Bytes())
	if len(annotation) > ccInitDataMaxEncodedBytes {
		return "", "", fmt.Errorf("compressed confidential-compute initdata exceeds the Kubernetes annotation limit")
	}
	digest := sha256.Sum256(raw)

	return annotation, hex.EncodeToString(digest[:]), nil
}

func serviceKBSSelection(service *mani.Service) *mani.KBSParams {
	if service.Params == nil || service.Params.TEE == nil {
		return nil
	}
	return service.Params.TEE.KBS
}

func (b *Workload) resolveCCInitDataSettings(selection *mani.KBSParams) (CCInitDataSettings, error) {
	if selection == nil {
		return CCInitDataSettings{}, fmt.Errorf(
			"sealed workload data requires an explicit provider or tenant KBS selection",
		)
	}

	switch source := selection.Source.(type) {
	case *mani.KBSParams_Provider:
		if source.Provider == nil {
			return CCInitDataSettings{}, fmt.Errorf("provider-managed KBS selection is empty")
		}
		if b.settings.CCInitData == nil {
			return CCInitDataSettings{}, fmt.Errorf(
				"provider-managed KBS selection requires provider confidential-compute initdata settings",
			)
		}
		return *b.settings.CCInitData, nil
	case *mani.KBSParams_Tenant:
		if source.Tenant == nil {
			return CCInitDataSettings{}, fmt.Errorf("tenant-managed KBS selection is empty")
		}
		return CCInitDataSettings{
			KBSURL:                 source.Tenant.URL,
			KBSCertificate:         source.Tenant.Certificate,
			ImageSecurityPolicyURI: source.Tenant.ImageSecurityPolicyURI,
			AgentPolicy:            source.Tenant.AgentPolicy,
		}, nil
	default:
		return CCInitDataSettings{}, fmt.Errorf("KBS selection must be provider or tenant managed")
	}
}

func serviceHasSealedEnvironment(environment []string) (bool, error) {
	found := false
	for _, entry := range environment {
		_, value, ok := strings.Cut(entry, "=")
		if !ok || !strings.HasPrefix(value, "sealed.") {
			continue
		}
		if len(value) > ccSealedKeyRefMaxBytes || !ccSealedKeyRefPattern.MatchString(value) {
			return false, fmt.Errorf("environment contains a malformed sealed secret")
		}
		found = true
	}
	return found, nil
}

func buildConfidentialInitData(
	settings CCInitDataSettings,
	algorithm string,
	containerName string,
	volumes []ccPersistentVolume,
	registryCredentialsURI string,
) ([]byte, error) {
	if err := validateCCInitDataSettings(settings); err != nil {
		return nil, fmt.Errorf("invalid confidential-compute initdata settings: %w", err)
	}
	if algorithm != "sha256" && algorithm != "sha384" {
		return nil, fmt.Errorf("unsupported initdata digest algorithm %q", algorithm)
	}

	certificate := strings.TrimSpace(settings.KBSCertificate) + "\n"
	agentPolicy := settings.AgentPolicy
	if !strings.HasSuffix(agentPolicy, "\n") {
		agentPolicy += "\n"
	}
	aaConfig := "[token_configs]\n" +
		"[token_configs.kbs]\n" +
		fmt.Sprintf("url = %q\n", settings.KBSURL) +
		"cert = '''\n" + certificate + "'''\n"
	cdhConfig := "[kbc]\n" +
		"name = \"cc_kbc\"\n" +
		fmt.Sprintf("url = %q\n", settings.KBSURL) +
		"kbs_cert = '''\n" + certificate + "'''\n\n" +
		"[image]\n" +
		fmt.Sprintf("image_security_policy_uri = %q\n", settings.ImageSecurityPolicyURI)
	if registryCredentialsURI != "" {
		if err := validateCCKBSResourceURI(registryCredentialsURI); err != nil {
			return nil, err
		}
		cdhConfig += fmt.Sprintf(
			"authenticated_registry_credentials_uri = %q\n",
			registryCredentialsURI,
		)
	}

	var raw bytes.Buffer
	fmt.Fprintf(&raw, "version = %q\n", ccInitDataVersion)
	fmt.Fprintf(&raw, "algorithm = %q\n\n", algorithm)
	raw.WriteString("[data]\n")
	if err := writeInitDataMultiline(&raw, ccInitDataAAKey, aaConfig); err != nil {
		return nil, err
	}
	raw.WriteByte('\n')
	if err := writeInitDataMultiline(&raw, ccInitDataCDHKey, cdhConfig); err != nil {
		return nil, err
	}
	raw.WriteByte('\n')
	if err := writeInitDataMultiline(&raw, ccInitDataPolicyKey, agentPolicy); err != nil {
		return nil, err
	}

	if len(volumes) != 0 {
		descriptorVolumes := make([]ccPersistentVolumeDescriptor, 0, len(volumes))
		for _, volume := range volumes {
			descriptorVolumes = append(descriptorVolumes, ccPersistentVolumeDescriptor{
				DevicePath: volume.DevicePath,
				KeyRef:     volume.KeyRef,
				MountPath:  volume.MountPath,
				ReadOnly:   volume.ReadOnly,
				VolumeID:   volume.VolumeID,
			})
		}
		descriptor, err := json.Marshal(ccPersistentStorageDescriptor{
			ContainerName: containerName,
			Version:       "1",
			Volumes:       descriptorVolumes,
		})
		if err != nil {
			return nil, fmt.Errorf("encode confidential persistent-volume descriptor: %w", err)
		}
		raw.WriteByte('\n')
		if err := writeInitDataMultiline(&raw, ccInitDataSecureVolumesKey, string(descriptor)); err != nil {
			return nil, err
		}
	}

	return raw.Bytes(), nil
}

func writeInitDataMultiline(buffer *bytes.Buffer, name, value string) error {
	if !isInitDataTOMLSafe(value) {
		return fmt.Errorf("initdata value %q cannot be represented safely in TOML", name)
	}
	escaped := strings.ReplaceAll(value, `\`, `\\`)
	fmt.Fprintf(buffer, "%q = \"\"\"\n%s\"\"\"\n", name, escaped)
	return nil
}

func isInitDataTOMLSafe(value string) bool {
	return !strings.Contains(value, `"""`) && !strings.ContainsAny(value, "\x00\r")
}
