package cluster

import (
	"time"
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
	GatewayName                     string
	GatewayNamespace                string
	GatewayProvider                 string
	ReclamationRPCTimeout           time.Duration
	ReclamationCloseRetryInterval   time.Duration
	ClusterSettings                 map[interface{}]interface{}
}

func NewDefaultConfig() Config {
	return Config{
		InventoryResourcePollPeriod:     time.Second * 5,
		InventoryResourceDebugFrequency: 10,
		MonitorMaxRetries:               40,
		MonitorRetryPeriod:              time.Second * 4, // nolint revive
		MonitorRetryPeriodJitter:        time.Second * 15,
		GatewayName:                     "akash-gateway",
		GatewayNamespace:                "akash-gateway",
		GatewayProvider:                 "auto",
		ReclamationRPCTimeout:           30 * time.Second,
		ReclamationCloseRetryInterval:   time.Minute,
	}
}
