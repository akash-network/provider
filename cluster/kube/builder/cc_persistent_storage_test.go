package builder

import (
	"bytes"
	"compress/gzip"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"io"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"

	mani "pkg.akt.dev/go/manifest/v2beta3"
	attr "pkg.akt.dev/go/node/types/attributes/v1"
	rtypes "pkg.akt.dev/go/node/types/resources/v1beta4"
	"pkg.akt.dev/go/sdl"
	"pkg.akt.dev/go/testutil"

	crd "github.com/akash-network/provider/pkg/apis/akash.network/v2beta2"
)

const testSealedKeyRef = "sealed.eyJhbGciOiJFUzI1NiJ9.eyJuYW1lIjoia2JzOi8vL2RlZmF1bHQvdGVzdC9zaGEyNTYtMDAifQ.c2lnbmF0dXJl" // gitleaks:allow -- synthetic fixture

type ccStorageSpec struct {
	name       string
	mount      string
	persistent bool
	readOnly   bool
	keyRef     string
	class      string
}

func testCCInitDataSettings(t *testing.T) *CCInitDataSettings {
	t.Helper()
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "canary-kbs"},
		NotBefore:             time.Unix(0, 0),
		NotAfter:              time.Unix(4102444800, 0),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, privateKey.Public(), privateKey)
	require.NoError(t, err)
	certificate := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})

	return &CCInitDataSettings{
		KBSURL:                 "https://192.0.2.10:8080",
		KBSCertificate:         string(certificate),
		ImageSecurityPolicyURI: "kbs:///default/security-policy/sha256-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		AgentPolicy:            "package agent_policy\n\ndefault allow = true\n",
	}
}

func providerManagedKBSParams() *mani.KBSParams {
	return &mani.KBSParams{
		Source: &mani.KBSParams_Provider{Provider: &mani.ProviderKBSParams{}},
	}
}

func tenantManagedKBSParams(settings CCInitDataSettings) *mani.KBSParams {
	return &mani.KBSParams{
		Source: &mani.KBSParams_Tenant{Tenant: &mani.TenantKBSParams{
			URL:                    settings.KBSURL,
			Certificate:            settings.KBSCertificate,
			ImageSecurityPolicyURI: settings.ImageSecurityPolicyURI,
			AgentPolicy:            settings.AgentPolicy,
		}},
	}
}

func newCCStorageWorkload(t *testing.T, runtimeClass RuntimeClass, specs ...ccStorageSpec) *Workload {
	t.Helper()

	resourceStorage := make([]rtypes.Storage, 0, len(specs))
	parameterStorage := make([]mani.StorageParams, 0, len(specs))
	for _, spec := range specs {
		attributes := attr.Attributes{}
		if spec.persistent {
			attributes = append(attributes, attr.Attribute{Key: sdl.StorageAttributePersistent, Value: "true"})
			storageClass := spec.class
			if storageClass == "" {
				storageClass = "beta3"
			}
			attributes = append(attributes, attr.Attribute{Key: sdl.StorageAttributeClass, Value: storageClass})
		}
		resourceStorage = append(resourceStorage, rtypes.Storage{
			Name:       spec.name,
			Quantity:   rtypes.NewResourceValue(1 << 30),
			Attributes: attributes,
		})
		parameterStorage = append(parameterStorage, mani.StorageParams{
			Name:     spec.name,
			Mount:    spec.mount,
			ReadOnly: spec.readOnly,
			KeyRef:   spec.keyRef,
		})
	}

	params := &mani.ServiceParams{Storage: parameterStorage}
	if runtimeClass.Is(WithCC()) {
		teeType := "cpu"
		if runtimeClass.Is(WithGPU()) {
			teeType = "cpu-gpu"
		}
		params.TEE = &mani.TEEParams{
			Type:        teeType,
			Attestation: true,
			KBS:         providerManagedKBSParams(),
		}
	}
	service := mani.Service{
		Name:      "proof",
		Count:     1,
		Resources: rtypes.Resources{Storage: resourceStorage},
		Params:    params,
	}
	group := mani.Group{Services: mani.Services{service}}
	return &Workload{
		builder: builder{
			log: testutil.Logger(t),
			settings: Settings{
				CCInitData:                 testCCInitDataSettings(t),
				CCPersistentStorageClasses: map[string]struct{}{"beta3": {}},
			},
			deployment: &ClusterDeployment{Lid: testutil.LeaseID(t), Group: &group},
			group:      group,
			sparams:    []*crd.SchedulerParams{{RuntimeClass: runtimeClass}},
		},
		serviceIdx: 0,
	}
}

