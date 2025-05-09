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

	"github.com/tendermint/tendermint/libs/log"
	"golang.org/x/sync/errgroup"

	sdkclient "github.com/cosmos/cosmos-sdk/client"
	sdk "github.com/cosmos/cosmos-sdk/types"
	sdkquery "github.com/cosmos/cosmos-sdk/types/query"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	"github.com/fsnotify/fsnotify"

	ctypes "github.com/akash-network/akash-api/go/node/cert/v1beta3"
	"github.com/akash-network/akash-api/go/node/client/v1beta2"
	mtypes "github.com/akash-network/akash-api/go/node/market/v1beta4"
	"github.com/akash-network/node/pubsub"
	cutils "github.com/akash-network/node/x/cert/utils"

	"github.com/akash-network/provider/event"
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
	peerCertCh chan peerCertReq
	mtlsCh     chan chan<- certResp
	caCh       chan chan<- certResp
	certReqCh  chan peerCertReq
	watcher    *fsnotify.Watcher
	mtlsCerts  []tls.Certificate
	caCerts    []tls.Certificate
	cOpts      *accountQuerierOptions
	paddr      sdk.Address
}

type accountQuerierOptions struct {
	tlsCertFile string
	tlsKeyFile  string
	mtlsPemFile string
}

func aqParseOpts(opts ...accountQuerierOption) (*accountQuerierOptions, error) {
	ac := &accountQuerierOptions{}

	for _, opt := range opts {
		err := opt(ac)
		if err != nil {
			return nil, err
		}
	}

	return ac, nil
}

type accountQuerierOption func(options *accountQuerierOptions) error

func WithTLSCert(cert, key string) accountQuerierOption {
	return func(options *accountQuerierOptions) error {
		options.tlsCertFile = cert
		options.tlsKeyFile = key

		return nil
	}
}

func WithMTLSPem(val string) accountQuerierOption {
	return func(options *accountQuerierOptions) error {
		options.mtlsPemFile = val

		return nil
	}
}

