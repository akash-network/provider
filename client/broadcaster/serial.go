package broadcaster

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/boz/go-lifecycle"
	sdkclient "github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/client/tx"
	"github.com/cosmos/cosmos-sdk/crypto/keyring"
	sdk "github.com/cosmos/cosmos-sdk/types"
	authtx "github.com/cosmos/cosmos-sdk/x/auth/tx"
	"github.com/tendermint/tendermint/libs/log"
	ttypes "github.com/tendermint/tendermint/types"

	abroadcaster "github.com/ovrclk/akash/client/broadcaster"
	"github.com/ovrclk/akash/sdkutil"
)

const (
	syncDuration    = 10 * time.Second
	errCodeMismatch = 32

	broadcastBlockRetryPeriod = time.Second
)

var (
	ErrNotRunning = errors.New("not running")
	// sadface.

	// Only way to detect the timeout error.
	// https://github.com/tendermint/tendermint/blob/46e06c97320bc61c4d98d3018f59d47ec69863c9/rpc/core/mempool.go#L124
	timeoutErrorMessage = "timed out waiting for tx to be included in a block"

	// Only way to check for tx not found error.
	// https://github.com/tendermint/tendermint/blob/46e06c97320bc61c4d98d3018f59d47ec69863c9/rpc/core/tx.go#L31-L33
	notFoundErrorMessageSuffix = ") not found"

	// errors are of the form:
	// "account sequence mismatch, expected 25, got 27: incorrect account sequence"
	recoverRegexp = regexp.MustCompile(`^account sequence mismatch, expected (\d+), got (\d+):`)
)

type SerialClient interface {
	abroadcaster.Client
	Close()
}

type serialBroadcaster struct {
	cctx             sdkclient.Context
	txf              tx.Factory
	info             keyring.Info
	broadcastTimeout time.Duration
	broadcastch      chan broadcastRequest
	lc               lifecycle.Lifecycle
	log              log.Logger
}

func NewSerialClient(log log.Logger, cctx sdkclient.Context, timeout time.Duration, txf tx.Factory, info keyring.Info) (SerialClient, error) {
	// populate account number, current sequence number
	poptxf, err := sdkutil.PrepareFactory(cctx, txf)
	if err != nil {
		return nil, err
	}

	client := &serialBroadcaster{
		cctx:             cctx,
		txf:              poptxf,
		info:             info,
		broadcastTimeout: timeout,
		lc:               lifecycle.New(),
		broadcastch:      make(chan broadcastRequest),
		log:              log.With("cmp", "client/broadcaster"),
	}

	go client.run()

	return client, nil
}

func (c *serialBroadcaster) Close() {
	c.lc.Shutdown(nil)
}

type broadcastRequest struct {
	responsech chan<- error
	msgs       []sdk.Msg
}

func (c *serialBroadcaster) Broadcast(ctx context.Context, msgs ...sdk.Msg) error {
	responsech := make(chan error, 1)
	request := broadcastRequest{
		responsech: responsech,
		msgs:       msgs,
	}

	select {

	// request received, return response
	case c.broadcastch <- request:
		return <-responsech

	// caller context cancelled, return error
	case <-ctx.Done():
		return ctx.Err()

	// loop shutting down, return error
	case <-c.lc.ShuttingDown():
		return ErrNotRunning
	}
}

func (c *serialBroadcaster) run() {
	defer c.lc.ShutdownCompleted()

	var (
		txf    = c.txf
		synch  = make(chan uint64)
		donech = make(chan struct{})
	)

	go func() {
		defer close(donech)
		c.syncLoop(synch)
	}()

	defer func() { <-donech }()

loop:
	for {
		select {
		case err := <-c.lc.ShutdownRequest():
			c.lc.ShutdownInitiated(err)
			break loop
		case req := <-c.broadcastch:
			// broadcast the message
			var err error
			txf, err = c.doBroadcast(txf, false, req.msgs...)

			// send response
			req.responsech <- err

		case seqno := <-synch:
			c.log.Info("syncing sequence", "local", txf.Sequence(), "remote", seqno)

			// fast-forward current sequence if necessary
			if seqno > txf.Sequence() {
				txf = txf.WithSequence(seqno)
			}
		}
	}
}

