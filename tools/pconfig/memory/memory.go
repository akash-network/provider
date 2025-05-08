package memory

import (
	"context"
	"crypto"
	"crypto/x509"
	"math/big"
	"sync"

	cryptotypes "github.com/cosmos/cosmos-sdk/crypto/types"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/akash-network/provider/tools/pconfig"
)

type certificate struct {
	cert   *x509.Certificate
	pubkey crypto.PublicKey
}

type account struct {
	pubkey cryptotypes.PubKey
	certs  map[string]certificate
}
type impl struct {
	closed chan struct{}

	lock     sync.RWMutex
	accounts map[string]account
}

var _ pconfig.Storage = (*impl)(nil)

func NewMemory() (pconfig.Storage, error) {
	b := &impl{
		closed:   make(chan struct{}),
		accounts: make(map[string]account),
	}

	return b, nil
}

func (i *impl) AddAccount(_ context.Context, address sdk.Address, pubkey cryptotypes.PubKey) error {
	if address.Empty() || pubkey == nil {
		return pconfig.ErrInvalidArgs
	}

	defer i.lock.Unlock()
	i.lock.Lock()

	_, exists := i.accounts[address.String()]
	if exists {
		return pconfig.ErrAccountExists
	}

	i.accounts[address.String()] = account{
		pubkey: pubkey,
		certs:  make(map[string]certificate),
	}

	return nil
}

func (i *impl) DelAccount(_ context.Context, address sdk.Address) error {
	if address.Empty() {
		return pconfig.ErrInvalidArgs
	}

	defer i.lock.Unlock()
	i.lock.Lock()

	acc, exists := i.accounts[address.String()]
	if !exists {
		return pconfig.ErrAccountNotExists
	}

	for id := range acc.certs {
		delete(acc.certs, id)
	}

	delete(i.accounts, address.String())

	return nil
}

func (i *impl) GetAllCertificates(_ context.Context) ([]*x509.Certificate, error) {
	defer i.lock.RUnlock()
	i.lock.RLock()

	res := make([]*x509.Certificate, 0)

	for _, acc := range i.accounts {
		for _, cert := range acc.certs {
			res = append(res, cert.cert)
		}
	}

	return res, nil
}

func (i *impl) GetAccountPublicKey(_ context.Context, address sdk.Address) (cryptotypes.PubKey, error) {
	if address.Empty() {
		return nil, pconfig.ErrInvalidArgs
	}

	defer i.lock.RUnlock()
	i.lock.RLock()

	acc, exists := i.accounts[address.String()]
	if !exists {
		return nil, pconfig.ErrAccountNotExists
	}

	return acc.pubkey, nil
}

func (i *impl) GetAccountCertificate(_ context.Context, address sdk.Address, serial *big.Int) (*x509.Certificate, crypto.PublicKey, error) {
	if address.Empty() || serial == nil {
		return nil, nil, pconfig.ErrInvalidArgs
	}

	defer i.lock.RUnlock()
	i.lock.RLock()

	acc, exists := i.accounts[address.String()]
	if !exists {
		return nil, nil, pconfig.ErrAccountNotExists
	}

	cert, exists := acc.certs[serial.String()]
	if !exists {
		return nil, nil, pconfig.ErrCertificateNotExists
	}

	return cert.cert, cert.pubkey, nil
}

func (i *impl) AddAccountCertificate(_ context.Context, address sdk.Address, cert *x509.Certificate, pubkey crypto.PublicKey) error {
	if address.Empty() || cert == nil || pubkey == nil {
		return pconfig.ErrInvalidArgs
	}

	defer i.lock.Unlock()
	i.lock.Lock()

	acc, exists := i.accounts[address.String()]
	if !exists {
		return pconfig.ErrAccountNotExists
	}

	if _, exists := acc.certs[cert.SerialNumber.String()]; exists {
		return pconfig.ErrCertificateExists
	}

	acc.certs[cert.SerialNumber.String()] = certificate{
		cert:   cert,
		pubkey: pubkey,
	}

	return nil
}

func (i *impl) DelAccountCertificate(_ context.Context, address sdk.Address, serial *big.Int) error {
	if address.Empty() || serial == nil {
		return pconfig.ErrInvalidArgs
	}

	defer i.lock.Unlock()
	i.lock.Lock()

	acc, exists := i.accounts[address.String()]
	if !exists {
		return pconfig.ErrAccountNotExists
	}

	delete(acc.certs, serial.String())

	return nil
}

func (i *impl) Close() error {
	select {
	case <-i.closed:
	default:
		close(i.closed)
	}

	return nil
}
