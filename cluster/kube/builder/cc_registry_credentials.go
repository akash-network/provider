package builder

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"

	mani "pkg.akt.dev/go/manifest/v2beta3"
)

const ccKBSResourceURIMaxBytes = 2048

var ccKBSResourceSegmentPattern = regexp.MustCompile(`^[A-Za-z0-9_-][A-Za-z0-9._-]*$`)

func (b *Workload) confidentialRegistryCredentialsURI() (string, error) {
	service := &b.group.Services[b.serviceIdx]
	credentials := service.Credentials
	if credentials == nil {
		return "", nil
	}

	uri := credentials.URI
	hasInline := strings.TrimSpace(credentials.Host) != "" ||
		strings.TrimSpace(credentials.Email) != "" ||
		strings.TrimSpace(credentials.Username) != "" ||
		strings.TrimSpace(credentials.Password) != ""
	if uri != "" && hasInline {
		return "", fmt.Errorf("registry credentials cannot mix inline fields with a KBS resource URI")
	}

	isConfidential := b.sparams[b.serviceIdx] != nil &&
		b.sparams[b.serviceIdx].RuntimeClass.Is(WithCC())
	if !isConfidential {
		if uri != "" {
			return "", fmt.Errorf("registry credential KBS resource URI requires a confidential runtime")
		}
		return "", nil
	}

	if uri == "" {
		return "", fmt.Errorf("confidential registry authentication requires a KBS resource URI")
	}
	if err := validateCCKBSResourceURI(uri); err != nil {
		return "", err
	}

	return uri, nil
}

func validateCCKBSResourceURI(value string) error {
	if value == "" || len(value) > ccKBSResourceURIMaxBytes || value != strings.TrimSpace(value) {
		return fmt.Errorf("registry credential URI must be a bounded canonical kbs:///repo/type/tag URI")
	}

	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "kbs" || parsed.Host != "" || parsed.User != nil ||
		parsed.Opaque != "" || parsed.RawPath != "" || parsed.RawQuery != "" || parsed.ForceQuery ||
		parsed.Fragment != "" {
		return fmt.Errorf("registry credential URI must be a canonical kbs:///repo/type/tag URI")
	}

	parts := strings.Split(strings.TrimPrefix(parsed.Path, "/"), "/")
	if len(parts) != 3 {
		return fmt.Errorf("registry credential URI must be a canonical kbs:///repo/type/tag URI")
	}
	for _, part := range parts {
		if !ccKBSResourceSegmentPattern.MatchString(part) {
			return fmt.Errorf("registry credential URI must be a canonical kbs:///repo/type/tag URI")
		}
	}
	if value != "kbs:///"+strings.Join(parts, "/") {
		return fmt.Errorf("registry credential URI must be a canonical kbs:///repo/type/tag URI")
	}

	return nil
}

// ImagePullCredentials returns credentials that may be materialized as a host
// Kubernetes Secret. Confidential credential references are consumed only by
// CDH inside the guest and are never returned here.
func (b *Workload) ImagePullCredentials() *mani.ImageCredentials {
	if b.registryCredentialsURI != "" {
		return nil
	}
	return b.group.Services[b.serviceIdx].Credentials
}
