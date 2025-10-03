package pconfig

import (
	"context"
	"crypto"
	"crypto/x509"
	"errors"
	"fmt"
	"math/big"

	cryptotypes "github.com/cosmos/cosmos-sdk/crypto/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
)

var (
	ErrInvalidArgs          = errors.New("pstorage: invalid arguments")
	ErrNotExists            = errors.New("pstorage: not exists")
	ErrExists               = errors.New("pstorage: exists")
	ErrAccountNotExists     = fmt.Errorf("%w: account", ErrNotExists)
	ErrAccountExists        = fmt.Errorf("%w: account", ErrExists)
	ErrCertificateNotExists = fmt.Errorf("%w: certificate", ErrNotExists)
	ErrCertificateExists    = fmt.Errorf("%w: certificate", ErrExists)
)

type StorageR interface {
	GetAccountPublicKey(context.Context, sdk.Address) (cryptotypes.PubKey, error)
	GetAccountCertificate(context.Context, sdk.Address, *big.Int) (*x509.Certificate, crypto.PublicKey, error)
	GetAllCertificates(ctx context.Context) ([]*x509.Certificate, error)
	GetOrdersNextKey(ctx context.Context) ([]byte, error)
}

type StorageW interface {
	AddAccount(context.Context, sdk.Address, cryptotypes.PubKey) error
	DelAccount(context.Context, sdk.Address) error
	AddAccountCertificate(context.Context, sdk.Address, *x509.Certificate, crypto.PublicKey) error
	DelAccountCertificate(context.Context, sdk.Address, *big.Int) error
	SetOrdersNextKey(ctx context.Context, key []byte) error
	DelOrdersNextKey(ctx context.Context) error
}

type Storage interface {
	StorageR
	StorageW
	Close() error
}

type StorageNextKey interface {
	SetOrdersNextKey(ctx context.Context, key []byte) error
	DelOrdersNextKey(ctx context.Context) error
	GetOrdersNextKey(ctx context.Context) ([]byte, error)
}
