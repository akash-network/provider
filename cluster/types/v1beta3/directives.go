package v1beta3

import (
	manifest "github.com/akash-network/akash-api/go/manifest/v2beta2"
	mtypes "github.com/akash-network/akash-api/go/node/market/v1beta3"
)

type ConnectHostnameToDeploymentDirective struct {
	Hostname    string
	LeaseID     mtypes.LeaseID
	ServiceName string
	ServicePort int32
	ReadTimeout uint32
	SendTimeout uint32
	NextTimeout uint32
	MaxBodySize uint32
	NextTries   uint32
	NextCases   []string
}

type ClusterIPPassthroughDirective struct {
	LeaseID      mtypes.LeaseID
	ServiceName  string
	Port         uint32
	ExternalPort uint32
	SharingKey   string
	Protocol     manifest.ServiceProtocol
}
