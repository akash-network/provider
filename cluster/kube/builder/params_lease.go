package builder

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/tendermint/tendermint/libs/log"

	mtypes "github.com/akash-network/akash-api/go/node/market/v1beta3"

	crd "github.com/akash-network/provider/pkg/apis/akash.network/v2beta2"
)

type ParamsServices interface {
	builderBase
	Create() (*crd.LeaseParamsService, error)
	Update(obj *crd.LeaseParamsService) (*crd.LeaseParamsService, error)
	Name() string
}

type paramsServices struct {
	builder
	ns string
}

var _ ParamsServices = (*paramsServices)(nil)

func BuildParamsLeases(log log.Logger, settings Settings, ns string, lid mtypes.LeaseID, params crd.ParamsServices) ParamsServices {
	return &paramsServices{
		builder: builder{
			log:      log.With("module", "kube-builder"),
			settings: settings,
			lid:      lid,
			sparams:  params,
		},
		ns: ns,
	}
}

func (b *paramsServices) labels() map[string]string {
	return AppendLeaseLabels(b.lid, b.builder.labels())
}

func (b *paramsServices) Create() (*crd.LeaseParamsService, error) {
	obj := &crd.LeaseParamsService{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: b.ns,
			Name:      b.Name(),
			Labels:    b.labels(),
		},
		Spec: crd.LeaseParamsServiceSpec{
			Services: b.sparams,
		},
	}

	return obj, nil
}

func (b *paramsServices) Update(obj *crd.LeaseParamsService) (*crd.LeaseParamsService, error) {
	cobj := *obj
	cobj.Spec.Services = b.sparams

	return &cobj, nil
}

func (b *paramsServices) NS() string {
	return b.ns
}
