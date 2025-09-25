package cmd

import (
	"bytes"
	"context"
	"crypto"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"

	"github.com/fsnotify/fsnotify"
	"golang.org/x/sync/errgroup"

	"github.com/tendermint/tendermint/libs/log"

	sdkclient "github.com/cosmos/cosmos-sdk/client"
	cryptotypes "github.com/cosmos/cosmos-sdk/crypto/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	sdkquery "github.com/cosmos/cosmos-sdk/types/query"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"

	ctypes "github.com/akash-network/akash-api/go/node/cert/v1beta3"
	"github.com/akash-network/akash-api/go/node/client/v1beta2"
	mtypes "github.com/akash-network/akash-api/go/node/market/v1beta4"
	"github.com/akash-network/node/pubsub"
	cutils "github.com/akash-network/node/x/cert/utils"

	"github.com/akash-network/provider/event"
	"github.com/akash-network/provider/tools/certissuer"
	"github.com/akash-network/provider/tools/fromctx"
	"github.com/akash-network/provider/tools/pconfig"
)

type peerCertReq struct {
	acc      sdk.Address
	serial   *big.Int
	resp     chan<- peerCertResp
	userData any
}

type peerCertResp struct {
	cert     *x509.Certificate
	pubkey   crypto.PublicKey
	userData any
	err      error
}

type accReq struct {
	acc      sdk.Address
	resp     chan<- accResp
	userData any
}

type accResp struct {
	pubkey   cryptotypes.PubKey
	userData any
	err      error
}

type certReq struct {
	domain string
	resp   chan<- certResp
}

type certResp struct {
	certs []tls.Certificate
	err   error
}

type accountQuerier struct {
	ctx        context.Context
	group      *errgroup.Group
	log        log.Logger
	cancel     context.CancelFunc
	bus        pubsub.Bus
	pstorage   pconfig.Storage
	qc         v1beta2.Client
	accCh      chan accReq
	peerCertCh chan peerCertReq
	mtlsCh     chan chan<- certResp
	caCh       chan certReq
	watcher    *fsnotify.Watcher
	mtlsCerts  []tls.Certificate
	caCerts    []tls.Certificate
	cOpts      *accountQuerierOptions
	paddr      sdk.Address
}

type accountQuerierOptions struct {
	tlsDomain   string
	tlsCertFile string
	tlsKeyFile  string
	mtlsPemFile string
}

func aqParseOpts(opts ...AccountQuerierOption) (*accountQuerierOptions, error) {
	ac := &accountQuerierOptions{}

	for _, opt := range opts {
		err := opt(ac)
		if err != nil {
			return nil, err
		}
	}

	return ac, nil
}

type AccountQuerierOption func(*accountQuerierOptions) error

func WithTLSDomainWatch(domain string) AccountQuerierOption {
	return func(options *accountQuerierOptions) error {
		options.tlsDomain = domain
		return nil
	}
}

func WithTLSCert(cert, key string) AccountQuerierOption {
	return func(options *accountQuerierOptions) error {
		options.tlsCertFile = cert
		options.tlsKeyFile = key

		return nil
	}
}

func WithMTLSPem(val string) AccountQuerierOption {
	return func(options *accountQuerierOptions) error {
		options.mtlsPemFile = val

		return nil
	}
}

func newAccountQuerier(ctx context.Context, cctx sdkclient.Context, log log.Logger, bus pubsub.Bus, qc v1beta2.Client, opts ...AccountQuerierOption) (*accountQuerier, error) {
	cOpts, err := aqParseOpts(opts...)
	if err != nil {
		return nil, err
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}

	pstorage, err := fromctx.PersistentConfigFromCtx(ctx)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithCancel(ctx)
	group, ctx := errgroup.WithContext(ctx)

	log = log.With("module", "account-querier")

	q := &accountQuerier{
		ctx:        ctx,
		group:      group,
		cancel:     cancel,
		log:        log,
		bus:        bus,
		pstorage:   pstorage,
		qc:         qc,
		accCh:      make(chan accReq, 1),
		peerCertCh: make(chan peerCertReq, 1),
		mtlsCh:     make(chan chan<- certResp, 1),
		caCh:       make(chan certReq, 1),
		watcher:    watcher,
		paddr:      cctx.FromAddress,
		cOpts:      cOpts,
	}

	if cOpts.tlsCertFile != "" {
		// key-cert pair may not yet be created. don't fail,
		// rather add a directory to watch for a new cert

		_, err := os.Stat(cOpts.tlsCertFile)
		if err == nil {
			cert, err := tls.LoadX509KeyPair(cOpts.tlsCertFile, cOpts.tlsKeyFile)
			if err != nil {
				return nil, err
			}

			q.caCerts = []tls.Certificate{cert}
		}

		dir := filepath.Dir(cOpts.tlsCertFile)
		log.Info(fmt.Sprintf("watching dir '%s' for cert changes", dir))
		err = watcher.Add(dir)
		if err != nil {
			return nil, err
		}
	}

	kpm, err := cutils.NewKeyPairManager(cctx, cctx.FromAddress)
	if err != nil {
		return nil, err
	}

	_, mtlscert, err := kpm.ReadX509KeyPair()
	if err != nil {
		return nil, err
	}

	q.mtlsCerts = []tls.Certificate{
		mtlscert,
	}

	group.Go(q.run)
	group.Go(q.accountQuerier)
	group.Go(q.certsQuerier)
	group.Go(q.leasesQuerier)

	return q, nil
}

