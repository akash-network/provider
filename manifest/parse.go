package manifest

import (
	"encoding/base32"
	"strings"

	maniv2beta1 "github.com/akash-network/akash-api/go/manifest/v2beta2"
	mtypes "github.com/akash-network/akash-api/go/node/market/v1beta4"
	"github.com/google/uuid"
)

func AllHostnamesOfManifestGroup(mgroup maniv2beta1.Group) []string {
	allHostnames := make([]string, 0)
	for _, service := range mgroup.Services {
		for _, expose := range service.Expose {
			allHostnames = append(allHostnames, expose.Hosts...)
		}
	}

	return allHostnames
}

func IngressHost(lid mtypes.LeaseID, svcName string) string {
	uid := uuid.NewSHA1(uuid.NameSpaceDNS, []byte(lid.String()+svcName))
	// MarshalBinary always returns nil
	data, _ := uid.MarshalBinary()
	return strings.ToLower(base32.HexEncoding.WithPadding(base32.NoPadding).EncodeToString(data))
}
