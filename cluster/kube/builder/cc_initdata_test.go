package builder

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"io"
	"testing"

	"github.com/BurntSushi/toml"
	"github.com/stretchr/testify/require"

	mani "pkg.akt.dev/go/manifest/v2beta3"
	"pkg.akt.dev/go/testutil"

	crd "github.com/akash-network/provider/pkg/apis/akash.network/v2beta2"
)

// decodeCCInitData reverses ccImageRegistryAuthAnnotation
// (base64 -> gunzip -> TOML) so tests can assert on the delivered document,
// mirroring exactly what the kata-agent does with the annotation value.
func decodeCCInitData(t *testing.T, annotation string) ccInitData {
	t.Helper()

	gzBytes, err := base64.StdEncoding.DecodeString(annotation)
	require.NoError(t, err)

	gr, err := gzip.NewReader(bytes.NewReader(gzBytes))
	require.NoError(t, err)
	defer func() { _ = gr.Close() }()

	raw, err := io.ReadAll(gr)
	require.NoError(t, err)

	var doc ccInitData
	require.NoError(t, toml.Unmarshal(raw, &doc))

	return doc
}

func TestContainersAuthJSON(t *testing.T) {
	// Leading/trailing whitespace must be trimmed, matching the host-side secret.
	creds := &mani.ImageCredentials{
		Host:     "ghcr.io",
		Username: "  user  ",
		Password: "  pass  ",
		Email:    "e@x.io",
	}

	raw, err := containersAuthJSON(creds)
	require.NoError(t, err)

	var dc dockerCredentials
	require.NoError(t, json.Unmarshal(raw, &dc))

	entry, ok := dc.Auths["ghcr.io"]
	require.True(t, ok, "auth entry must be keyed by registry host")
	require.Equal(t, "user", entry.Username)
	require.Equal(t, "pass", entry.Password)
	require.Equal(t, "e@x.io", entry.Email)
	require.Equal(t, base64.StdEncoding.EncodeToString([]byte("user:pass")), entry.Auth)
}

func TestCCImageRegistryAuthAnnotation_NilCreds(t *testing.T) {
	v, err := ccImageRegistryAuthAnnotation(nil)
	require.NoError(t, err)
	require.Empty(t, v, "nil credentials must produce no annotation value")
}

func TestCCImageRegistryAuthAnnotation_RoundTrip(t *testing.T) {
	creds := &mani.ImageCredentials{Host: "ghcr.io", Username: "user", Password: "pass"}

	v, err := ccImageRegistryAuthAnnotation(creds)
	require.NoError(t, err)
	require.NotEmpty(t, v)

	doc := decodeCCInitData(t, v)
	require.Equal(t, ccInitDataAlgorithm, doc.Algorithm)
	require.Equal(t, ccInitDataVersion, doc.Version)

	// cdh.toml must point image-rs at the local auth file and NOT depend on a KBS.
	cdh, ok := doc.Data[ccInitDataCDHKey]
	require.True(t, ok, "cdh.toml entry must be present")
	require.Contains(t, cdh, "authenticated_registry_credentials_uri")
	require.Contains(t, cdh, "file://"+ccGuestAuthFilePath)
	require.NotContains(t, cdh, "kbs://", "credential delivery must not require attestation")

	// auth.json must carry the credentials in dockerconfigjson form.
	authJSON, ok := doc.Data[ccInitDataAuthKey]
	require.True(t, ok, "auth.json entry must be present")

	var dc dockerCredentials
	require.NoError(t, json.Unmarshal([]byte(authJSON), &dc))
	entry, ok := dc.Auths["ghcr.io"]
	require.True(t, ok)
	require.Equal(t, base64.StdEncoding.EncodeToString([]byte("user:pass")), entry.Auth)
}

