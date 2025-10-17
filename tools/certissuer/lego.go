package certissuer

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"time"

	"github.com/go-acme/lego/v4/certificate"
	"github.com/go-acme/lego/v4/challenge"
	"github.com/go-acme/lego/v4/challenge/dns01"
	"github.com/go-acme/lego/v4/challenge/http01"
	"github.com/go-acme/lego/v4/challenge/tlsalpn01"
	"github.com/go-acme/lego/v4/lego"
	"github.com/go-acme/lego/v4/providers/dns/cloudflare"
	"github.com/go-acme/lego/v4/providers/dns/gcloud"
	"github.com/go-acme/lego/v4/registration"
	"github.com/hashicorp/go-retryablehttp"
	tpubsub "github.com/troian/pubsub"
	"golang.org/x/sync/errgroup"

	"cosmossdk.io/log"
)

var (
	ErrExternalAccountBindingRequired = errors.New("server requires External Account Binding")
	ErrUnsupportedDNSProvider         = errors.New("unsupported DNS provider")
)

type CertIssuer interface {
	Close() error
}

type certIssuer struct {
	ctx     context.Context
	cancel  context.CancelFunc
	log     log.Logger
	pub     tpubsub.Publisher
	group   *errgroup.Group
	storage Storage
	cl      *lego.Client
	domains []string
}

type resourceInfo struct {
	*certificate.Resource
	cert *x509.Certificate
}

type resourceInfos []resourceInfo

func (sp resourceInfos) Len() int {
	return len(sp)
}

func (sp resourceInfos) Less(i, j int) bool {
	return sp[i].cert.NotAfter.Before(sp[j].cert.NotAfter)
}

