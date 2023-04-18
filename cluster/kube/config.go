package kube

import (
	"errors"
	"fmt"
	"os"
)

const (
	issuer                     = "issuer"
	clusterIssuer              = "cluster-issuer"
	akashProviderIssuerTypeStr = "AKASH_PROVIDER_ISSUER_TYPE"
	akashProviderIssuerNameStr = "AKASH_PROVIDER_ISSUER_NAME"
)

type clientConfig struct {
	issuerType string
	issuerName string
}

// configFromEnv creates a new clientConfig from environment variables.
func configFromEnv() (*clientConfig, error) {
	issuerType, ok := os.LookupEnv(akashProviderIssuerTypeStr)
	if !ok || (issuerType != issuer && issuerType != clusterIssuer) {
		return nil, errors.New(fmt.Sprintf("Invalid value for %s: %s", akashProviderIssuerTypeStr, issuerType))
	}

	issuerName, ok := os.LookupEnv(akashProviderIssuerNameStr)
	if !ok {
		return nil, errors.New(fmt.Sprintf("Value for %s not set", akashProviderIssuerNameStr))
	}

	return &clientConfig{
		issuerType: issuerType,
		issuerName: issuerName,
	}, nil
}