func prepareCCStorageWorkload(t *testing.T, workload *Workload) {
	t.Helper()
	var err error
	workload.registryCredentialsURI, err = workload.confidentialRegistryCredentialsURI()
	require.NoError(t, err)
	workload.secureVolumes, err = workload.confidentialPersistentVolumes()
	require.NoError(t, err)
	workload.ccInitDataAnnotation, workload.ccInitDataSHA256, err = workload.confidentialInitDataAnnotation()
	require.NoError(t, err)
}

func decodeCCInitData(t *testing.T, annotation string) ([]byte, map[string]any) {
	t.Helper()
	compressed, err := base64.StdEncoding.DecodeString(annotation)
	require.NoError(t, err)
	reader, err := gzip.NewReader(bytes.NewReader(compressed))
	require.NoError(t, err)
	raw, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.NoError(t, reader.Close())
	var document map[string]any
	require.NoError(t, toml.Unmarshal(raw, &document))
	return raw, document
}

func TestConfidentialPersistentStorageBuildsMeasuredBlockContract(t *testing.T) {
	workload := newCCStorageWorkload(t, RuntimeClassKataQemuNvidiaGPUSNP,
		ccStorageSpec{name: "data", mount: "/proof", persistent: true, keyRef: testSealedKeyRef},
	)
	prepareCCStorageWorkload(t, workload)

	pvcs := workload.persistentVolumeClaims()
	require.Len(t, pvcs, 1)
	require.Equal(t, corev1.PersistentVolumeBlock, *pvcs[0].Spec.VolumeMode)

	container := workload.container()
	require.Empty(t, container.VolumeMounts)
	require.Equal(t, []corev1.VolumeDevice{{
		Name:       "proof-data",
		DevicePath: "/dev/akash_secure/data",
	}}, container.VolumeDevices)

	annotations := workload.podAnnotations()
	raw, document := decodeCCInitData(t, annotations[ccInitDataAnnotation])
	require.Equal(t, "sha256", document["algorithm"])
	require.Equal(t, workload.ccInitDataSHA256, annotations[AkashCCInitDataSHA256Annotation])
	require.NotContains(t, string(raw), "agent.secure_volumes")
	require.NotContains(t, string(raw), "kernel_params")

	data := document["data"].(map[string]any)
	require.Contains(t, data, ccInitDataAAKey)
	require.Contains(t, data, ccInitDataCDHKey)
	require.Contains(t, data, ccInitDataPolicyKey)
	var descriptor struct {
		Version       string `json:"version"`
		ContainerName string `json:"containerName"`
		Volumes       []struct {
			DevicePath string `json:"devicePath"`
			KeyRef     string `json:"keyRef"`
			MountPath  string `json:"mountPath"`
			ReadOnly   bool   `json:"readOnly"`
			VolumeID   string `json:"volumeId"`
		} `json:"volumes"`
	}
	require.NoError(t, json.Unmarshal([]byte(data[ccInitDataSecureVolumesKey].(string)), &descriptor))
	require.Equal(t, "1", descriptor.Version)
	require.Equal(t, "proof", descriptor.ContainerName)
	require.Len(t, descriptor.Volumes, 1)
	require.Equal(t, "/dev/akash_secure/data", descriptor.Volumes[0].DevicePath)
	require.Equal(t, testSealedKeyRef, descriptor.Volumes[0].KeyRef)
	require.Equal(t, "/proof", descriptor.Volumes[0].MountPath)
	require.False(t, descriptor.Volumes[0].ReadOnly)
	expectedVolumeID, err := ccPersistentVolumeID(
		workload.deployment.LeaseID(),
		"proof",
		"data",
		testSealedKeyRef,
	)
	require.NoError(t, err)
	require.Equal(t, expectedVolumeID, descriptor.Volumes[0].VolumeID)
}

