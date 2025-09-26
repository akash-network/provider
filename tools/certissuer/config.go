package certissuer

import (
	"fmt"
	"math"
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
	HTTPChallengePort        int
	TLSChallengePort         int
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

	if c.HTTPChallengePort != 0 && (c.HTTPChallengePort < 1 || c.HTTPChallengePort > int(math.MaxUint16)) {
		return fmt.Errorf("%w: invalid port value %d for http-01 challenge. allowed: 1..65535 or 0 to disable", ErrConfig, c.HTTPChallengePort)
	}

	if c.TLSChallengePort != 0 && (c.TLSChallengePort < 1 || c.TLSChallengePort > int(math.MaxUint16)) {
		return fmt.Errorf("%w: invalid port value \"%d\" for tls-alpn-01 challenge. allowed range 1..65535", ErrConfig, c.HTTPChallengePort)
	}

	return nil
}