// The annotation the provider emits must be byte-identical to what the guest
// consumes; assert the exact wire format (base64 of gzip of TOML) round-trips.
func TestCCImageRegistryAuthAnnotation_WireFormat(t *testing.T) {
	creds := &mani.ImageCredentials{Host: "registry.example.com", Username: "u", Password: "p"}

	v, err := ccImageRegistryAuthAnnotation(creds)
	require.NoError(t, err)

	// Value must be valid standard base64.
	gzBytes, err := base64.StdEncoding.DecodeString(v)
	require.NoError(t, err)

	// ...of a gzip stream...
	gr, err := gzip.NewReader(bytes.NewReader(gzBytes))
	require.NoError(t, err)
	raw, err := io.ReadAll(gr)
	require.NoError(t, err)
	require.NoError(t, gr.Close())

	// ...of a valid initdata TOML document.
	var doc ccInitData
	require.NoError(t, toml.Unmarshal(raw, &doc))
	require.Len(t, doc.Data, 2)
}

func newTestWorkload(t *testing.T, rc RuntimeClass, attestationDisabled bool, creds *mani.ImageCredentials) *Workload {
	t.Helper()
	return &Workload{
		builder: builder{
			log: testutil.Logger(t),
			group: mani.Group{
				Services: mani.Services{{Name: "web", Credentials: creds}},
			},
			sparams: []*crd.SchedulerParams{{
				RuntimeClass:        rc,
				AttestationDisabled: attestationDisabled,
			}},
		},
		serviceIdx: 0,
	}
}

func TestPodAnnotations_CCImageRegistryAuth(t *testing.T) {
	creds := &mani.ImageCredentials{Host: "ghcr.io", Username: "user", Password: "pass"}

	tests := []struct {
		name                string
		runtimeClass        RuntimeClass
		attestationDisabled bool
		creds               *mani.ImageCredentials
		wantCCInitData      bool
		wantAttestation     bool
	}{
		{"CC GPU-SNP with creds", RuntimeClassKataQemuNvidiaGPUSNP, false, creds, true, false},
		{"CC SNP with creds", RuntimeClassKataQemuSNP, false, creds, true, false},
		{"CC TDX with creds", RuntimeClassKataQemuTDX, false, creds, true, false},
		{"CC GPU-TDX with creds", RuntimeClassKataQemuNvidiaGPUTDX, false, creds, true, false},
		{"non-CC empty runtime with creds", RuntimeClass(""), false, creds, false, false},
		{"non-CC nvidia with creds", RuntimeClass("nvidia"), false, creds, false, false},
		{"CC SNP without creds", RuntimeClassKataQemuSNP, false, nil, false, false},
		{"attestation-disabled + CC + creds", RuntimeClassKataQemuSNP, true, creds, true, true},
		{"attestation-disabled only", RuntimeClass(""), true, nil, false, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wl := newTestWorkload(t, tt.runtimeClass, tt.attestationDisabled, tt.creds)

			ann := wl.podAnnotations()

			if v, ok := ann[ccInitDataAnnotation]; tt.wantCCInitData {
				require.True(t, ok, "expected cc_init_data annotation")
				doc := decodeCCInitData(t, v)
				require.Contains(t, doc.Data[ccInitDataAuthKey], "ghcr.io",
					"delivered auth.json must contain the tenant registry host")
			} else {
				require.False(t, ok, "did not expect cc_init_data annotation")
			}

			if v, ok := ann[AkashAttestationDisabledAnnotation]; tt.wantAttestation {
				require.True(t, ok)
				require.Equal(t, ValTrue, v)
			} else {
				require.False(t, ok, "did not expect attestation-disabled annotation")
			}
		})
	}
}

// A nil per-service SchedulerParams entry must not panic and must not emit any
// annotations (guards the nil checks in podAnnotations).
func TestPodAnnotations_NilSchedulerParams(t *testing.T) {
	wl := &Workload{
		builder: builder{
			log:     testutil.Logger(t),
			group:   mani.Group{Services: mani.Services{{Name: "web"}}},
			sparams: []*crd.SchedulerParams{nil},
		},
		serviceIdx: 0,
	}

	require.Nil(t, wl.podAnnotations())
}
