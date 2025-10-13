package client

import (
	"context"
	"crypto"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"

	ctypes "github.com/akash-network/akash-api/go/node/cert/v1beta3"
	aclient "github.com/akash-network/akash-api/go/node/client/v1beta2"
	sdk "github.com/cosmos/cosmos-sdk/types"
)

type CertificateQuerier struct {
	cclient aclient.QueryClient
}

func NewCertificateQuerier(cclient aclient.QueryClient) *CertificateQuerier {
	return &CertificateQuerier{cclient: cclient}
}

func (c *CertificateQuerier) GetAccountCertificate(ctx context.Context, owner sdk.Address, serial *big.Int) (*x509.Certificate, crypto.PublicKey, error) {
	if c.cclient == nil {
		return nil, nil, errors.New("rpc client not set")
	}

	cresp, err := c.cclient.Certificates(ctx, &ctypes.QueryCertificatesRequest{
		Filter: ctypes.CertificateFilter{
			Owner:  owner.String(),
			Serial: serial.String(),
			State:  ctypes.CertificateValid.String(),
		},
	})
	if err != nil {
		return nil, nil, err
	}

	certData := cresp.Certificates[0]

	blk, rest := pem.Decode(certData.Certificate.Cert)
	if blk == nil || len(rest) > 0 {
		return nil, nil, ctypes.ErrInvalidCertificateValue
	} else if blk.Type != ctypes.PemBlkTypeCertificate {
		return nil, nil, fmt.Errorf("%w: invalid pem block type", ctypes.ErrInvalidCertificateValue)
	}

	cert, err := x509.ParseCertificate(blk.Bytes)
	if err != nil {
		return nil, nil, err
	}

	blk, rest = pem.Decode(certData.Certificate.Pubkey)
	if blk == nil || len(rest) > 0 {
		return nil, nil, ctypes.ErrInvalidPubkeyValue
	} else if blk.Type != ctypes.PemBlkTypeECPublicKey {
		return nil, nil, fmt.Errorf("%w: invalid pem block type", ctypes.ErrInvalidPubkeyValue)
	}

	pubkey, err := x509.ParsePKIXPublicKey(blk.Bytes)
	if err != nil {
		return nil, nil, err
	}

	return cert, pubkey, nil
}
