package pconfig_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"math/big"
	"os"
	"path/filepath"
	"testing"

	"github.com/akash-network/akash-api/go/testutil"
	"github.com/cosmos/cosmos-sdk/crypto/keys/secp256k1"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	"github.com/akash-network/provider/tools/pconfig"
	"github.com/akash-network/provider/tools/pconfig/bbolt"
	"github.com/akash-network/provider/tools/pconfig/memory"
)

type testBackend struct {
	pconfig.Storage
	name    string
	cleanup func()
}

func initTestBackends(t *testing.T) []testBackend {
	dir, err := os.MkdirTemp("", "bbolt-test-*")
	require.NoError(t, err)

	dbPath := filepath.Join(dir, "test.db")

	bdb, err := bbolt.NewBBolt(dbPath)
	require.NoError(t, err)

	mdb, err := memory.NewMemory()
	require.NoError(t, err)

	return []testBackend{
		{
			Storage: bdb,
			name:    "bbolt",
			cleanup: func() {
				err := bdb.Close()
				require.NoError(t, err)

				err = os.RemoveAll(dir)
				require.NoError(t, err)
			},
		},
		{
			Storage: mdb,
			name:    "memory",
			cleanup: func() {
				err := mdb.Close()
				require.NoError(t, err)
			},
		},
	}
}

func setupTestDB(t *testing.T) (string, func()) {
	dir, err := os.MkdirTemp("", "bbolt-test-*")
	require.NoError(t, err)

	dbPath := filepath.Join(dir, "test.db")
	cleanup := func() {
		err = os.RemoveAll(dir)
	}

	return dbPath, cleanup
}

func TestNewDB(t *testing.T) {
	dbs := initTestBackends(t)
	require.Len(t, dbs, 2)

	defer func() {
		for _, db := range dbs {
			db.cleanup()
		}
	}()
}

func TestStorage_AccountOperations(t *testing.T) {
	dbs := initTestBackends(t)
	require.Len(t, dbs, 2)

	defer func() {
		for _, db := range dbs {
			db.cleanup()
		}
	}()

	for _, db := range dbs {
		ctx := context.Background()
		privKey := secp256k1.GenPrivKey()
		pubKey := privKey.PubKey()
		addr := sdk.AccAddress(pubKey.Address())

		// Test AddAccount
		err := db.AddAccount(ctx, addr, pubKey)
		require.NoError(t, err)

		// Test duplicate AddAccount
		err = db.AddAccount(ctx, addr, pubKey)
		require.ErrorIs(t, err, pconfig.ErrExists)

		// Test GetAccountPublicKey
		retrievedPubKey, err := db.GetAccountPublicKey(ctx, addr)
		require.NoError(t, err)
		require.Equal(t, pubKey, retrievedPubKey)

		// Test GetAccountPublicKey with a non-existent account
		nonExistentAddr := sdk.AccAddress([]byte("non-existent"))
		_, err = db.GetAccountPublicKey(ctx, nonExistentAddr)
		require.ErrorIs(t, err, pconfig.ErrNotExists)

		// Test DelAccount
		err = db.DelAccount(ctx, addr)
		require.NoError(t, err)

		// Verify account is deleted
		_, err = db.GetAccountPublicKey(ctx, addr)
		require.ErrorIs(t, err, pconfig.ErrNotExists)
	}
}

func TestStorage_CertificateOperations(t *testing.T) {
	dbs := initTestBackends(t)
	require.Len(t, dbs, 2)

	defer func() {
		for _, db := range dbs {
			db.cleanup()
		}
	}()

	for _, db := range dbs {
		ctx := context.Background()
		privKey := testutil.Key(t)
		pubKey := privKey.PubKey()
		addr := sdk.AccAddress(pubKey.Address())

		// Add an account first
		err := db.AddAccount(ctx, addr, pubKey)
		require.NoError(t, err)

		cert := testutil.Certificate(t, privKey, testutil.CertificateOptionCache(db))

		xCert := cert.Cert[0].Leaf

		// Test duplicate AddAccountCertificate
		err = db.AddAccountCertificate(ctx, addr, xCert, xCert.PublicKey)
		require.ErrorIs(t, err, pconfig.ErrExists)

		// Test GetAccountCertificate
		retrievedCert, retrievedPubKey, err := db.GetAccountCertificate(ctx, addr, xCert.SerialNumber)
		require.NoError(t, err)
		require.Equal(t, xCert, retrievedCert)
		require.Equal(t, xCert.PublicKey, retrievedPubKey)

		// Test GetAllCertificates
		certs, err := db.GetAllCertificates(ctx)
		require.NoError(t, err)
		require.Len(t, certs, 1)
		require.Equal(t, xCert, certs[0])

		// Test DelAccountCertificate
		err = db.DelAccountCertificate(ctx, addr, cert.Cert[0].Leaf.SerialNumber)
		require.NoError(t, err)

		// Verify certificate is deleted
		_, _, err = db.GetAccountCertificate(ctx, addr, cert.Cert[0].Leaf.SerialNumber)
		require.ErrorIs(t, err, pconfig.ErrNotExists)
	}
}

func TestSStorage_InvalidOperations(t *testing.T) {
	dbs := initTestBackends(t)
	require.Len(t, dbs, 2)

	defer func() {
		for _, db := range dbs {
			db.cleanup()
		}
	}()

	for _, db := range dbs {
		ctx := context.Background()
		privKey := secp256k1.GenPrivKey()
		pubKey := privKey.PubKey()
		addr := sdk.AccAddress(pubKey.Address())

		// Test AddAccount with empty address
		err := db.AddAccount(ctx, sdk.AccAddress{}, pubKey)
		require.Error(t, err)

		// Test AddAccount with nil pubkey
		err = db.AddAccount(ctx, addr, nil)
		require.Error(t, err)

		// Test DelAccount with empty address
		err = db.DelAccount(ctx, sdk.AccAddress{})
		require.Error(t, err)

		// Test GetAccountPublicKey with empty address
		_, err = db.GetAccountPublicKey(ctx, sdk.AccAddress{})
		require.Error(t, err)

		// Test GetAccountCertificate with empty address
		_, _, err = db.GetAccountCertificate(ctx, sdk.AccAddress{}, big.NewInt(1))
		require.Error(t, err)

		// Test AddAccountCertificate with empty address
		_, err = ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		require.NoError(t, err)
		err = db.AddAccountCertificate(ctx, sdk.AccAddress{}, nil, nil)
		require.Error(t, err)

		// Test DelAccountCertificate with empty address
		err = db.DelAccountCertificate(ctx, sdk.AccAddress{}, nil)
		require.Error(t, err)
	}
}

func TestStorage_ConcurrentOperations(t *testing.T) {
	dbs := initTestBackends(t)
	require.Len(t, dbs, 2)

	defer func() {
		for _, db := range dbs {
			db.cleanup()
		}
	}()

	for _, db := range dbs {
		ctx := context.Background()
		privKey := secp256k1.GenPrivKey()
		pubKey := privKey.PubKey()
		addr := sdk.AccAddress(pubKey.Address())

		// Add initial account
		err := db.AddAccount(ctx, addr, pubKey)
		require.NoError(t, err)

		// Create multiple goroutines to perform operations concurrently
		done := make(chan struct{})
		for i := 0; i < 10; i++ {
			go func() {
				defer func() { done <- struct{}{} }()

				// Perform various operations
				_, _ = db.GetAccountPublicKey(ctx, addr)
				_, _ = db.GetAllCertificates(ctx)
			}()
		}

		// Wait for all goroutines to complete
		for i := 0; i < 10; i++ {
			<-done
		}
	}
}