func TestConfidentialInitDataIsDeterministic(t *testing.T) {
	workload := newCCStorageWorkload(t, RuntimeClassKataQemuNvidiaGPUSNP,
		ccStorageSpec{name: "data", mount: "/proof", persistent: true, keyRef: testSealedKeyRef},
	)
	volumes, err := workload.confidentialPersistentVolumes()
	require.NoError(t, err)
	first, err := buildConfidentialInitData(*workload.settings.CCInitData, "sha256", "proof", volumes, "")
	require.NoError(t, err)
	second, err := buildConfidentialInitData(*workload.settings.CCInitData, "sha256", "proof", volumes, "")
	require.NoError(t, err)
	require.Equal(t, first, second)

	workload.secureVolumes = volumes
	firstAnnotation, firstDigest, err := workload.confidentialInitDataAnnotation()
	require.NoError(t, err)
	secondAnnotation, secondDigest, err := workload.confidentialInitDataAnnotation()
	require.NoError(t, err)
	require.Equal(t, firstAnnotation, secondAnnotation)
	require.Equal(t, firstDigest, secondDigest)
}

func TestConfidentialInitDataPreservesLeadingNewline(t *testing.T) {
	settings := *testCCInitDataSettings(t)
	settings.AgentPolicy = "\npackage agent_policy\n\ndefault allow = true\n"

	raw, err := buildConfidentialInitData(settings, "sha256", "proof", nil, "")
	require.NoError(t, err)
	var document map[string]any
	require.NoError(t, toml.Unmarshal(raw, &document))
	data := document["data"].(map[string]any)
	require.Equal(t, settings.AgentPolicy, data[ccInitDataPolicyKey])
}

func TestSealedEnvironmentRequiresMeasuredCDHConfiguration(t *testing.T) {
	workload := newCCStorageWorkload(t, RuntimeClassKataQemuSNP)
	workload.group.Services[0].Env = []string{"ORDINARY=value", "CANARY=" + testSealedKeyRef}
	prepareCCStorageWorkload(t, workload)

	_, document := decodeCCInitData(t, workload.ccInitDataAnnotation)
	data := document["data"].(map[string]any)
	require.NotContains(t, data, ccInitDataSecureVolumesKey)
	require.Contains(t, data[ccInitDataCDHKey], "name = \"cc_kbc\"")
}

