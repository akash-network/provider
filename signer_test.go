package provider

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	sdkclient "github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/crypto/hd"
	"github.com/cosmos/cosmos-sdk/crypto/keyring"
	sdk "github.com/cosmos/cosmos-sdk/types"

	aclient "pkg.akt.dev/go/node/client/v1beta3"
	"pkg.akt.dev/go/sdkutil"
	"pkg.akt.dev/go/testutil"
)

type recordingTxClient struct {
	response interface{}
	err      error

	called bool
	msgs   []sdk.Msg
	opts   []aclient.BroadcastOption
}

func (c *recordingTxClient) BroadcastMsgs(_ context.Context, msgs []sdk.Msg, opts ...aclient.BroadcastOption) (interface{}, error) {
	c.called = true
	c.msgs = msgs
	c.opts = opts

	return c.response, c.err
}

func (c *recordingTxClient) BroadcastTx(context.Context, sdk.Tx, ...aclient.BroadcastOption) (interface{}, error) {
	return nil, errors.New("unused")
}

func newProviderSignerTestKeyring(t *testing.T) (testutil.Keyring, sdk.AccAddress) {
	t.Helper()

	encCfg := sdkutil.MakeEncodingConfig()
	kr := testutil.NewTestKeyring(encCfg.Codec)
	record, _, err := kr.NewMnemonic(
		"provider",
		keyring.English,
		sdk.FullFundraiserPath,
		keyring.DefaultBIP39Passphrase,
		hd.Secp256k1,
	)
	require.NoError(t, err)

	addr, err := record.GetAddress()
	require.NoError(t, err)

	return kr, addr
}

func TestNewProviderSignerValidatesInputs(t *testing.T) {
	kr, addr := newProviderSignerTestKeyring(t)
	tx := &recordingTxClient{response: &sdk.TxResponse{}}

	tests := []struct {
		name    string
		cctx    sdkclient.Context
		tx      aclient.TxClient
		wantErr error
	}{
		{
			name: "success",
			cctx: sdkclient.Context{
				FromAddress: addr,
				Keyring:     kr,
			},
			tx: tx,
		},
		{
			name: "missing address",
			cctx: sdkclient.Context{
				Keyring: kr,
			},
			tx:      tx,
			wantErr: errProviderSignerMissingAddress,
		},
		{
			name: "missing keyring",
			cctx: sdkclient.Context{
				FromAddress: addr,
			},
			tx:      tx,
			wantErr: errProviderSignerMissingKeyring,
		},
		{
			name: "missing tx client",
			cctx: sdkclient.Context{
				FromAddress: addr,
				Keyring:     kr,
			},
			wantErr: errProviderSignerMissingTxClient,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			signer, err := NewProviderSigner(test.cctx, test.tx)
			if test.wantErr != nil {
				require.ErrorIs(t, err, test.wantErr)
				require.Nil(t, signer)
				return
			}

			require.NoError(t, err)
			require.Equal(t, addr.String(), signer.Address().String())
		})
	}
}

func TestProviderSignerSign(t *testing.T) {
	kr, addr := newProviderSignerTestKeyring(t)
	payload := []byte("inventory snapshot payload")

	signer, err := NewProviderSigner(sdkclient.Context{
		FromAddress: addr,
		Keyring:     kr,
	}, &recordingTxClient{response: &sdk.TxResponse{}})
	require.NoError(t, err)

	signature, err := signer.Sign(context.Background(), payload)
	require.NoError(t, err)
	require.NotEmpty(t, signature)

	key, err := kr.KeyByAddress(addr)
	require.NoError(t, err)
	pubKey, err := key.GetPubKey()
	require.NoError(t, err)
	require.True(t, pubKey.VerifySignature(payload, signature))
}

func TestProviderSignerSignCanceledContext(t *testing.T) {
	kr, addr := newProviderSignerTestKeyring(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	signer, err := NewProviderSigner(sdkclient.Context{
		FromAddress: addr,
		Keyring:     kr,
	}, &recordingTxClient{response: &sdk.TxResponse{}})
	require.NoError(t, err)

	signature, err := signer.Sign(ctx, []byte("payload"))
	require.ErrorIs(t, err, context.Canceled)
	require.Nil(t, signature)
}

func TestProviderSignerBroadcast(t *testing.T) {
	kr, addr := newProviderSignerTestKeyring(t)
	tx := &recordingTxClient{response: &sdk.TxResponse{TxHash: "txhash"}}

	signer, err := NewProviderSigner(sdkclient.Context{
		FromAddress: addr,
		Keyring:     kr,
	}, tx)
	require.NoError(t, err)

	response, err := signer.Broadcast(context.Background())
	require.NoError(t, err)
	require.Equal(t, "txhash", response.TxHash)
	require.True(t, tx.called)
	require.Empty(t, tx.msgs)
	require.Len(t, tx.opts, 1)
}

func TestProviderSignerBroadcastErrors(t *testing.T) {
	kr, addr := newProviderSignerTestKeyring(t)
	broadcastErr := errors.New("broadcast failed")

	tests := []struct {
		name    string
		tx      *recordingTxClient
		wantErr error
	}{
		{
			name: "broadcast error",
			tx: &recordingTxClient{
				err: broadcastErr,
			},
			wantErr: broadcastErr,
		},
		{
			name: "unexpected response",
			tx: &recordingTxClient{
				response: struct{}{},
			},
			wantErr: errProviderSignerUnexpectedBroadcastResponse,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			signer, err := NewProviderSigner(sdkclient.Context{
				FromAddress: addr,
				Keyring:     kr,
			}, test.tx)
			require.NoError(t, err)

			response, err := signer.Broadcast(context.Background())
			require.ErrorIs(t, err, test.wantErr)
			require.Nil(t, response)
		})
	}
}
