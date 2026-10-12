package cmd

import (
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"

	"github.com/akash-network/provider/tools/logging"
)

func TestAttestationSidecarEnvPropagatesLogging(t *testing.T) {
	cfg, err := logging.ParseConfig("tee:debug,*:info", "json", true, "rfc3339nano", true)
	require.NoError(t, err)

	require.Equal(t, []corev1.EnvVar{
		{Name: "ATTESTATION_LOG_LEVEL", Value: "tee:debug,*:info"},
		{Name: "ATTESTATION_LOG_FORMAT", Value: "json"},
		{Name: "ATTESTATION_LOG_COLOR", Value: "false"},
		{Name: "ATTESTATION_LOG_TIMESTAMP", Value: "rfc3339nano"},
		{Name: "ATTESTATION_TRACE", Value: "true"},
	}, attestationSidecarEnv(cfg, false))
}

func TestAttestationSidecarEnvMock(t *testing.T) {
	env := attestationSidecarEnv(logging.DefaultConfig(), true)
	require.Len(t, env, 6)
	require.Equal(t, corev1.EnvVar{Name: "ATTESTATION_MOCK", Value: "true"}, env[5])
}
