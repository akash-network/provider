package cmd

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func writeTestKBSConfigFiles(t *testing.T) (string, string) {
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

	directory := t.TempDir()
	certificatePath := filepath.Join(directory, "kbs.pem")
	policyPath := filepath.Join(directory, "policy.rego")
	require.NoError(t, os.WriteFile(certificatePath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600))
	require.NoError(t, os.WriteFile(policyPath, []byte("package agent_policy\n\ndefault allow = true\n"), 0o600))
	return certificatePath, policyPath
}

func TestLoadCCInitDataSettings(t *testing.T) {
	settings, err := loadCCInitDataSettings("", "", "", "")
	require.NoError(t, err)
	require.Nil(t, settings)

	_, err = loadCCInitDataSettings("https://192.0.2.10:8080", "", "", "")
	require.ErrorContains(t, err, "must be configured together")

	certificatePath, policyPath := writeTestKBSConfigFiles(t)
	settings, err = loadCCInitDataSettings(
		"https://192.0.2.10:8080",
		certificatePath,
		"kbs:///default/security-policy/sha256-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		policyPath,
	)
	require.NoError(t, err)
	require.Equal(t, "https://192.0.2.10:8080", settings.KBSURL)
	require.Contains(t, settings.KBSCertificate, "BEGIN CERTIFICATE")
	require.Contains(t, settings.AgentPolicy, "package agent_policy")
}