func (c *accountQuerier) Close() error {
	select {
	case <-c.ctx.Done():
		return nil
	default:
	}

	c.cancel()

	return c.group.Wait()
}

func (c *accountQuerier) GetAccountCertificate(ctx context.Context, acc sdk.Address, serial *big.Int) (*x509.Certificate, crypto.PublicKey, error) {
	cert, pubkey, err := c.pstorage.GetAccountCertificate(ctx, acc, serial)
	if err == nil {
		return cert, pubkey, nil
	}

	if !errors.Is(err, pconfig.ErrNotExists) {
		return nil, nil, err
	}

	respch := make(chan peerCertResp, 1)
	req := peerCertReq{
		acc:    acc,
		serial: serial,
		resp:   respch,
	}

	select {
	case <-ctx.Done():
		return nil, nil, ctx.Err()
	case <-c.ctx.Done():
		return nil, nil, ctx.Err()
	case c.peerCertCh <- req:
	}

	select {
	case <-ctx.Done():
		return nil, nil, ctx.Err()
	case <-c.ctx.Done():
		return nil, nil, ctx.Err()
	case resp := <-respch:
		return resp.cert, resp.pubkey, resp.err
	}
}

func (c *accountQuerier) GetAccountPublicKey(ctx context.Context, acc sdk.Address) (cryptotypes.PubKey, error) {
	pubkey, err := c.pstorage.GetAccountPublicKey(ctx, acc)
	if err == nil {
		return pubkey, nil
	}

	if !errors.Is(err, pconfig.ErrNotExists) {
		return nil, err
	}

	respch := make(chan accResp, 1)
	req := accReq{
		acc:  acc,
		resp: respch,
	}

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-c.ctx.Done():
		return nil, ctx.Err()
	case c.accCh <- req:
	}

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-c.ctx.Done():
		return nil, ctx.Err()
	case resp := <-respch:
		return resp.pubkey, resp.err
	}
}

func (c *accountQuerier) GetMTLS(ctx context.Context) ([]tls.Certificate, error) {
	respch := make(chan certResp, 1)

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-c.ctx.Done():
		return nil, ctx.Err()
	case c.mtlsCh <- respch:
	}

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-c.ctx.Done():
		return nil, ctx.Err()
	case resp := <-respch:
		return resp.certs, resp.err
	}
}

func (c *accountQuerier) GetCACerts(ctx context.Context, domain string) ([]tls.Certificate, error) {
	respch := make(chan certResp, 1)

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-c.ctx.Done():
		return nil, ctx.Err()
	case c.caCh <- certReq{
		domain: domain,
		resp:   respch,
	}:
	}

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-c.ctx.Done():
		return nil, ctx.Err()
	case resp := <-respch:
		return resp.certs, resp.err
	}
}