func newAccountQuerier(ctx context.Context, cctx sdkclient.Context, log log.Logger, bus pubsub.Bus, qc v1beta2.Client, opts ...accountQuerierOption) (*accountQuerier, error) {
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

	q := &accountQuerier{
		ctx:        ctx,
		group:      group,
		cancel:     cancel,
		log:        log.With("module", "account-querier"),
		bus:        bus,
		pstorage:   pstorage,
		qc:         qc,
		peerCertCh: make(chan peerCertReq, 1),
		mtlsCh:     make(chan chan<- certResp, 1),
		caCh:       make(chan chan<- certResp, 1),
		certReqCh:  make(chan peerCertReq, 1),
		watcher:    watcher,
		paddr:      cctx.FromAddress,
		cOpts:      cOpts,
	}

	if cOpts.tlsCertFile != "" {
		cert, err := tls.LoadX509KeyPair(cOpts.tlsCertFile, cOpts.tlsKeyFile)
		if err != nil {
			return nil, err
		}

		q.caCerts = []tls.Certificate{cert}
		err = watcher.Add(cOpts.tlsCertFile)
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

	if !errors.Is(err, os.ErrNotExist) {
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

func (c *accountQuerier) GetCACerts(ctx context.Context) ([]tls.Certificate, error) {
	respch := make(chan certResp, 1)

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-c.ctx.Done():
		return nil, ctx.Err()
	case c.caCh <- respch:
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

	pstorage, _ := fromctx.PersistentConfigFromCtx(c.ctx)

	leasesch := make(chan mtypes.LeaseID, 100)

	eventsch := sub.Events()

	var mtlsRespCh <-chan peerCertResp
	accountReqCh := make(chan sdk.Address, 1)
	accountRespCh := make(chan authtypes.AccountI, 1)

	c.group.Go(func() error {
		return c.accountQuerier(accountReqCh, accountRespCh)
	})

	c.group.Go(func() error {
		return c.leasesQuerier(c.paddr, leasesch)
	})

	c.group.Go(c.certsQuerier)

	for {
		select {
		case <-c.ctx.Done():
			return c.ctx.Err()
		case evt := <-eventsch:
			switch ev := evt.(type) {
			case event.LeaseWon:
				leasesch <- ev.LeaseID
			}
		case lid := <-leasesch:
			owner, _ := sdk.AccAddressFromBech32(lid.Owner)
			_, err := pstorage.GetAccountPublicKey(c.ctx, owner)
			if err != nil {
				accountReqCh <- owner
			}
		case acc := <-accountRespCh:
			err := pstorage.AddAccount(c.ctx, acc.GetAddress(), acc.GetPubKey())
			if err != nil {
				c.log.Error("unable to save account pubkey into storage", "owner", acc.GetAddress().String(), "err", err)
			}
		case req := <-c.peerCertCh:
			c.certReqCh <- peerCertReq{
				acc:    req.acc,
				serial: req.serial,
				resp:   req.resp,
			}
		case req := <-c.mtlsCh:
			req <- certResp{
				certs: c.mtlsCerts,
				err:   nil,
			}
		case req := <-c.caCh:
			req <- certResp{
				certs: nil,
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
						// return err
					}

					certFromFlag := bytes.NewBufferString(evt.Name)
					x509cert, tlsCert, err := kpm.ReadX509KeyPair(certFromFlag)
					if err != nil {
						// return err
					}

					respCh := make(chan peerCertResp, 1)
					mtlsRespCh = respCh

					c.certReqCh <- peerCertReq{
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
		case req := <-c.certReqCh:
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

			req.resp <- resp
		}
	}
}

func (c *accountQuerier) accountQuerier(reqch <-chan sdk.Address, respch chan<- authtypes.AccountI) error {
	requests := make([]sdk.Address, 0)

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
		case addr := <-reqch:
			requests = append(requests, addr)
			trySignal()
		case <-signalch:
			addr := requests[0]
			requests = requests[1:]

			res, err := c.qc.Query().Auth().Account(c.ctx, &authtypes.QueryAccountRequest{Address: addr.String()})
			if err != nil {
				requests = append(requests, addr)
				continue
			}

			var acc authtypes.AccountI
			if err := cctx.InterfaceRegistry.UnpackAny(res.Account, &acc); err != nil {
				requests = append(requests, addr)
				continue
			}

			respch <- acc
		}
	}
}

func (c *accountQuerier) leasesQuerier(paddr sdk.Address, leasesch chan<- mtypes.LeaseID) error {
	var nextKey []byte
	limit := cap(leasesch)

loop:
	for {
		preq := &sdkquery.PageRequest{
			Key:   nextKey,
			Limit: uint64(limit),
		}

		resp, err := c.qc.Query().Leases(c.ctx, &mtypes.QueryLeasesRequest{
			Filters: mtypes.LeaseFilters{
				State:    mtypes.LeaseActive.String(),
				Provider: paddr.String(),
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
			select {
			case leasesch <- lease.Lease.LeaseID:
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

// func (s *accountQuerier) accountWatcherRun() error {
//
// 	log := s.session.Log().With("module", "provider-session", "cmp", "account-watcher")
//
// 	log.Info("ensuring leases owner's public keys and certificates cached")
//
// 	for {
// 		select {
// 		case <-s.ctx.Done():
// 			return s.ctx.Err()
// 		case evt := <-eventsch:
// 			switch ev := evt.(type) {
// 			case event.LeaseWon:
// 				leasesch <- ev.LeaseID
// 			}
// 		case lid := <-leasesch:
// 			owner, _ := sdk.AccAddressFromBech32(lid.Owner)
// 			_, err := pstorage.GetAccountPublicKey(s.ctx, owner)
// 			if err != nil {
// 				accountReqCh <- owner
// 			}
// 		case acc := <-accountRespCh:
// 			err := pstorage.AddAccount(s.ctx, acc.GetAddress(), acc.GetPubKey())
// 			if err != nil {
// 				log.Error("unable to save account pubkey into storage", "owner", acc.GetAddress().String(), "err", err)
// 			}
// 		}
// 	}
// }

// type certQuerier struct {
// 	ctx          context.Context
// 	qc           v1beta2.Client
// 	peerCertCh   chan peerCertReq
// 	mtlsCh       chan chan<- certResp
// 	caCh         chan chan<- certResp
// 	certReqCh    chan peerCertReq
// 	watcher      *fsnotify.Watcher
// 	mtlsFileName string
// 	mtlsCerts    []tls.Certificate
// 	caCerts      []tls.Certificate
// }
//
// func setupCertQuerier(ctx context.Context, qc v1beta2.Client, mtlsCert string) (gwutils.CertGetter, error) {
// 	watcher, err := fsnotify.NewWatcher()
// 	if err != nil {
// 		return nil, err
// 	}
//
// 	q := &certQuerier{
// 		ctx:        ctx,
// 		qc:         qc,
// 		peerCertCh: make(chan peerCertReq, 1),
// 		mtlsCh:     make(chan chan<- certResp, 1),
// 		caCh:       make(chan chan<- certResp, 1),
// 		certReqCh:  make(chan peerCertReq, 1),
// 		watcher:    watcher,
// 	}
//
// 	if mtlsCert != "" {
// 		err = watcher.Add(filepath.Dir(mtlsCert))
// 		if err != nil {
// 			return nil, err
// 		}
//
// 		q.mtlsFileName = mtlsCert
// 	}
//
// 	go q.run()
// 	go q.runQuerier()
//
// 	return q, nil
// }
//
// func (c *accountQuerier) certQuerier() {
// 	defer func() {
// 		_ = c.watcher.Close()
// 	}()
//
// 	var mtlsRespCh <-chan peerCertResp
//
// 	for {
// 		select {
// 		case <-c.ctx.Done():
// 			return
// 		case req := <-c.peerCertCh:
// 			c.certReqCh <- peerCertReq{
// 				acc:    req.acc,
// 				serial: req.serial,
// 				resp:   req.resp,
// 			}
// 		case req := <-c.mtlsCh:
// 			req <- certResp{
// 				certs: c.mtlsCerts,
// 				err:   nil,
// 			}
// 		case req := <-c.caCh:
// 			req <- certResp{
// 				certs: nil,
// 				err:   nil,
// 			}
// 		case resp := <-mtlsRespCh:
// 			mtlsRespCh = nil
//
// 			if resp.err == nil {
// 				tlsCerts := resp.userData.([]tls.Certificate)
// 				c.mtlsCerts = tlsCerts
// 			} else {
// 				// todo retry query
// 			}
// 		case evt := <-c.watcher.Events:
// 			if c.mtlsFileName != "" && evt.Name == c.mtlsFileName {
// 				if evt.Has(fsnotify.Create) || evt.Has(fsnotify.Rename) {
// 					cctx := c.qc.ClientContext()
// 					kpm, err := cutils.NewKeyPairManager(cctx, cctx.FromAddress)
// 					if err != nil {
// 						// return err
// 					}
//
// 					certFromFlag := bytes.NewBufferString(evt.Name)
// 					x509cert, tlsCert, err := kpm.ReadX509KeyPair(certFromFlag)
// 					if err != nil {
// 						// return err
// 					}
//
// 					respCh := make(chan peerCertResp, 1)
// 					mtlsRespCh = respCh
//
// 					c.certReqCh <- peerCertReq{
// 						acc:    cctx.FromAddress,
// 						serial: x509cert.SerialNumber,
// 						resp:   respCh,
// 						userData: []tls.Certificate{
// 							tlsCert,
// 						},
// 					}
// 				} else if evt.Has(fsnotify.Remove) {
// 					c.mtlsCerts = []tls.Certificate{}
// 				}
// 			}
// 		}
// 	}
// }
