package builder

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/BurntSushi/toml"
	mani "pkg.akt.dev/go/manifest/v2beta3"
)

// Confidential-compute image registry credential delivery.
//
// For confidential-compute (Kata/CoCo) workloads the container image is pulled
// *inside* the guest VM by image-rs, so the host-side imagePullSecrets the
// provider sets never reach it and private images fail to pull. To fix this
// without a Key Broker Service (KBS) or attestation, we deliver the tenant's
// registry credentials into the guest through Kata initdata: the (measured)
// `cc_init_data` annotation carries a `cdh.toml` that points image-rs at a
// local `auth.json`, plus the `auth.json` itself. The kata-agent materializes
// both files under INITDATA_PATH before the image is pulled.
//
// Because the credentials are released via a local file (not attestation), this
// works uniformly for SNP, TDX and GPU workloads; and because they are only
// referenced by (not folded into) the launch measurement, they do not change
// the guest image measurement.
const (
	// ccInitDataAnnotation is the Kata annotation carrying a gzip+base64 encoded
	// initdata TOML document into the confidential guest.
	ccInitDataAnnotation = "io.katacontainers.config.hypervisor.cc_init_data"

	// ccInitDataAlgorithm / ccInitDataVersion are the initdata document header
	// fields. The algorithm only selects which measurement register field the
	// guest reports the initdata digest in; it never covers the launch image.
	ccInitDataAlgorithm = "sha384"
	ccInitDataVersion   = "0.1.0"

	// ccInitDataCDHKey and ccInitDataAuthKey are the initdata `[data]` keys the
	// kata-agent recognizes and materializes to files of the same name under
	// INITDATA_PATH inside the guest.
	ccInitDataCDHKey  = "cdh.toml"
	ccInitDataAuthKey = "auth.json"

	// ccGuestAuthFilePath is the in-guest path the kata-agent writes the
	// `auth.json` entry to (INITDATA_PATH + "/" + ccInitDataAuthKey). It must
	// match the kata-agent's initdata handling.
	ccGuestAuthFilePath = "/run/confidential-containers/initdata/auth.json"
)

// ccCDHConfig is the CDH configuration delivered via initdata. It points
// image-rs at the local, initdata-provided credentials file so private images
// can be pulled without contacting a KBS.
var ccCDHConfig = fmt.Sprintf("[image]\nauthenticated_registry_credentials_uri = \"file://%s\"\n", ccGuestAuthFilePath)

// ccInitData mirrors the kata-types initdata document
// (algorithm, version, [data] map of file-name -> file-content).
type ccInitData struct {
	Algorithm string            `toml:"algorithm"`
	Version   string            `toml:"version"`
	Data      map[string]string `toml:"data"`
}

// ccImageRegistryAuthAnnotation builds the value for the Kata `cc_init_data`
// annotation that delivers the given registry credentials into a confidential
// guest. It returns an empty string (and no error) when creds is nil, i.e. when
// there is nothing to deliver.
func ccImageRegistryAuthAnnotation(creds *mani.ImageCredentials) (string, error) {
	if creds == nil {
		return "", nil
	}

	authJSON, err := containersAuthJSON(creds)
	if err != nil {
		return "", fmt.Errorf("encode registry auth: %w", err)
	}

	doc := ccInitData{
		Algorithm: ccInitDataAlgorithm,
		Version:   ccInitDataVersion,
		Data: map[string]string{
			ccInitDataCDHKey:  ccCDHConfig,
			ccInitDataAuthKey: string(authJSON),
		},
	}

	var tomlBuf bytes.Buffer
	if err := toml.NewEncoder(&tomlBuf).Encode(doc); err != nil {
		return "", fmt.Errorf("encode initdata toml: %w", err)
	}

	var gzBuf bytes.Buffer
	gz := gzip.NewWriter(&gzBuf)
	if _, err := gz.Write(tomlBuf.Bytes()); err != nil {
		return "", fmt.Errorf("gzip initdata: %w", err)
	}
	if err := gz.Close(); err != nil {
		return "", fmt.Errorf("gzip initdata: %w", err)
	}

	return base64.StdEncoding.EncodeToString(gzBuf.Bytes()), nil
}

// containersAuthJSON builds a containers-auth.json / .dockerconfigjson document
// from the given credentials. It reuses the same structure as the host-side
// image pull secret (see service_credentials.go) so both the host manifest
// resolve and the in-guest layer pull use identical auth material.
func containersAuthJSON(creds *mani.ImageCredentials) ([]byte, error) {
	username := strings.TrimSpace(creds.Username)
	password := strings.TrimSpace(creds.Password)

	doc := dockerCredentials{
		Auths: map[string]dockerCredentialsEntry{
			creds.Host: {
				Username: username,
				Password: password,
				Email:    strings.TrimSpace(creds.Email),
				Auth:     encodeAuth(username, password),
			},
		},
	}

	return json.Marshal(doc)
}
