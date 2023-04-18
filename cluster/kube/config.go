package kube

import (
	"fmt"
	kubeclienterrors "github.com/akash-network/provider/cluster/kube/errors"
	"github.com/pkg/errors"
	"os"
)

const (
	issuer                     = "issuer"
	clusterIssuer              = "cluster-issuer"
	akashProviderIssuerTypeStr = "AKASH_PROVIDER_ISSUER_TYPE"
	akashProviderIssuerNameStr = "AKASH_PROVIDER_ISSUER_NAME"
	akashProviderSslEnabledStr = "AKASH_PROVIDER_SSL_ENABLED"
)

type clientConfig struct {
	ssl ssl
}

type ssl struct {
	issuerType string
	issuerName string
}

// configFromEnv creates a new clientConfig from environment variables.
func configFromEnv() (*clientConfig, error) {
	sslEnabled := os.Getenv(akashProviderSslEnabledStr)
	var sslCfg ssl

	if sslEnabled != "" && sslEnabled != "0" {
		issuerType, ok := os.LookupEnv(akashProviderIssuerTypeStr)
		if !ok || (issuerType != issuer && issuerType != clusterIssuer) {
			return nil, errors.Wrap(kubeclienterrors.ErrInternalError, fmt.Sprintf("Invalid value for %s: %s", akashProviderIssuerTypeStr, issuerType))
		}

		issuerName, ok := os.LookupEnv(akashProviderIssuerNameStr)
		if !ok {
			return nil, errors.Wrap(kubeclienterrors.ErrInternalError, fmt.Sprintf("Value for %s not set", akashProviderIssuerNameStr))
		}

		sslCfg = ssl{
			issuerType: issuerType,
			issuerName: issuerName,
		}
	}

	return &clientConfig{
		ssl: sslCfg,
	}, nil
}