func TestConfidentialInitDataUsesExplicitKBSSelection(t *testing.T) {
	t.Run("provider managed", func(t *testing.T) {
		workload := newCCStorageWorkload(t, RuntimeClassKataQemuSNP)
		workload.group.Services[0].Env = []string{"CANARY=" + testSealedKeyRef}
		prepareCCStorageWorkload(t, workload)

		raw, _ := decodeCCInitData(t, workload.ccInitDataAnnotation)
		require.Contains(t, string(raw), workload.settings.CCInitData.KBSURL)
	})

	t.Run("tenant managed", func(t *testing.T) {
		workload := newCCStorageWorkload(
			t,
			RuntimeClassKataQemuSNP,
			ccStorageSpec{
				name:       "data",
				mount:      "/proof",
				persistent: true,
				keyRef:     testSealedKeyRef,
			},
		)
		tenant := *testCCInitDataSettings(t)
		tenant.KBSURL = "https://kbs.tenant.example:8443"
		tenant.ImageSecurityPolicyURI = "kbs:///tenant/security-policy/sha256-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
		tenant.AgentPolicy = "package agent_policy\n\ndefault allow = false\n"
		workload.group.Services[0].Params.TEE.KBS = tenantManagedKBSParams(tenant)
		workload.settings.CCInitData = nil
		workload.group.Services[0].Env = []string{"CANARY=" + testSealedKeyRef}
		prepareCCStorageWorkload(t, workload)

		raw, _ := decodeCCInitData(t, workload.ccInitDataAnnotation)
		require.Len(t, workload.secureVolumes, 1)
		require.Contains(t, string(raw), tenant.KBSURL)
		require.Contains(t, string(raw), tenant.ImageSecurityPolicyURI)
		require.Contains(t, string(raw), "default allow = false")
	})

	t.Run("missing selection", func(t *testing.T) {
		workload := newCCStorageWorkload(t, RuntimeClassKataQemuSNP)
		workload.group.Services[0].Params.TEE.KBS = nil
		workload.group.Services[0].Env = []string{"CANARY=" + testSealedKeyRef}

		_, _, err := workload.confidentialInitDataAnnotation()
		require.ErrorContains(t, err, "explicit provider or tenant KBS selection")
	})

	t.Run("invalid tenant bundle", func(t *testing.T) {
		workload := newCCStorageWorkload(t, RuntimeClassKataQemuSNP)
		tenant := *testCCInitDataSettings(t)
		tenant.KBSCertificate = "not a certificate"
		workload.group.Services[0].Params.TEE.KBS = tenantManagedKBSParams(tenant)
		workload.group.Services[0].Env = []string{"CANARY=" + testSealedKeyRef}

		_, _, err := workload.confidentialInitDataAnnotation()
		require.ErrorContains(t, err, "KBS certificate")
	})
}

func TestConfidentialPersistentStorageRejectsUnsafeContracts(t *testing.T) {
	tests := []struct {
		name         string
		runtimeClass RuntimeClass
		spec         ccStorageSpec
		wantError    string
	}{
		{
			name:         "missing keyRef",
			runtimeClass: RuntimeClassKataQemuSNP,
			spec:         ccStorageSpec{name: "data", mount: "/proof", persistent: true},
			wantError:    "requires a tenant-signed keyRef",
		},
		{
			name:         "plaintext KBS URI",
			runtimeClass: RuntimeClassKataQemuSNP,
			spec:         ccStorageSpec{name: "data", mount: "/proof", persistent: true, keyRef: "kbs:///default/key/plain"},
			wantError:    "invalid sealed keyRef",
		},
		{
			name:         "read only",
			runtimeClass: RuntimeClassKataQemuSNP,
			spec:         ccStorageSpec{name: "data", mount: "/proof", persistent: true, readOnly: true, keyRef: testSealedKeyRef},
			wantError:    "does not support readOnly",
		},
		{
			name:         "unsafe mount",
			runtimeClass: RuntimeClassKataQemuSNP,
			spec:         ccStorageSpec{name: "data", mount: "/proof/../etc", persistent: true, keyRef: testSealedKeyRef},
			wantError:    "unsafe mount path",
		},
		{
			name:         "ordinary runtime",
			runtimeClass: RuntimeClass(""),
			spec:         ccStorageSpec{name: "data", mount: "/proof", persistent: true, keyRef: testSealedKeyRef},
			wantError:    "requires a confidential runtime",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			workload := newCCStorageWorkload(t, test.runtimeClass, test.spec)
			_, err := workload.confidentialPersistentVolumes()
			require.ErrorContains(t, err, test.wantError)
		})
	}
}

func TestConfidentialPersistentStorageRejectsAmbiguousContracts(t *testing.T) {
	workload := newCCStorageWorkload(t, RuntimeClassKataQemuSNP,
		ccStorageSpec{name: "data", mount: "/proof", persistent: true, keyRef: testSealedKeyRef},
	)
	workload.group.Services[0].Resources.Storage = append(
		workload.group.Services[0].Resources.Storage,
		workload.group.Services[0].Resources.Storage[0],
	)
	_, err := workload.confidentialPersistentVolumes()
	require.ErrorContains(t, err, "duplicate storage resource")

	workload = newCCStorageWorkload(t, RuntimeClassKataQemuSNP,
		ccStorageSpec{name: "data", mount: "/proof", persistent: true, keyRef: testSealedKeyRef},
		ccStorageSpec{name: "cache", mount: "/proof", persistent: true, keyRef: testSealedKeyRef},
	)
	_, err = workload.confidentialPersistentVolumes()
	require.ErrorContains(t, err, "duplicates mount path")
}

