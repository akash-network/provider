package flags

const (
	FlagK8sManifestNS      = "k8s-manifest-ns"
	FlagListenAddress      = "listen"
	FlagPruneInterval      = "prune-interval"
	FlagWebRefreshInterval = "web-refresh-interval"
	FlagRetryDelay         = "retry-delay"
	FlagKubeConfig         = "kubeconfig"
)

// EnvPrefixes returns the environment variable prefixes of provider-services options in order
// of precedence: AP_LOG_LEVEL wins over AKASH_LOG_LEVEL.
func EnvPrefixes() []string {
	return []string{"AP", "AKASH"}
}
