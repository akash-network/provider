package release

import (
	"errors"
	"testing"
)

func TestParseReference(t *testing.T) {
	cases := []struct {
		name     string
		input    RawReference
		want     Reference
		wantErr  bool
		wantKind ReferenceErrorKind
	}{
		{
			name:  "digest and tag",
			input: "ghcr.io/akash-network/provider:0.17.0@sha256:5d3c4f00deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef",
			want: Reference{
				Repository: "ghcr.io/akash-network/provider",
				Tag:        "0.17.0",
				Digest:     "sha256:5d3c4f00deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef",
			},
		},
		{
			name:  "oci scheme, digest only",
			input: "oci://ghcr.io/akash-network/charts/akash-provider@sha256:88de0000deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef",
			want: Reference{
				Repository: "ghcr.io/akash-network/charts/akash-provider",
				Tag:        "",
				Digest:     "sha256:88de0000deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef",
			},
		},
		{
			name:     "no digest at all",
			input:    "ghcr.io/akash-network/provider:0.17.0",
			wantErr:  true,
			wantKind: MissingDigest,
		},
		{
			name:  "digest, no tag",
			input: "ghcr.io/akash-network/provider@sha256:5d3c4f00deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef",
			want: Reference{
				Repository: "ghcr.io/akash-network/provider",
				Tag:        "",
				Digest:     "sha256:5d3c4f00deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef",
			},
		},
		{
			name:     "trailing empty digest",
			input:    "ghcr.io/akash-network/provider@",
			wantErr:  true,
			wantKind: MalformedReference,
		},
		{
			name:  "registry host with port, no tag",
			input: "localhost:5000/library/foo@sha256:cafebabedeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef",
			want: Reference{
				Repository: "localhost:5000/library/foo",
				Tag:        "",
				Digest:     "sha256:cafebabedeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef",
			},
		},
		{
			name:  "registry host with port, with tag",
			input: "localhost:5000/library/foo:v1@sha256:cafebabedeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef",
			want: Reference{
				Repository: "localhost:5000/library/foo",
				Tag:        "v1",
				Digest:     "sha256:cafebabedeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseReference(tc.input)

			if tc.wantErr {
				if err == nil {
					t.Fatalf("ParseReference(%q) = %+v, nil, want error", tc.input, got)
				}
				var refErr *ReferenceError
				if !errors.As(err, &refErr) {
					t.Fatalf("ParseReference(%q) error = %v, want *ReferenceError", tc.input, err)
				}
				if refErr.Kind != tc.wantKind {
					t.Fatalf("ParseReference(%q) error kind = %s, want %s", tc.input, refErr.Kind, tc.wantKind)
				}
				return
			}

			if err != nil {
				t.Fatalf("ParseReference(%q) unexpected error: %v", tc.input, err)
			}
			if got != tc.want {
				t.Fatalf("ParseReference(%q) = %+v, want %+v", tc.input, got, tc.want)
			}
		})
	}
}
