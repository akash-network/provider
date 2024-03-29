package util

import (
	"crypto/sha256"
	"encoding/base32"
	"fmt"
	"io"
	"regexp"
	"strings"

	mtypes "github.com/akash-network/akash-api/go/node/market/v1beta4"
)

var allowedIPEndpointNameRegex = regexp.MustCompile(`^[a-z\d\-]+$`)

func MakeIPSharingKey(lID mtypes.LeaseID, endpointName string) string {
	effectiveName := endpointName
	if !allowedIPEndpointNameRegex.MatchString(endpointName) {
		h := sha256.New()
		_, err := io.WriteString(h, endpointName)
		if err != nil {
			panic(err)

		}
		effectiveName = strings.ToLower(base32.HexEncoding.WithPadding(base32.NoPadding).EncodeToString(h.Sum(nil)[0:15]))
	}
	return fmt.Sprintf("%s-ip-%s", lID.GetOwner(), effectiveName)
}
