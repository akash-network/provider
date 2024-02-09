package util

import (
	"os"
)

const (
	kubeSecretPath = "/var/run/secrets/kubernetes.io" // nolint:gosec
)

var insideKubernetes bool

func init() {
	_, err := os.Stat(kubeSecretPath)

	insideKubernetes = err == nil
}

func IsInsideKubernetes() bool {
	return insideKubernetes
}
