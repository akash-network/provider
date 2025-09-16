package bbolt

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"

	ctypes "github.com/akash-network/akash-api/go/node/cert/v1beta3"
	"github.com/cosmos/cosmos-sdk/crypto/keys/secp256k1"
	cryptotypes "github.com/cosmos/cosmos-sdk/crypto/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"go.etcd.io/bbolt"
	berrors "go.etcd.io/bbolt/errors"

	"github.com/akash-network/provider/tools/pconfig"
)

var (
	ErrUninitialized            = errors.New("bbolt: un-initialized database")
	ErrPubKeyEntry              = errors.New("bbolt: account does not contain pubkey")
	ErrDBClosed                 = errors.New("bbolt: database instance closed")
	ErrCertificateNotFoundInPEM = fmt.Errorf("%w: certificate not found in PEM", ctypes.ErrCertificate)
	ErrInvalidPubKeyType        = errors.New("bbolt: invalid pubkey type")
)

var (
	bucketAccounts     = []byte("accounts")
	keyBuckets         = []byte("buckets")
	keyPubKey          = []byte("pubkey")
	bucketCertificates = []byte("certificates")
	keyCertificate     = []byte("certificate")
	bucketProvider     = []byte("provider")
	bucketBidengine    = []byte("bidengine")
	keyOrdersNextKey   = []byte("orders-nextkey")
)

type impl struct {
	db     *bbolt.DB
	closed chan struct{}
}

var _ pconfig.Storage = (*impl)(nil)

type account struct {
	Address sdk.Address `boltholdKey:"Address" boltholdUnique:"UniqueAddress"`
	PubKey  cryptotypes.PubKey
}

func NewBBolt(path string) (pconfig.Storage, error) {
	db, err := bbolt.Open(path, 0600, nil)
	if err != nil {
		return nil, err
	}

	err = db.Update(func(tx *bbolt.Tx) error {
		_, err := tx.CreateBucketIfNotExists(bucketAccounts)
		if err != nil {
			return fmt.Errorf("create bucket: %s", err)
		}

		_, err = tx.CreateBucketIfNotExists(bucketProvider)
		if err != nil {
			return fmt.Errorf("create bucket: %s", err)
		}

		return nil
	})

	b := &impl{
		db:     db,
		closed: make(chan struct{}),
	}

	return b, nil
}

func (b *impl) Close() error {
	select {
	case <-b.closed:
		return nil
	default:
	}

	close(b.closed)

	return b.db.Close()
}

func (b *impl) AddAccount(_ context.Context, acc sdk.Address, pubkey cryptotypes.PubKey) error {
	select {
	case <-b.closed:
		return ErrDBClosed
	default:
	}

	if acc.Empty() || pubkey == nil {
		return pconfig.ErrInvalidArgs
	}

	err := b.db.Update(func(tx *bbolt.Tx) error {
		accounts := tx.Bucket(bucketAccounts)
		if accounts == nil {
			return ErrUninitialized
		}

		account, err := accounts.CreateBucket(acc.Bytes())
		if err != nil {
			if errors.Is(err, berrors.ErrBucketExists) {
				return pconfig.ErrAccountExists
			}

			return err
		}

		err = account.Put(keyPubKey, pubkey.Bytes())
		if err != nil {
			return err
		}

		return nil
	})

	if err != nil {
		return err
	}

	return nil
}

func (b *impl) DelAccount(_ context.Context, acc sdk.Address) error {
	if acc.Empty() {
		return pconfig.ErrInvalidArgs
	}

	err := b.db.Update(func(tx *bbolt.Tx) error {
		accounts := tx.Bucket(bucketAccounts)
		if accounts == nil {
			return ErrUninitialized
		}

		err := accounts.DeleteBucket(acc.Bytes())
		if err != nil {
			if errors.Is(err, berrors.ErrBucketExists) {
				return pconfig.ErrAccountNotExists
			}

			return err
		}

		return nil
	})

	if err != nil {
		return err
	}

	return nil
}

func (b *impl) GetAccountPublicKey(_ context.Context, acc sdk.Address) (cryptotypes.PubKey, error) {
	var res cryptotypes.PubKey

	if acc.Empty() {
		return nil, pconfig.ErrInvalidArgs
	}

	err := b.db.View(func(tx *bbolt.Tx) error {
		accounts := tx.Bucket(bucketAccounts)
		if accounts == nil {
			return ErrUninitialized
		}

		account := accounts.Bucket(acc.Bytes())
		if account == nil {
			return pconfig.ErrAccountNotExists
		}

		pubkey := account.Get(keyPubKey)
		res = &secp256k1.PubKey{Key: pubkey}

		return nil
	})

	if err != nil {
		return nil, err
	}

	return res, nil
}

