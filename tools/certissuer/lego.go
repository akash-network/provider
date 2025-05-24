package certissuer

import (
	"context"
	"fmt"

	"github.com/go-acme/lego/v4/challenge"
	"github.com/go-acme/lego/v4/challenge/dns01"
	"github.com/go-acme/lego/v4/lego"
	"github.com/go-acme/lego/v4/providers/dns/cloudflare"
	"github.com/go-acme/lego/v4/providers/dns/gcloud"
	"github.com/go-acme/lego/v4/registration"
	"github.com/hashicorp/go-retryablehttp"
	"github.com/tendermint/tendermint/libs/log"
	"golang.org/x/sync/errgroup"
)

type CertIssuer interface {
	Close() error
}

type certIssuer struct {
	ctx     context.Context
	cancel  context.CancelFunc
	group   *errgroup.Group
	storage Storage
	cl      *lego.Client
}

var _ CertIssuer = (*certIssuer)(nil)

// NewLego initializes and returns a certificate issuer based on the ACME protocol
// using the Lego library. It manages certificate operations through DNS validation
// challenges.
//
// Parameters:
//   - ctx: Parent context for lifecycle management
//   - log: Logger for operation and error reporting
//   - cfg: Configuration containing ACME server, account details, and DNS providers
//
// The function performs several key operations:
//   - Sets up local storage for account persistence
//   - Configures and creates a Lego client with retry capability
//   - Handles ACME account registration (with External Account Binding if required)
//   - Initializes configured DNS providers (supports gcloud and Cloudflare)
//   - Starts a background process for certificate management
//
// Returns a CertIssuer interface and any error encountered during setup.
func NewLego(ctx context.Context, log log.Logger, cfg Config) (CertIssuer, error) {
	log = log.With("module", "cert-issuer")

	storage, err := NewStorage(log, StorageConfig{
		CADirURL: cfg.CADirURL,
		UserID:   cfg.Email,
		RootPath: cfg.StorageDir,
	})
	if err != nil {
		return nil, err
	}

	account, keyType := storage.AccountSetup()

	lcfg := lego.NewConfig(account)
	lcfg.CADirURL = cfg.CADirURL
	lcfg.Certificate.KeyType = keyType

	retryClient := retryablehttp.NewClient()
	retryClient.RetryMax = 5
	retryClient.HTTPClient = lcfg.HTTPClient
	retryClient.Logger = nil
	lcfg.HTTPClient = retryClient.StandardClient()

	providers, err := initProviders(cfg.DNSProviders)
	if err != nil {
		return nil, err
	}

	client, err := lego.NewClient(lcfg)
	if err != nil {
		return nil, err
	}

	if account.Registration == nil {
		if client.GetExternalAccountRequired() {
			if cfg.KID == "" || cfg.HMAC == "" {
				log.Error(fmt.Sprintf("server requires External Account Binding. Config options KID and HMAC must be set"))
				return nil, err
			}

			account.Registration, err = client.Registration.RegisterWithExternalAccountBinding(registration.RegisterEABOptions{
				TermsOfServiceAgreed: true,
				Kid:                  cfg.KID,
				HmacEncoded:          cfg.HMAC,
			})
			if err != nil {
				return nil, err
			}
		} else {
			account.Registration, err = client.Registration.Register(registration.RegisterOptions{TermsOfServiceAgreed: true})
			if err != nil {
				return nil, err
			}
		}

		if err = storage.AccountSave(account); err != nil {
			return nil, err
		}
	}

	ctx, cancel := context.WithCancel(ctx)
	group, ctx := errgroup.WithContext(ctx)

	svc := &certIssuer{
		ctx:     ctx,
		cancel:  cancel,
		group:   group,
		storage: storage,
		cl:      client,
	}

	for _, provider := range providers {
		opts := []dns01.ChallengeOption{
			dns01.CondOption(len(cfg.DNSResolvers) > 0,
				dns01.AddRecursiveNameservers(dns01.ParseNameservers(cfg.DNSResolvers))),
			dns01.CondOption(cfg.DNSDisableCP || cfg.DNSPropagationDisableANS,
				dns01.DisableAuthoritativeNssPropagationRequirement()),
			dns01.CondOption(cfg.DNSPropagationWait > 0,
				// TODO(troian): inside the next major lego version DNSDisableCP will be used here.
				// This will change the meaning of this flag to really disable all propagation checks.
				dns01.PropagationWait(cfg.DNSPropagationWait, true)),
			dns01.CondOption(cfg.DNSPropagationRNS,
				dns01.RecursiveNSsPropagationRequirement()),
			dns01.CondOption(cfg.DNSTimeout > 0,
				dns01.AddDNSTimeout(cfg.DNSTimeout)),
		}

		if err = client.Challenge.SetDNS01Provider(provider, opts...); err != nil {
			return nil, err
		}
	}

	group.Go(svc.run)

	return svc, nil
}

func (cl *certIssuer) Close() error {
	select {
	case <-cl.ctx.Done():
		return nil
	default:
	}

	cl.cancel()

	err := cl.group.Wait()
	if err != nil {
		return err
	}

	return nil
}

func (cl *certIssuer) run() error {

	// resp, err := cl.cl.Certificate.GetRenewalInfo(certificate.RenewalInfoRequest{})
	//
	// resp.ShouldRenewAt()
	for {
		select {
		case <-cl.ctx.Done():
			return cl.ctx.Err()
		}
	}
}

func initProviders(providers []string) ([]challenge.Provider, error) {
	res := make([]challenge.Provider, 0, len(providers))

	for _, provider := range providers {
		switch provider {
		case "gcloud":
			p, err := gcloud.NewDNSProvider()
			if err != nil {
				return nil, err
			}

			res = append(res, p)
		case "cf":
			p, err := cloudflare.NewDNSProvider()
			if err != nil {
				return nil, err
			}

			res = append(res, p)
		default:
			return res, fmt.Errorf("lego: unsupported dns provider %s", provider)
		}
	}

	return res, nil
}
