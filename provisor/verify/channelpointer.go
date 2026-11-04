package verify

// ChannelPointer is the document a channel's signed envelope carries: where
// the current manifest lives, and the digest it must hash to.
type ChannelPointer struct {
	ManifestURL    string `json:"manifestURL"`
	ManifestDigest string `json:"manifestDigest"`
}