func (b *impl) AddAccountCertificate(_ context.Context, acc sdk.Address, cert *x509.Certificate, pubkey crypto.PublicKey) error {
	if acc.Empty() || cert == nil || pubkey == nil {
		return pconfig.ErrInvalidArgs
	}

	err := b.db.Update(func(tx *bbolt.Tx) error {
		accounts := tx.Bucket(bucketAccounts)
		if accounts == nil {
			return ErrUninitialized
		}

		account := accounts.Bucket(acc.Bytes())
		if account == nil {
			return pconfig.ErrAccountNotExists
		}

		certs, err := account.CreateBucketIfNotExists(bucketCertificates)
		if err != nil {
			return err
		}

		certBucket, err := certs.CreateBucket(cert.SerialNumber.Bytes())
		if err != nil {
			if errors.Is(err, berrors.ErrBucketExists) {
				return pconfig.ErrCertificateExists
			}
			return err
		}
		certData := pem.EncodeToMemory(&pem.Block{Type: ctypes.PemBlkTypeCertificate, Bytes: cert.Raw})

		err = certBucket.Put(keyCertificate, certData)
		if err != nil {
			return err
		}

		pk, err := x509.MarshalPKIXPublicKey(pubkey)
		if err != nil {
			return fmt.Errorf("%w: failed extracting public key", err)
		}

		err = certBucket.Put(keyPubKey, pk)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return err
	}

	return nil
}

func (b *impl) DelAccountCertificate(_ context.Context, acc sdk.Address, serial *big.Int) error {
	if acc.Empty() || serial == nil {
		return pconfig.ErrInvalidArgs
	}

	err := b.db.Update(func(tx *bbolt.Tx) error {
		accounts := tx.Bucket(bucketAccounts)
		if accounts == nil {
			return ErrUninitialized
		}

		account := accounts.Bucket(acc.Bytes())
		if account == nil {
			return pconfig.ErrAccountNotExists
		}

		certs := account.Bucket(bucketCertificates)
		if certs == nil {
			return pconfig.ErrCertificateNotExists
		}

		err := certs.DeleteBucket(serial.Bytes())
		if err != nil {
			if errors.Is(err, berrors.ErrBucketNotFound) {
				return pconfig.ErrCertificateNotExists
			}

			return err
		}

		return nil
	})

	if err != nil {
		return err
	}

	return nil
}

func (b *impl) GetAccountCertificate(_ context.Context, acc sdk.Address, serial *big.Int) (*x509.Certificate, crypto.PublicKey, error) {
	if acc.Empty() || serial == nil {
		return nil, nil, pconfig.ErrInvalidArgs
	}

	var cert *x509.Certificate
	var pubKey crypto.PublicKey

	err := b.db.View(func(tx *bbolt.Tx) error {
		accounts := tx.Bucket(bucketAccounts)
		if accounts == nil {
			return ErrUninitialized
		}

		account := accounts.Bucket(acc.Bytes())
		if account == nil {
			return pconfig.ErrAccountNotExists
		}

		certs := account.Bucket(bucketCertificates)
		if certs == nil {
			return pconfig.ErrCertificateNotExists
		}

		certBucket := certs.Bucket(serial.Bytes())
		if certBucket == nil {
			return pconfig.ErrCertificateNotExists
		}

		block, _ := pem.Decode(certBucket.Get(keyCertificate))
		if block == nil {
			return ErrCertificateNotFoundInPEM
		}

		var err error
		cert, err = x509.ParseCertificate(block.Bytes)
		if err != nil {
			return err
		}

		pk, err := x509.ParsePKIXPublicKey(certBucket.Get(keyPubKey))
		if err != nil {
			return err
		}

		var valid bool
		pubKey, valid = pk.(*ecdsa.PublicKey)
		if !valid {
			return ErrInvalidPubKeyType
		}

		return nil
	})

	if err != nil {
		return nil, nil, err
	}

	return cert, pubKey, nil
}

func (b *impl) GetAllCertificates(_ context.Context) ([]*x509.Certificate, error) {
	res := make([]*x509.Certificate, 0)

	err := b.db.View(func(tx *bbolt.Tx) error {
		accountsBucket := tx.Bucket(bucketAccounts)
		if accountsBucket == nil {
			return ErrUninitialized
		}

		return accountsBucket.ForEachBucket(func(k []byte) error {
			account := accountsBucket.Bucket(k)

			certsBucket := account.Bucket(bucketCertificates)
			if certsBucket == nil {
				return nil
			}

			return certsBucket.ForEachBucket(func(k []byte) error {
				certBucket := certsBucket.Bucket(k)

				block, _ := pem.Decode(certBucket.Get(keyCertificate))
				if block == nil {
					return ErrCertificateNotFoundInPEM
				}

				cert, err := x509.ParseCertificate(block.Bytes)
				if err != nil {
					return err
				}

				res = append(res, cert)

				return nil
			})
		})
	})

	if err != nil {
		return nil, err
	}

	return res, nil
}

func (b *impl) SetOrdersNextKey(_ context.Context, key []byte) error {
	err := b.db.Update(func(tx *bbolt.Tx) error {
		provider := tx.Bucket(bucketProvider)
		if provider == nil {
			return ErrUninitialized
		}

		bidengine := provider.Bucket(bucketBidengine)
		if bidengine == nil {
			return ErrUninitialized
		}

		err := bidengine.Put(keyOrdersNextKey, key)
		if err != nil {
			return err
		}

		return nil
	})

	if err != nil {
		return err
	}

	return nil
}

func (b *impl) DelOrdersNextKey(_ context.Context) error {
	err := b.db.Update(func(tx *bbolt.Tx) error {
		provider := tx.Bucket(bucketProvider)
		if provider == nil {
			return ErrUninitialized
		}

		bidengine := provider.Bucket(bucketBidengine)
		if bidengine == nil {
			return ErrUninitialized
		}

		err := bidengine.Delete(keyOrdersNextKey)
		if err != nil {
			return err
		}

		return nil
	})

	if err != nil {
		return err
	}

	return nil
}

func (b *impl) GetOrdersNextKey(_ context.Context) ([]byte, error) {
	var nextkey []byte

	err := b.db.View(func(tx *bbolt.Tx) error {
		provider := tx.Bucket(bucketProvider)
		if provider == nil {
			return ErrUninitialized
		}

		bidengine := provider.Bucket(bucketBidengine)
		if bidengine == nil {
			return pconfig.ErrNotExists
		}

		nextkey = bidengine.Get(keyOrdersNextKey)

		return nil
	})

	if err == nil && len(nextkey) == 0 {
		err = pconfig.ErrNotExists
	}

	if err != nil {
		return nil, err
	}

	return nextkey, nil
}
