package builder

import (
	"github.com/tendermint/tendermint/libs/log"

	manitypes "github.com/akash-network/akash-api/go/manifest/v2beta2"
	mtypes "github.com/akash-network/akash-api/go/node/market/v1beta3"

	crd "github.com/akash-network/provider/pkg/apis/akash.network/v2beta2"
)

type Manifest interface {
	builderBase
	Create() (*crd.Manifest, error)
	Update(obj *crd.Manifest) (*crd.Manifest, error)
	Name() string
}

// manifest composes the k8s akashv1.Manifest type from LeaseID and
// manifest.Group data.
type manifest struct {
	builder
	mns string
}

var _ Manifest = (*manifest)(nil)

func BuildManifest(log log.Logger, settings Settings, ns string, lid mtypes.LeaseID, group *manitypes.Group) Manifest {
	return &manifest{
		builder: builder{
			log:      log.With("module", "kube-builder"),
			settings: settings,
			lid:      lid,
			group:    group,
		},
		mns: ns,
	}
}

func (b *manifest) labels() map[string]string {
	return AppendLeaseLabels(b.lid, b.builder.labels())
}

func (b *manifest) Create() (*crd.Manifest, error) {

	obj, err := crd.NewManifest(b.mns, b.lid, b.group)

	if err != nil {
		return nil, err
	}
	obj.Labels = b.labels()
	return obj, nil
}

func (b *manifest) Update(obj *crd.Manifest) (*crd.Manifest, error) {
	m, err := crd.NewManifest(b.mns, b.lid, b.group)
	if err != nil {
		return nil, err
	}
	obj.Spec = m.Spec
	obj.Labels = b.labels()
	return obj, nil
}

func (b *manifest) NS() string {
	return b.mns
}
