package migrate

import (
	"fmt"

	dtypes "github.com/akash-network/akash-api/go/node/deployment/v1beta3"

	"github.com/akash-network/provider/pkg/apis/akash.network/v2beta1"
	"github.com/akash-network/provider/pkg/apis/akash.network/v2beta2"
)

func ManifestSpecFromV2beta1(res dtypes.ResourceUnits, from v2beta1.ManifestSpec) (v2beta2.ManifestSpec, error) {
	group, err := ManifestGroupFromV2beta1(res, from.Group)
	if err != nil {
		return v2beta2.ManifestSpec{}, err
	}

	to := v2beta2.ManifestSpec{
		LeaseID: LeaseIDFromV2beta1(from.LeaseID),
		Group:   group,
	}

	return to, nil
}

func LeaseIDFromV2beta1(from v2beta1.LeaseID) v2beta2.LeaseID {
	return v2beta2.LeaseID{
		Owner:    from.Owner,
		DSeq:     from.DSeq,
		GSeq:     from.GSeq,
		OSeq:     from.OSeq,
		Provider: from.Provider,
	}
}

func ManifestGroupFromV2beta1(res dtypes.ResourceUnits, from v2beta1.ManifestGroup) (v2beta2.ManifestGroup, error) {
	svcs, err := ManifestServicesFromV2beta1(res, from.Services)
	if err != nil {
		return v2beta2.ManifestGroup{}, err
	}

	to := v2beta2.ManifestGroup{
		Name:     from.Name,
		Services: svcs,
	}

	return to, nil
}

func ManifestServicesFromV2beta1(res dtypes.ResourceUnits, from []v2beta1.ManifestService) ([]v2beta2.ManifestService, error) {
	to := make([]v2beta2.ManifestService, 0, len(from))

	for _, svc := range from {
		nsvc, err := ManifestServiceFromV2beta1(res, svc)
		if err != nil {
			return nil, err
		}
		to = append(to, nsvc)
	}

	return to, nil
}

func ManifestServiceFromV2beta1(res dtypes.ResourceUnits, from v2beta1.ManifestService) (v2beta2.ManifestService, error) {
	resources, err := ManifestResourcesFromV2beta1(res, from.Resources, from.Count)
	if err != nil {
		return v2beta2.ManifestService{}, err
	}

	return v2beta2.ManifestService{
		Name:            from.Name,
		Image:           from.Image,
		Command:         from.Command,
		Args:            from.Args,
		Env:             from.Env,
		Resources:       resources,
		Count:           from.Count,
		Expose:          ManifestServiceExposeFromV2beta1(from.Expose),
		Params:          ManifestServiceParamsFromV2beta1(from.Params),
		SchedulerParams: nil, // brought up in v2beta2
	}, nil
}

func ManifestServiceExposeFromV2beta1(from []v2beta1.ManifestServiceExpose) []v2beta2.ManifestServiceExpose {
	to := make([]v2beta2.ManifestServiceExpose, 0, len(from))

	for _, oldExpose := range from {
		expose := v2beta2.ManifestServiceExpose{
			Port:         oldExpose.Port,
			ExternalPort: oldExpose.ExternalPort,
			Proto:        oldExpose.Proto,
			Service:      oldExpose.Service,
			Global:       oldExpose.Global,
			Hosts:        oldExpose.Hosts,
			HTTPOptions: v2beta2.ManifestServiceExposeHTTPOptions{
				MaxBodySize: oldExpose.HTTPOptions.MaxBodySize,
				ReadTimeout: oldExpose.HTTPOptions.ReadTimeout,
				SendTimeout: oldExpose.HTTPOptions.SendTimeout,
				NextTries:   oldExpose.HTTPOptions.NextTries,
				NextTimeout: oldExpose.HTTPOptions.NextTimeout,
				NextCases:   oldExpose.HTTPOptions.NextCases,
			},
		}

		to = append(to, expose)
	}

	return to
}

func ManifestResourcesFromV2beta1(res dtypes.ResourceUnits, from v2beta1.ResourceUnits, count uint32) (v2beta2.Resources, error) {
	var resID uint32

	svcRes, err := from.ToAkash()
	if err != nil {
		return v2beta2.Resources{}, err
	}

	for idx, units := range res {
		if units.CPU.Units.Value() == svcRes.CPU.Units.Value() &&
			units.Memory.Quantity.Value() == svcRes.Memory.Quantity.Value() {

			matched := len(svcRes.Storage)

			for _, vol := range svcRes.Storage {
				for _, v := range units.Storage {
					if vol.Quantity.Value() == v.Quantity.Value() {
						matched--
					}
				}
			}

			if matched > 0 {
				continue
			}

			if res[idx].Count >= count {
				res[idx].Count -= count

				resID = units.ID

				break
			}
		}
	}

	if resID == 0 {
		return v2beta2.Resources{}, fmt.Errorf("over-utilized resource group") // nolint goerr113
	}

	to := v2beta2.Resources{
		ID:      resID,
		CPU:     from.CPU,
		GPU:     0, // GPU has been introduced in v2beta2
		Memory:  from.Memory,
		Storage: make([]v2beta2.ResourcesStorage, 0, len(from.Storage)),
	}

	for _, storage := range from.Storage {
		to.Storage = append(to.Storage, v2beta2.ResourcesStorage{
			Name: storage.Name,
			Size: storage.Size,
		})
	}

	return to, nil
}

func ManifestServiceParamsFromV2beta1(from *v2beta1.ManifestServiceParams) *v2beta2.ManifestServiceParams {
	if from == nil {
		return nil
	}

	to := &v2beta2.ManifestServiceParams{
		Storage: make([]v2beta2.ManifestStorageParams, len(from.Storage)),
	}

	for _, storage := range from.Storage {
		to.Storage = append(to.Storage, v2beta2.ManifestStorageParams{
			Name:     storage.Name,
			Mount:    storage.Mount,
			ReadOnly: storage.ReadOnly,
		})
	}

	return to
}