func (c *serialBroadcaster) syncLoop(ch chan<- uint64) {
	// TODO: add jitter, force update on "sequence mismatch"-type errors.
	ticker := time.NewTicker(syncDuration)
	defer ticker.Stop()

loop:
	for {
		select {
		case <-c.lc.ShuttingDown():
			return
		case <-ticker.C:
			// query sequence number
			_, seq, err := c.cctx.AccountRetriever.GetAccountNumberSequence(c.cctx, c.info.GetAddress())

			if err != nil {
				c.log.Error("error requesting account", "err", err)
				continue loop
			}

			// send to main loop if no error
			select {
			case ch <- seq:
			case <-c.lc.ShuttingDown():
			}
		}
	}
}

func (c *serialBroadcaster) doBroadcast(txf tx.Factory, retried bool, msgs ...sdk.Msg) (tx.Factory, error) {
	txf, err := sdkutil.AdjustGas(c.cctx, txf, msgs...)
	if err != nil {
		return txf, err
	}

	response, err := doBroadcast(c.cctx, txf, c.broadcastTimeout, c.info.GetName(), msgs...)
	c.log.Info("broadcast response", "response", response, "err", err)
	if err != nil {
		return txf, err
	}

	// if no error, increment sequence.
	if response.Code == 0 {
		return txf.WithSequence(txf.Sequence() + 1), nil
	}

	// if not mismatch error, don't increment sequence and return
	if response.Code != errCodeMismatch {
		return txf, fmt.Errorf("%w: response code %d - (%#v)", abroadcaster.ErrBroadcastTx, response.Code, response)
	}

	// if we're retrying a parsed sequence (see below), don't try to fix it again.
	if retried {
		return txf, fmt.Errorf("%w: retried response code %d - (%#v)", abroadcaster.ErrBroadcastTx, response.Code, response)
	}

	// attempt to parse correct next sequence
	nextseq, ok := parseNextSequence(txf.Sequence(), response.RawLog)

	if !ok {
		return txf, fmt.Errorf("%w: response code %d - (%#v)", abroadcaster.ErrBroadcastTx, response.Code, response)
	}

	txf = txf.WithSequence(nextseq)

	// try again
	return c.doBroadcast(txf, true, msgs...)
}

func parseNextSequence(current uint64, message string) (uint64, bool) {
	// errors are of the form:
	// "account sequence mismatch, expected 25, got 27: incorrect account sequence"

	matches := recoverRegexp.FindStringSubmatch(message)

	if len(matches) != 3 {
		return 0, false
	}

	if len(matches[1]) == 0 || len(matches[2]) == 0 {
		return 0, false
	}

	expected, err := strconv.ParseUint(matches[1], 10, 64)
	if err != nil || expected == 0 {
		return 0, false
	}

	received, err := strconv.ParseUint(matches[2], 10, 64)
	if err != nil || received == 0 {
		return 0, false
	}

	if received != current {
		// XXX not sure wtf todo.
		return expected + 1, true
	}

	return expected, true
}

func doBroadcast(cctx sdkclient.Context, txf tx.Factory, timeout time.Duration, keyName string, msgs ...sdk.Msg) (*sdk.TxResponse, error) {
	txn, err := tx.BuildUnsignedTx(txf, msgs...)
	if err != nil {
		return nil, err
	}

	txn.SetFeeGranter(cctx.GetFeeGranterAddress())
	err = tx.Sign(txf, keyName, txn, true)
	if err != nil {
		return nil, err
	}

	bytes, err := cctx.TxConfig.TxEncoder()(txn.GetTx())
	if err != nil {
		return nil, err
	}

	txb := ttypes.Tx(bytes)
	hash := hex.EncodeToString(txb.Hash())

	// broadcast-mode=block
	// submit with mode commit/block
	cres, err := cctx.BroadcastTxCommit(txb)
	if err == nil {
		// good job
		return cres, nil
	} else if !strings.HasSuffix(err.Error(), timeoutErrorMessage) {
		return cres, err
	}

	// timeout error, continue on to retry

	// loop
	lctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	for lctx.Err() == nil {
		// wait up to one second
		select {
		case <-lctx.Done():
			return cres, err
		case <-time.After(broadcastBlockRetryPeriod):
		}

		// check transaction
		// https://github.com/cosmos/cosmos-sdk/pull/8734
		res, err := authtx.QueryTx(cctx, hash)
		if err == nil {
			return res, nil
		}

		// if it's not a "not found" error, return
		if !strings.HasSuffix(err.Error(), notFoundErrorMessageSuffix) {
			return res, err
		}
	}

	return cres, lctx.Err()
}
