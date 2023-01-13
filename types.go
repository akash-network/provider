package provider

import (
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/akash-network/provider/bidengine"
	ctypes "github.com/akash-network/provider/cluster/types/v1beta2"
	"github.com/akash-network/provider/manifest"
)

// Status is the data structure that stores Cluster, Bidengine and Manifest details.
type Status struct {
	Cluster               *ctypes.Status    `json:"cluster"`
	Bidengine             *bidengine.Status `json:"bidengine"`
	Manifest              *manifest.Status  `json:"manifest"`
	ClusterPublicHostname string            `json:"cluster_public_hostname,omitempty"`
}

type ValidateGroupSpecResult struct {
	MinBidPrice sdk.DecCoin `json:"min_bid_price"`
}
