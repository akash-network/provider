package provider

import (
	"context"
	"errors"
	"fmt"

	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/crypto/keyring"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/tx/signing"

	aclient "pkg.akt.dev/go/node/client/v1beta3"

	ptypes "github.com/akash-network/provider/types"
)

var (
	errProviderSignerMissingAddress              = errors.New("provider signer missing address")
	errProviderSignerMissingKeyring              = errors.New("provider signer missing keyring")
	errProviderSignerMissingTxClient             = errors.New("provider signer missing tx client")
	errProviderSignerUnexpectedBroadcastResponse = errors.New("provider signer unexpected broadcast response")
)

type providerSigner struct {
	address sdk.AccAddress
	keyring keyring.Signer
	tx      aclient.TxClient
}

var _ ptypes.ProviderSigner = (*providerSigner)(nil)

func NewProviderSigner(cctx client.Context, tx aclient.TxClient) (ptypes.ProviderSigner, error) {
	if len(cctx.FromAddress) == 0 {
		return nil, errProviderSignerMissingAddress
	}

	if cctx.Keyring == nil {
		return nil, errProviderSignerMissingKeyring
	}

	if tx == nil {
		return nil, errProviderSignerMissingTxClient
	}

	return &providerSigner{
		address: cctx.FromAddress,
		keyring: cctx.Keyring,
		tx:      tx,
	}, nil
}

func (s *providerSigner) Address() sdk.AccAddress {
	return s.address
}

func (s *providerSigner) Sign(ctx context.Context, payload []byte) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	signature, _, err := s.keyring.SignByAddress(s.address, payload, signing.SignMode_SIGN_MODE_DIRECT)
	if err != nil {
		return nil, err
	}

	return signature, nil
}

func (s *providerSigner) Broadcast(ctx context.Context, msgs ...sdk.Msg) (*sdk.TxResponse, error) {
	resp, err := s.tx.BroadcastMsgs(ctx, msgs, aclient.WithResultCodeAsError())
	if err != nil {
		return nil, err
	}

	txResp, ok := resp.(*sdk.TxResponse)
	if !ok || txResp == nil {
		return nil, fmt.Errorf("%w: %T", errProviderSignerUnexpectedBroadcastResponse, resp)
	}

	return txResp, nil
}
