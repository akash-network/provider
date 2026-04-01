package cluster

import (
	"time"

	"github.com/akash-network/provider/cluster/kube/builder"
)

type Config struct {
	InventoryResourcePollPeriod     time.Duration
	InventoryResourceDebugFrequency uint
	InventoryExternalPortQuantity   uint
	CPUCommitLevel                  float64
	GPUCommitLevel                  float64
	MemoryCommitLevel               float64
	StorageCommitLevel              float64
	BlockedHostnames                []string
	DeploymentIngressStaticHosts    bool
	DeploymentIngressDomain         string
	MonitorMaxRetries               uint
	MonitorRetryPeriod              time.Duration
	MonitorRetryPeriodJitter        time.Duration
	MonitorHealthcheckPeriod        time.Duration
	MonitorHealthcheckPeriodJitter  time.Duration
	IngressMode                     builder.IngressMode
	GatewayName                     string
	GatewayNamespace                string
	GatewayImplementation           string
	ClusterSettings                 map[interface{}]interface{}
}

func NewDefaultConfig() Config {
	return Config{
		InventoryResourcePollPeriod:     time.Second * 5,
		InventoryResourceDebugFrequency: 10,
		MonitorMaxRetries:               40,
		MonitorRetryPeriod:              time.Second * 4, // nolint revive
		MonitorRetryPeriodJitter:        time.Second * 15,
		MonitorHealthcheckPeriod:        time.Second * 10, // nolint revive
		MonitorHealthcheckPeriodJitter:  time.Second * 5,
		IngressMode:                     builder.IngressModeIngress,
		GatewayName:                     "akash-gateway",
		GatewayNamespace:                "akash-gateway",
		GatewayImplementation:           "nginx",
	}
}
