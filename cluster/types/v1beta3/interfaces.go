package v1beta3

import (
	mani "github.com/akash-network/akash-api/go/manifest/v2beta2"
)

type MGroup interface {
	ManifestGroup() mani.Group
}
