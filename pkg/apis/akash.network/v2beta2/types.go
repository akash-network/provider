package v2beta2

import (
	"math"
	"strconv"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/pkg/errors"

	mani "github.com/akash-network/akash-api/go/manifest/v2beta2"
	mtypes "github.com/akash-network/akash-api/go/node/market/v1beta3"
	types "github.com/akash-network/akash-api/go/node/types/v1beta3"
)

type deployment struct {
	lid   mtypes.LeaseID
	group mani.Group
}

func (d deployment) LeaseID() mtypes.LeaseID {
	return d.lid
}

func (d deployment) ManifestGroup() mani.Group {
	return d.group
}

// LeaseID stores deployment, group sequence, order, provider and metadata
type LeaseID struct {
	Owner    string `json:"owner"`
	DSeq     string `json:"dseq"`
	GSeq     uint32 `json:"gseq"`
	OSeq     uint32 `json:"oseq"`
	Provider string `json:"provider"`
}

// FromCRD returns LeaseID from LeaseID details
func (id LeaseID) FromCRD() (mtypes.LeaseID, error) {
	owner, err := sdk.AccAddressFromBech32(id.Owner)
	if err != nil {
		return mtypes.LeaseID{}, err
	}

	provider, err := sdk.AccAddressFromBech32(id.Provider)
	if err != nil {
		return mtypes.LeaseID{}, err
	}

	dseq, err := strconv.ParseUint(id.DSeq, 10, 64)
	if err != nil {
		return mtypes.LeaseID{}, err
	}

	return mtypes.LeaseID{
		Owner:    owner.String(),
		DSeq:     dseq,
		GSeq:     id.GSeq,
		OSeq:     id.OSeq,
		Provider: provider.String(),
	}, nil
}

// LeaseIDFromAkash returns LeaseID instance from akash
func LeaseIDFromAkash(id mtypes.LeaseID) LeaseID {
	return LeaseID{
		Owner:    id.Owner,
		DSeq:     strconv.FormatUint(id.DSeq, 10),
		GSeq:     id.GSeq,
		OSeq:     id.OSeq,
		Provider: id.Provider,
	}
}

type ResourceUnitsStorage struct {
	Name string `json:"name"`
	Size string `json:"size"`
}

// ResourceUnits stores cpu, memory and storage details
type ResourceUnits struct {
	CPU     uint32                 `json:"cpu,omitempty"`
	Memory  string                 `json:"memory,omitempty"`
	Storage []ResourceUnitsStorage `json:"storage,omitempty"`
	GPU     uint32                 `json:"gpu,omitempty"`
}

func (ru ResourceUnits) fromCRD() (types.ResourceUnits, error) {
	memory, err := strconv.ParseUint(ru.Memory, 10, 64)
	if err != nil {
		return types.ResourceUnits{}, err
	}

	storage := make([]types.Storage, 0, len(ru.Storage))
	for _, st := range ru.Storage {
		size, err := strconv.ParseUint(st.Size, 10, 64)
		if err != nil {
			return types.ResourceUnits{}, err
		}

		storage = append(storage, types.Storage{
			Name:     st.Name,
			Quantity: types.NewResourceValue(size),
		})
	}

	return types.ResourceUnits{
		CPU: &types.CPU{
			Units: types.NewResourceValue(uint64(ru.CPU)),
		},
		GPU: &types.GPU{
			Units: types.NewResourceValue(uint64(ru.GPU)),
		},
		Memory: &types.Memory{
			Quantity: types.NewResourceValue(memory),
		},
		Storage: storage,
	}, nil
}

func resourceUnitsFromAkash(aru types.ResourceUnits) (ResourceUnits, error) {
	res := ResourceUnits{}
	if aru.CPU != nil {
		if aru.CPU.Units.Value() > math.MaxUint32 {
			return ResourceUnits{}, errors.New("k8s api: cpu units value overflows uint32")
		}
		res.CPU = uint32(aru.CPU.Units.Value())
	}
	if aru.Memory != nil {
		res.Memory = strconv.FormatUint(aru.Memory.Quantity.Value(), 10)
	}

	if aru.GPU != nil {
		// todo boundary check
		if aru.GPU.Units.Value() > math.MaxUint32 {
			return ResourceUnits{}, errors.New("k8s api: gpu units value overflows uint32")
		}
		res.GPU = uint32(aru.GPU.Units.Value())
	}

	res.Storage = make([]ResourceUnitsStorage, 0, len(aru.Storage))
	for _, storage := range aru.Storage {
		res.Storage = append(res.Storage, ResourceUnitsStorage{
			Name: storage.Name,
			Size: strconv.FormatUint(storage.Quantity.Value(), 10),
		})
	}

	return res, nil
}