func (c *accountQuerier) run() error {
	sub, _ := c.bus.Subscribe()

	defer func() {
		c.cancel()
		sub.Close()
	}()

	var tsub <-chan interface{}

	if c.cOpts.tlsCertFile == "" {
		tbus := fromctx.MustPubSubFromCtx(c.ctx)
		tsub = tbus.Sub(fmt.Sprintf("domain-cert-%s", c.cOpts.tlsDomain))
	}

	eventsch := sub.Events()

	var mtlsRespCh <-chan peerCertResp

	for {
		select {
		case <-c.ctx.Done():
			return c.ctx.Err()
		case data := <-tsub:
			info, valid := data.(certissuer.ResourcesInfo)
			if !valid {
				c.log.Error("received invalid cert issuer data", "type", fmt.Sprintf("%T", data))
				continue
			}

			c.cOpts.tlsCertFile = info.CertFile
			c.cOpts.tlsKeyFile = info.KeyFile

			_, err := os.Stat(c.cOpts.tlsCertFile)
			if err == nil {
				cert, err := tls.LoadX509KeyPair(c.cOpts.tlsCertFile, c.cOpts.tlsKeyFile)
				if err != nil {
					c.log.Error("loading cert/key pair", "err", err.Error())
					continue
				}

				c.caCerts = []tls.Certificate{cert}
			}

			dir := filepath.Dir(c.cOpts.tlsCertFile)
			c.log.Info(fmt.Sprintf("watching dir '%s' for cert changes", dir))

			watching := false

			for _, wdir := range c.watcher.WatchList() {
				if wdir == dir {
					watching = true
					break
				}
			}

			if !watching {
				err = c.watcher.Add(dir)
				if err != nil {
					c.log.Error("watching cert dir", "err", err.Error())
				}
			}
		case evt := <-eventsch:
			switch ev := evt.(type) {
			case event.LeaseWon:
				owner, _ := sdk.AccAddressFromBech32(ev.LeaseID.Owner)
				c.accCh <- accReq{
					acc: owner,
				}
			}
		case req := <-c.mtlsCh:
			req <- certResp{
				certs: c.mtlsCerts,
				err:   nil,
			}
		case req := <-c.caCh:
			req.resp <- certResp{
				certs: c.caCerts,
				err:   nil,
			}
		case resp := <-mtlsRespCh:
			mtlsRespCh = nil

			if resp.err == nil {
				tlsCerts := resp.userData.([]tls.Certificate)
				c.mtlsCerts = tlsCerts
			} else {
				// todo retry query
			}
		case evt := <-c.watcher.Events:
			if c.cOpts.mtlsPemFile != "" && evt.Name == c.cOpts.mtlsPemFile {
				if evt.Has(fsnotify.Create) || evt.Has(fsnotify.Rename) {
					cctx := c.qc.ClientContext()
					kpm, err := cutils.NewKeyPairManager(cctx, cctx.FromAddress)
					if err != nil {
						c.log.Error("couldn't load mtls key manager", "err", err)
						continue
					}

					certFromFlag := bytes.NewBufferString(evt.Name)
					x509cert, tlsCert, err := kpm.ReadX509KeyPair(certFromFlag)
					if err != nil {
						c.log.Error("couldn't read mtls key", "err", err)
						continue
					}

					respCh := make(chan peerCertResp, 1)
					mtlsRespCh = respCh

					c.peerCertCh <- peerCertReq{
						acc:    cctx.FromAddress,
						serial: x509cert.SerialNumber,
						resp:   respCh,
						userData: []tls.Certificate{
							tlsCert,
						},
					}
				} else if evt.Has(fsnotify.Remove) {
					c.mtlsCerts = []tls.Certificate{}
				}
			} else if c.cOpts.tlsCertFile != "" && evt.Name == c.cOpts.tlsCertFile {
				c.log.Info(fmt.Sprintf("detected certificate change, reloading"))
				cert, err := tls.LoadX509KeyPair(c.cOpts.tlsCertFile, c.cOpts.tlsKeyFile)
				if err != nil {
					c.log.Error("unable to load tls certificate", "err", err)
				} else {
					c.caCerts = []tls.Certificate{cert}
				}
			}
		}
	}
}

