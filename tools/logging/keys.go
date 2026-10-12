package logging

// Canonical log keys. All keys are snake_case; see _docs/logging.md for the full dictionary.
const (
	// KeyModule names the component emitting the entry. Set it only via With.
	KeyModule = "module"
	// KeyErr carries an error value.
	KeyErr = "err"

	KeyLeaseID      = "lid"
	KeyOrderID      = "oid"
	KeyDeploymentID = "did"
	KeyBidID        = "bid"
	KeyGroupID      = "gid"

	KeyNamespace  = "ns"
	KeyOwner      = "owner"
	KeyProvider   = "provider"
	KeyService    = "service"
	KeyHostname   = "hostname"
	KeyCount      = "count"
	KeyGroupName  = "group_name"
	KeyListenAddr = "listen_addr"
)
