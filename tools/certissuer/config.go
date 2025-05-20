package certissuer

import (
	"fmt"
	"time"

	tpubsub "github.com/troian/pubsub"

	sdk "github.com/cosmos/cosmos-sdk/types"
)

type Config struct {
	Bus                      tpubsub.Publisher
	Email                    string
	Owner                    sdk.Address
	KID                      string
	HMAC                     string
	StorageDir               string
	CADirURL                 string
	Domains                  []string
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
		return fmt.Errorf("%w: invalid email", ErrConfig)
	}

	if c.Owner.Empty() {
		return fmt.Errorf("%w: invalid owner address", ErrConfig)
	}

	for _, provider := range c.DNSProviders {
		switch provider {
		case "gcloud":
		case "cf":
		default:
			return fmt.Errorf("%w: invalid dns provider %s", ErrConfig, provider)
		}
	}

	if c.StorageDir == "" {
		return fmt.Errorf("%w: invalid path to storage dir", ErrConfig)
	}

	if c.CADirURL == "" {
		return fmt.Errorf("%w: invalid ca dir url", ErrConfig)
	}

	return nil
}
