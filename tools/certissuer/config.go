package certissuer

import (
	"fmt"
	"time"
)

type Config struct {
	Email                    string
	KID                      string
	HMAC                     string
	StorageDir               string
	CADirURL                 string
	DNSProviders             []string
	DNSResolvers             []string
	DNSTimeout               time.Duration
	DNSPropagationWait       time.Duration
	DNSPropagationRNS        bool
	DNSDisableCP             bool
	DNSPropagationDisableANS bool
}

func (c Config) Validate() error {
	if c.Email == "" {
		return fmt.Errorf("%w: invalid email", ErrInvalidConfig)
	}

	for _, provider := range c.DNSProviders {
		switch provider {
		case "gcloud":
		case "cf":
		default:
			return fmt.Errorf("%w: invalid dns provider %s", ErrInvalidConfig, provider)
		}
	}

	if c.StorageDir == "" {
		return fmt.Errorf("%w: invalid path to storage dir", ErrInvalidConfig)
	}

	if c.CADirURL == "" {
		return fmt.Errorf("%w: invalid ca dir url", ErrInvalidConfig)
	}

	return nil
}