func (c *accountQuerier) certsQuerier() error {
	for {
		select {
		case <-c.ctx.Done():
			return c.ctx.Err()
		case req := <-c.peerCertCh:
			// Check that the certificate exists on the chain and is not revoked
			cresp, err := c.qc.Query().Certificates(c.ctx, &ctypes.QueryCertificatesRequest{
				Filter: ctypes.CertificateFilter{
					Owner:  req.acc.String(),
					Serial: req.serial.String(),
					State:  "valid",
				},
			})

			resp := peerCertResp{
				userData: req.userData,
				err:      err,
			}

			if err == nil && len(cresp.Certificates) > 0 {
				certData := cresp.Certificates[0]

				blk, rest := pem.Decode(certData.Certificate.Cert)
				if blk == nil || len(rest) > 0 {
					resp.err = ctypes.ErrInvalidCertificateValue
				} else if blk.Type != ctypes.PemBlkTypeCertificate {
					resp.err = fmt.Errorf("%w: invalid pem block type", ctypes.ErrInvalidCertificateValue)
				}

				if resp.err == nil {
					resp.cert, resp.err = x509.ParseCertificate(blk.Bytes)
				}

				if resp.err == nil {
					blk, rest = pem.Decode(certData.Certificate.Pubkey)
					if blk == nil || len(rest) > 0 {
						resp.err = ctypes.ErrInvalidPubkeyValue
					} else if blk.Type != ctypes.PemBlkTypeECPublicKey {
						resp.err = fmt.Errorf("%w: invalid pem block type", ctypes.ErrInvalidPubkeyValue)
					}
				}

				if resp.err == nil {
					resp.pubkey, resp.err = x509.ParsePKIXPublicKey(blk.Bytes)
				}

				if resp.err == nil {
					_ = c.pstorage.AddAccountCertificate(c.ctx, req.acc, resp.cert, resp.pubkey)
				}
			}

			if req.resp != nil {
				req.resp <- resp
			}
		}
	}
}

func (c *accountQuerier) accountQuerier() error {
	requests := make([]accReq, 0)

	signalch := make(chan struct{}, 1)

	trySignal := func() {
		if len(requests) > 0 {
			select {
			case signalch <- struct{}{}:
			default:
			}
		}
	}

	cctx := c.qc.ClientContext()

	for {
		select {
		case <-c.ctx.Done():
			return c.ctx.Err()
		case req := <-c.accCh:
			requests = append(requests, req)
			trySignal()
		case <-signalch:
			req := requests[0]
			requests = requests[1:]

			var pubkey cryptotypes.PubKey
			var err error

			// could be a duplicate request
			if pubkey, err = c.pstorage.GetAccountPublicKey(c.ctx, req.acc); err != nil {
				res, err := c.qc.Query().Auth().Account(c.ctx, &authtypes.QueryAccountRequest{Address: req.acc.String()})
				if err != nil {
					c.log.Error("fetching account info", "err", err.Error(), "account", req.acc.String())
					requests = append(requests, req)
				}

				if err == nil {
					var acc authtypes.AccountI

					err = cctx.InterfaceRegistry.UnpackAny(res.Account, &acc)
					if err != nil {
						c.log.Error("unpacking account info", "err", err.Error(), "account", req.acc.String())
						requests = append(requests, req)
					}

					if err == nil {
						pubkey = acc.GetPubKey()

						err = c.pstorage.AddAccount(c.ctx, acc.GetAddress(), acc.GetPubKey())
						if err != nil && !errors.Is(err, pconfig.ErrExists) {
							c.log.Error("unable to save account pubkey into storage", "owner", acc.GetAddress().String(), "err", err)

							// reset the error as we have got the pubkey and need to pass it above if it was a user request
							err = nil
						}
					}
				}
			}

			if req.resp != nil {
				req.resp <- accResp{
					pubkey:   pubkey,
					userData: req.userData,
					err:      err,
				}
			}
		}
	}
}

func (c *accountQuerier) leasesQuerier() error {
	var nextKey []byte

loop:
	for {
		preq := &sdkquery.PageRequest{
			Key:   nextKey,
			Limit: uint64(100),
		}

		resp, err := c.qc.Query().Leases(c.ctx, &mtypes.QueryLeasesRequest{
			Filters: mtypes.LeaseFilters{
				State:    mtypes.LeaseActive.String(),
				Provider: c.paddr.String(),
			},
			Pagination: preq,
		})
		if err != nil {
			if errors.Is(err, context.Canceled) {
				break
			}

			continue
		}

		if resp.Pagination != nil {
			nextKey = resp.Pagination.NextKey
		}

		for _, lease := range resp.Leases {
			owner, _ := sdk.AccAddressFromBech32(lease.Lease.LeaseID.Owner)

			select {
			case c.accCh <- accReq{
				acc: owner,
			}:
			case <-c.ctx.Done():
				break loop
			}

		}

		if len(nextKey) == 0 {
			break loop
		}
	}

	select {
	case <-c.ctx.Done():
		return c.ctx.Err()
	default:
	}

	return nil
}
