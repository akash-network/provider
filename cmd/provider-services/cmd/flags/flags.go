package flags

import (
	"fmt"
	"regexp"
	"strconv"
)

const (
	FlagK8sManifestNS      = "k8s-manifest-ns"
	FlagListenAddress      = "listen"
	FlagPruneInterval      = "prune-interval"
	FlagWebRefreshInterval = "web-refresh-interval"
	FlagRetryDelay         = "retry-delay"
	FlagKubeConfig         = "kubeconfig"
	FlagProxyBufferSize    = "proxy-buffer-size"
)

// nginxSizeRe matches an nginx size value: a positive integer with an optional
// k/K (kilobytes) or m/M (megabytes) suffix, e.g. 512, 16k, 1m. That is the grammar
// nginx accepts for proxy_buffer_size, so "16kb" and "16g" are invalid.
var nginxSizeRe = regexp.MustCompile(`^([0-9]+)([kKmM]?)$`)

// ValidateProxyBufferSize returns an error unless the value is empty (unset) or a
// valid positive nginx size. The value is inserted verbatim into a per-route
// SnippetsFilter, and NGF marks a structurally valid filter Accepted=True without
// parsing its nginx contents, so a bad value such as "16kb" would only surface
// later at nginx -t and block the data plane from applying the config. Rejecting
// it at startup fails fast instead.
func ValidateProxyBufferSize(value string) error {
	if value == "" {
		return nil
	}
	m := nginxSizeRe.FindStringSubmatch(value)
	if m == nil {
		return fmt.Errorf("invalid %s %q: expected a positive nginx size such as 16k, 512k, or 1m", FlagProxyBufferSize, value)
	}
	if n, err := strconv.ParseUint(m[1], 10, 64); err != nil || n == 0 {
		return fmt.Errorf("invalid %s %q: size must be greater than zero", FlagProxyBufferSize, value)
	}
	return nil
}