func (sp resourceInfos) Swap(i, j int) {
	sp[i], sp[j] = sp[j], sp[i]
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

	account, keyType, err := storage.AccountSetup()
	if err != nil {
		return nil, err
	}

	lcfg := lego.NewConfig(account)
	lcfg.CADirURL = cfg.CADirURL
	lcfg.Certificate.KeyType = keyType

	retryClient := retryablehttp.NewClient()
	retryClient.RetryMax = 5
	retryClient.HTTPClient = lcfg.HTTPClient
	retryClient.Logger = nil
	lcfg.HTTPClient = retryClient.StandardClient()

	client, err := lego.NewClient(lcfg)
	if err != nil {
		return nil, err
	}

	dnsProviders, err := initDNSProviders(cfg.DNSProviders)
	if err != nil {
		return nil, err
	}

	if account.Registration == nil {
		if client.GetExternalAccountRequired() {
			if cfg.KID == "" || cfg.HMAC == "" {
				return nil, fmt.Errorf("%w: KID and HMAC must be set", ErrExternalAccountBindingRequired)
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
		log:     log,
		storage: storage,
		cl:      client,
		domains: cfg.Domains,
		pub:     cfg.Bus,
	}

	if cfg.HTTPChallengePort > 0 {
		log.Info(fmt.Sprintf("starting HTTP-01 listener on port %d", cfg.HTTPChallengePort))
		srv := http01.NewProviderServer("", strconv.Itoa(cfg.HTTPChallengePort))
		if err = client.Challenge.SetHTTP01Provider(srv); err != nil {
			return nil, err
		}
	}

	if cfg.TLSChallengePort > 0 {
		log.Info(fmt.Sprintf("starting TLS-ALPN-01 listener on port %d", cfg.TLSChallengePort))
		srv := tlsalpn01.NewProviderServer("", strconv.Itoa(cfg.TLSChallengePort))
		if err = client.Challenge.SetTLSALPN01Provider(srv); err != nil {
			return nil, err
		}
	}

	for _, provider := range dnsProviders {
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

	group.Go(func() error {
		return svc.run()
	})

	return svc, nil
}

func (ci *certIssuer) Close() error {
	select {
	case <-ci.ctx.Done():
		return nil
	default:
	}

	ci.cancel()

	err := ci.group.Wait()
	if err != nil {
		return err
	}

	return nil
}

func (ci *certIssuer) obtainCert(domain string) (*certificate.Resource, ResourcesInfo, error) {
	req := certificate.ObtainRequest{
		Domains:                        []string{domain},
		PrivateKey:                     nil,
		MustStaple:                     false,
		EmailAddresses:                 nil, // do not set email addresses or LetsEncrypt will reject the request
		Bundle:                         true,
		PreferredChain:                 "",
		AlwaysDeactivateAuthorizations: false,
	}
	res, err := ci.cl.Certificate.Obtain(req)
	if err != nil {
		return nil, ResourcesInfo{}, err
	}

	info, err := ci.storage.SaveResource(res)
	if err != nil {
		return nil, ResourcesInfo{}, fmt.Errorf("failed to save resource for %s: %w", domain, err)
	}

	return res, info, nil
}

func (ci *certIssuer) run() error {
	resources := make(resourceInfos, 0, len(ci.domains))

	var err error
	defer func() {
		if err != nil {
			ci.log.Error("finished with error", "err", err.Error())
		}
	}()

	for _, domain := range ci.domains {
		res, info, err := ci.storage.ReadResource(domain)
		if err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				ci.log.Error(fmt.Sprintf("failed to read resource for %s", domain), "err", err)
				continue
			}

			// if this is a first time certificate is being requested, it may take a little time
			// for ACME to propagate and deploy it. we give it a few attempts to retry the download

			for retryCnt := 0; retryCnt < 5; retryCnt++ {
				res, info, err = ci.obtainCert(domain)
				if err == nil {
					break
				}
				if err != nil {
					ci.log.Error(fmt.Sprintf("failed to obtain certificate for %s", domain), "err", err.Error())

					select {
					case <-ci.ctx.Done():
						return ci.ctx.Err()
					case <-time.After(10 * time.Second):
					}
				}
			}
		}

		if err == nil {
			block, _ := pem.Decode(res.Certificate)
			if block == nil {
				ci.log.Error(fmt.Sprintf("invalid PEM certificate data for %s", domain))
				continue
			}

			cert, err := x509.ParseCertificate(block.Bytes)
			if err != nil {
				ci.log.Error(fmt.Sprintf("failed to parse certificate %s", domain), "err", err)
				continue
			}
			ci.pub.Pub(info, []string{fmt.Sprintf("domain-cert-%s", info.Domain)}, tpubsub.WithRetain())

			resources = append(resources, resourceInfo{
				Resource: res,
				cert:     cert,
			})
		} else {
			ci.log.Error(fmt.Sprintf("unable to obtain certificate for %s after 5 retries. giving up. provider will not check this certificate until next restart", domain), "err", err.Error())
		}
	}

	sort.Sort(sort.Reverse(resources))

	renewTrigger := make(chan struct{}, 1)

	timer := time.NewTimer(1 * time.Second)
	timer.Stop()

	renewCheck := 24 * time.Hour

	renewTrigger <- struct{}{}

	for {
		select {
		case <-ci.ctx.Done():
			return ci.ctx.Err()
		case <-timer.C:
			select {
			case renewTrigger <- struct{}{}:
			default:
			}
		case <-renewTrigger:
			checkAt := time.Now().Add(renewCheck)

			// set up the initial check far enough so all certificates should expire before this time.
			// with ACME generally certs are valid for 90d, so setting the initial value to 365d looks "safe".
			// might need to find a better starting point tho
			earliestRenewAt := time.Now().Add(24 * time.Hour * 365)

			ci.log.Debug("starting check if any of certificates need a renew")
			for i, res := range resources {
				// check when a certificate is expected to expire and set renew check 1d prior
				if checkAt.After(res.cert.NotAfter) {
					ci.log.Debug(fmt.Sprintf("certificate for \"%s\" needs renew", res.Domain))

					resp, info, err := ci.obtainCert(res.Domain)
					if err != nil {
						ci.log.Error(fmt.Sprintf("failed to renew certificate for %s: %s", res.Domain, err))
						continue
					}

					block, _ := pem.Decode(res.Certificate)
					if block == nil {
						ci.log.Error(fmt.Sprintf("invalid PEM certificate data for %s", res.Domain))
						continue
					}

					cert, err := x509.ParseCertificate(block.Bytes)
					if err != nil {
						ci.log.Error(fmt.Sprintf("failed to parse certificate %s", res.Domain), "err", err)
						continue
					}

					resources[i].Resource = resp
					resources[i].cert = cert
					ci.pub.Pub(info, []string{fmt.Sprintf("domain-cert-%s", info.Domain)}, tpubsub.WithRetain())
				}

				if earliestRenewAt.After(resources[i].cert.NotAfter) {
					earliestRenewAt = resources[i].cert.NotAfter
				}
			}

			renewCheckIn := time.Until(earliestRenewAt)
			if renewCheckIn >= time.Hour*24 {
				renewCheckIn -= time.Hour * 24
			} else if renewCheckIn <= 0 {
				renewCheckIn = time.Hour
			}

			timer.Reset(renewCheckIn)
		}
	}
}

func initDNSProviders(providers []string) ([]challenge.Provider, error) {
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
			return res, fmt.Errorf("%w: %s", ErrUnsupportedDNSProvider, provider)
		}
	}

	return res, nil
}
