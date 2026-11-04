package release

import (
	"fmt"
	"strings"
)

// RawReference is an image or chart reference exactly as it appears in a
// Manifest: a string that has not been parsed and may or may not carry a
// digest.
type RawReference string

// Reference is a RawReference that has been parsed and is known to carry a
// digest. ParseReference is the only way to produce one.
type Reference struct {
	Repository string
	Tag        string
	Digest     string
}

type ReferenceErrorKind string

const (
	MissingDigest      ReferenceErrorKind = "MissingDigest"
	MalformedReference ReferenceErrorKind = "MalformedReference"
)

type ReferenceError struct {
	Kind  ReferenceErrorKind
	Input RawReference
}

func (e *ReferenceError) Error() string {
	return fmt.Sprintf("%s: %q", e.Kind, string(e.Input))
}

// ParseReference parses raw into a Reference or returns a *ReferenceError.
// raw may carry an optional leading "oci://" scheme and an optional ":tag",
// and must carry a trailing "@<digest>"; anything else is malformed, and a
// reference with no "@<digest>" at all is MissingDigest.
func ParseReference(raw RawReference) (Reference, error) {
	s := strings.TrimPrefix(string(raw), "oci://")

	at := strings.LastIndexByte(s, '@')
	if at < 0 {
		return Reference{}, &ReferenceError{Kind: MissingDigest, Input: raw}
	}
	repoAndTag, digest := s[:at], s[at+1:]
	if repoAndTag == "" || digest == "" || !strings.Contains(digest, ":") {
		return Reference{}, &ReferenceError{Kind: MalformedReference, Input: raw}
	}

	repo, tag := splitRepoTag(repoAndTag)
	if repo == "" {
		return Reference{}, &ReferenceError{Kind: MalformedReference, Input: raw}
	}
	return Reference{Repository: repo, Tag: tag, Digest: digest}, nil
}

// splitRepoTag splits "repo[:tag]" on the ":" in the final "/"-separated
// segment only, so a registry host of the form "host:port" never gets
// mistaken for a tag separator.
func splitRepoTag(repoAndTag string) (repo, tag string) {
	segmentStart := strings.LastIndexByte(repoAndTag, '/') + 1
	segment := repoAndTag[segmentStart:]
	colon := strings.IndexByte(segment, ':')
	if colon < 0 {
		return repoAndTag, ""
	}
	return repoAndTag[:segmentStart+colon], segment[colon+1:]
}
