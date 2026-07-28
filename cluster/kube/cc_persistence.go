package kube

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"

	"github.com/akash-network/provider/cluster/kube/builder"
)

// Confidential-compute persistent storage: per-lease DEK provisioning.
//
// Each confidential persistent volume is encrypted (in-guest, by the kata-agent)
// with a per-lease disk encryption key (DEK) that the guest retrieves from the
// Key Broker Service (Trustee) after attestation. The provider registers that
// DEK in KBS at deploy time. The DEK is derived deterministically from a stable
// provider master key so it is identical across deploys/updates and across
// provider restarts — otherwise previously encrypted data could not be reopened.

// deriveDEK derives a per-volume DEK from the provider master key and the
// volume's KBS key URI (unique per lease+volume). Returned as lowercase hex so
// it contains no NUL/newline and is safe to hand to cryptsetup as a passphrase.
func deriveDEK(masterKey []byte, keyURI string) []byte {
	mac := hmac.New(sha256.New, masterKey)
	mac.Write([]byte(keyURI))
	sum := mac.Sum(nil)
	out := make([]byte, hex.EncodedLen(len(sum)))
	hex.Encode(out, sum)
	return out
}

// kbsResourcePath converts a kbs:///<repo>/<type>/<tag> URI into the KBS admin
// API path /kbs/v0/resource/<repo>/<type>/<tag>.
func kbsResourcePath(keyURI string) (string, error) {
	const scheme = "kbs:///"
	rest := strings.TrimPrefix(keyURI, scheme)
	if rest == keyURI || rest == "" {
		return "", fmt.Errorf("invalid KBS resource URI %q", keyURI)
	}
	return "/kbs/v0/resource/" + rest, nil
}

// kbsPutResource registers a resource (the DEK) in KBS via the admin API.
func kbsPutResource(ctx context.Context, kbsURL, keyURI string, data []byte) error {
	path, err := kbsResourcePath(keyURI)
	if err != nil {
		return err
	}
	url := strings.TrimRight(kbsURL, "/") + path

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/octet-stream")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("POST %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("KBS returned %s for %s", resp.Status, url)
	}
	return nil
}

// provisionCCPersistenceKeys registers the per-lease DEK for each confidential
// persistent volume of the workload in KBS. It is a no-op when the feature is
// disabled or the workload has no confidential persistent volumes. When such a
// volume exists but the feature cannot be honored (no KBS/master key) it returns
// an error so the deploy fails loud rather than silently losing data.
func (c *client) provisionCCPersistenceKeys(ctx context.Context, settings builder.Settings, workload *builder.Workload) error {
	refs := workload.CCPersistentVolumeKeyRefs()
	if len(refs) == 0 {
		return nil
	}

	if settings.CCPersistenceKBSURL == "" {
		return fmt.Errorf("confidential persistent storage requested but no KBS endpoint configured")
	}
	if len(settings.CCPersistenceMasterKey) == 0 {
		return fmt.Errorf("confidential persistent storage requested but no master key configured")
	}

	for _, ref := range refs {
		dek := deriveDEK(settings.CCPersistenceMasterKey, ref.KeyURI)
		if err := kbsPutResource(ctx, settings.CCPersistenceKBSURL, ref.KeyURI, dek); err != nil {
			return fmt.Errorf("provision DEK for volume %s: %w", ref.VolumeName, err)
		}
		c.log.Info("provisioned confidential persistent storage key", "volume", ref.VolumeName, "keyURI", ref.KeyURI)
	}

	return nil
}
