package builder

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	mani "pkg.akt.dev/go/manifest/v2beta3"
	"pkg.akt.dev/go/testutil"

	crd "github.com/akash-network/provider/pkg/apis/akash.network/v2beta2"
)

func TestConfidentialRegistryCredentialsAreKBSReferenced(t *testing.T) {
	const resourceURI = "kbs:///lease-scope/registry/auth"
	fixture := newCCStorageWorkload(t, RuntimeClassKataQemuSNP)
	fixture.settings.DockerImagePullSecretsName = "provider-wide-pull-secret"
	fixture.group.Services[0].Credentials = &mani.ImageCredentials{URI: resourceURI}
	manifest, err := crd.NewManifest(
		"lease",
		fixture.deployment.LeaseID(),
		&fixture.group,
		crd.ClusterSettings{SchedulerParams: fixture.sparams},
	)
	require.NoError(t, err)

	workload, err := NewWorkloadBuilder(
		testutil.Logger(t),
		fixture.settings,
		fixture.deployment,
		manifest,
		0,
	)
	require.NoError(t, err)

	raw, document := decodeCCInitData(t, workload.ccInitDataAnnotation)
	data := document["data"].(map[string]any)
	require.Contains(t, data[ccInitDataCDHKey], "authenticated_registry_credentials_uri = \""+resourceURI+"\"")
	require.NotContains(t, string(raw), "username")
	require.NotContains(t, string(raw), "password")
	require.Empty(t, workload.imagePullSecrets())
	require.Empty(t, workload.secretsRefs)
	require.Nil(t, workload.ImagePullCredentials())
}

func TestRegistryCredentialModeMatchesRuntime(t *testing.T) {
	workload := newCCStorageWorkload(t, RuntimeClassKataQemuSNP)
	workload.group.Services[0].Credentials = &mani.ImageCredentials{
		Host: "registry.example", Username: "tenant", Password: "secret-value",
	}
	_, err := workload.confidentialRegistryCredentialsURI()
	require.ErrorContains(t, err, "requires a KBS resource URI")

	workload = newCCStorageWorkload(t, RuntimeClass(""))
	workload.group.Services[0].Credentials = &mani.ImageCredentials{URI: "kbs:///lease-scope/registry/auth"}
	_, err = workload.confidentialRegistryCredentialsURI()
	require.ErrorContains(t, err, "requires a confidential runtime")

	workload = newCCStorageWorkload(t, RuntimeClassKataQemuSNP)
	workload.group.Services[0].Credentials = &mani.ImageCredentials{
		Host: "registry.example", URI: "kbs:///lease-scope/registry/auth",
	}
	_, err = workload.confidentialRegistryCredentialsURI()
	require.ErrorContains(t, err, "cannot mix inline fields")
}

func TestRegistryCredentialURIValidation(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		wantErr bool
	}{
		{name: "canonical", value: "kbs:///lease-scope/registry/auth"},
		{name: "host", value: "kbs://server/lease-scope/registry/auth", wantErr: true},
		{name: "query", value: "kbs:///lease-scope/registry/auth?version=1", wantErr: true},
		{name: "leading dot", value: "kbs:///.lease-scope/registry/auth", wantErr: true},
		{name: "too many segments", value: "kbs:///lease-scope/registry/auth/extra", wantErr: true},
		{name: "oversized", value: "kbs:///" + strings.Repeat("a", ccKBSResourceURIMaxBytes) + "/registry/auth", wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateCCKBSResourceURI(test.value)
			if test.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestOrdinaryRegistryCredentialsKeepExistingKubernetesSecretFlow(t *testing.T) {
	credentials := &mani.ImageCredentials{
		Host: "registry.example", Username: "tenant", Password: "secret-value",
	}
	workload := newCCStorageWorkload(t, RuntimeClass(""))
	workload.group.Services[0].Credentials = credentials

	uri, err := workload.confidentialRegistryCredentialsURI()
	require.NoError(t, err)
	require.Empty(t, uri)
	require.Same(t, credentials, workload.ImagePullCredentials())
}
