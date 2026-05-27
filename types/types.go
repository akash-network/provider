package types

import (
	"context"

	cryptotypes "github.com/cosmos/cosmos-sdk/crypto/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
)

const (
	PubSubTopicLeasesStatus    = "leases-status"
	PubSubTopicProviderStatus  = "provider-status"
	PubSubTopicClusterStatus   = "cluster-status"
	PubSubTopicBidengineStatus = "bidengine-status"
	PubSubTopicManifestStatus  = "manifest-status"
	PubSubTopicInventoryStatus = "inventory-status"
)

type AccountQuerier interface {
	GetAccountPublicKey(context.Context, sdk.Address) (cryptotypes.PubKey, error)
}

// ProviderSigner is the narrow signing and broadcast surface needed by AEP-86 provider components.
type ProviderSigner interface {
	Address() sdk.AccAddress
	Sign(context.Context, []byte) ([]byte, error)
	Broadcast(context.Context, ...sdk.Msg) (*sdk.TxResponse, error)
}
