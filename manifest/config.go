package manifest

import "time"

const DefaultBroadcastTimeout = 12 * time.Second

type ServiceConfig struct {
	HTTPServicesRequireAtLeastOneHost bool
	ManifestTimeout                   time.Duration
	BroadcastTimeout                  time.Duration
	RPCQueryTimeout                   time.Duration
	CachedResultMaxAge                time.Duration
}