func TestSealedEnvironmentContractValidation(t *testing.T) {
	tests := []struct {
		name      string
		env       []string
		wantFound bool
		wantError string
	}{
		{name: "ordinary", env: []string{"NORMAL=value"}},
		{name: "sealed", env: []string{"SECRET=" + testSealedKeyRef}, wantFound: true},
		{name: "malformed", env: []string{"SECRET=sealed.incomplete"}, wantError: "malformed sealed secret"},
		{
			name:      "oversized",
			env:       []string{"SECRET=sealed." + strings.Repeat("a", ccSealedKeyRefMaxBytes) + ".payload.signature"},
			wantError: "malformed sealed secret",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			found, err := serviceHasSealedEnvironment(test.env)
			if test.wantError != "" {
				require.ErrorContains(t, err, test.wantError)
				return
			}
			require.NoError(t, err)
			require.Equal(t, test.wantFound, found)
		})
	}

	workload := newCCStorageWorkload(t, RuntimeClassKataQemuSNP)
	workload.group.Services[0].Env = []string{"SECRET=" + testSealedKeyRef}
	workload.settings.CCInitData = nil
	_, _, err := workload.confidentialInitDataAnnotation()
	require.ErrorContains(t, err, "provider-managed KBS selection requires provider confidential-compute initdata settings")

	workload = newCCStorageWorkload(t, RuntimeClass(""))
	workload.group.Services[0].Env = []string{"SECRET=" + testSealedKeyRef}
	_, _, err = workload.confidentialInitDataAnnotation()
	require.ErrorContains(t, err, "requires a confidential runtime")
}

func TestCCInitDataSettingsRejectsNonCertificatePEMContent(t *testing.T) {
	settings := *testCCInitDataSettings(t)
	settings.KBSCertificate = "unexpected\n" + settings.KBSCertificate
	require.ErrorContains(t, validateCCInitDataSettings(settings), "only PEM certificates")

	settings = *testCCInitDataSettings(t)
	settings.KBSCertificate += "unexpected\n"
	require.ErrorContains(t, validateCCInitDataSettings(settings), "only PEM certificates")

	settings = *testCCInitDataSettings(t)
	settings.KBSCertificate = strings.ReplaceAll(settings.KBSCertificate, "\n", "\r\n")
	require.ErrorContains(t, validateCCInitDataSettings(settings), "unsupported control characters")

	settings = *testCCInitDataSettings(t)
	settings.AgentPolicy += "\r"
	require.ErrorContains(t, validateCCInitDataSettings(settings), "represented safely")
}

func TestCCInitDataSettingsRejectsNonCanonicalPolicyURI(t *testing.T) {
	settings := *testCCInitDataSettings(t)
	settings.ImageSecurityPolicyURI = "kbs:///../security-policy/sha256-" + strings.Repeat("a", 64)
	require.ErrorContains(t, validateCCInitDataSettings(settings), "canonical")
}

func TestOrdinaryPersistentStorageRemainsFilesystemBacked(t *testing.T) {
	workload := newCCStorageWorkload(t, RuntimeClass(""),
		ccStorageSpec{name: "data", mount: "/proof", persistent: true},
	)
	prepareCCStorageWorkload(t, workload)
	pvcs := workload.persistentVolumeClaims()
	require.Len(t, pvcs, 1)
	require.Equal(t, corev1.PersistentVolumeFilesystem, *pvcs[0].Spec.VolumeMode)
	require.Len(t, workload.container().VolumeMounts, 1)
	require.Empty(t, workload.container().VolumeDevices)
	require.Nil(t, workload.podAnnotations())
}
