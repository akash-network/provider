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
