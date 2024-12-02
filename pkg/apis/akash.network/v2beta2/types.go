package v2beta2

import (
	"fmt"
	"math"
	"strconv"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/pkg/errors"

	mani "github.com/akash-network/akash-api/go/manifest/v2beta2"
	mtypes "github.com/akash-network/akash-api/go/node/market/v1beta4"
	types "github.com/akash-network/akash-api/go/node/types/v1beta3"
)

var (
	ErrInvalidArgs = fmt.Errorf("crd/%s: invalid args", crdVersion)
)

type Status struct {
	State   string `json:"state,omitempty"`
	Message string `json:"message,omitempty"`
}

type deployment struct {
	lid     mtypes.LeaseID
	group   mani.Group
	cparams interface{}
}

func (d deployment) LeaseID() mtypes.LeaseID {
	return d.lid
}

func (d deployment) ManifestGroup() *mani.Group {
	return &d.group
}

func (d deployment) ClusterParams() interface{} {
	return d.cparams
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

type ResourceCPU struct {
	Units      uint32           `json:"units"`
	Attributes types.Attributes `json:"attributes,omitempty"`
}

type ResourceGPU struct {
	Units      uint32           `json:"units"`
	Attributes types.Attributes `json:"attributes,omitempty"`
}

type ResourceMemory struct {
	Size       string           `json:"size"`
	Attributes types.Attributes `json:"attributes,omitempty"`
}

type ResourceVolume struct {
	Name       string           `json:"name"`
	Size       string           `json:"size"`
	Attributes types.Attributes `json:"attributes,omitempty"`
}

type ResourceStorage []ResourceVolume

func (ru ResourceCPU) ToAkash() *types.CPU {
	return &types.CPU{
		Units:      types.NewResourceValue(uint64(ru.Units)),
		Attributes: ru.Attributes.Dup(),
	}
}

func (ru ResourceGPU) ToAkash() *types.GPU {
	return &types.GPU{
		Units:      types.NewResourceValue(uint64(ru.Units)),
		Attributes: ru.Attributes.Dup(),
	}
}

func (ru ResourceMemory) ToAkash() *types.Memory {
	size, _ := strconv.ParseUint(ru.Size, 10, 64)

	return &types.Memory{
		Quantity:   types.NewResourceValue(size),
		Attributes: ru.Attributes.Dup(),
	}
}

func (ru ResourceVolume) ToAkash() types.Storage {
	size, _ := strconv.ParseUint(ru.Size, 10, 64)

	return types.Storage{
		Name:       ru.Name,
		Quantity:   types.NewResourceValue(size),
		Attributes: ru.Attributes.Dup(),
	}
}

func (ru ResourceStorage) ToAkash() types.Volumes {
	res := make(types.Volumes, 0, len(ru))

	for _, vol := range ru {
		res = append(res, vol.ToAkash())
	}

	return res
}

// Resources stores cpu, memory and storage details
type Resources struct {
	ID      uint32          `json:"id"`
	CPU     ResourceCPU     `json:"cpu"`
	GPU     ResourceGPU     `json:"gpu"`
	Memory  ResourceMemory  `json:"memory"`
	Storage ResourceStorage `json:"storage,omitempty"`
}

func (ru *Resources) ToAkash() (types.Resources, error) {
	return types.Resources{
		ID:      ru.ID,
		CPU:     ru.CPU.ToAkash(),
		GPU:     ru.GPU.ToAkash(),
		Memory:  ru.Memory.ToAkash(),
		Storage: ru.Storage.ToAkash(),
	}, nil
}

func resourcesFromAkash(aru types.Resources) (Resources, error) {
	res := Resources{
		ID: aru.ID,
	}

	if aru.CPU != nil {
		if aru.CPU.Units.Value() > math.MaxUint32 {
			return Resources{}, errors.New("k8s api: cpu units value overflows uint32")
		}
		res.CPU.Units = uint32(aru.CPU.Units.Value()) // nolint: gosec
		res.CPU.Attributes = aru.CPU.Attributes.Dup()
	}

	if aru.Memory != nil {
		res.Memory.Size = strconv.FormatUint(aru.Memory.Quantity.Value(), 10)
		res.Memory.Attributes = aru.Memory.Attributes.Dup()
	}

	if aru.GPU != nil {
		if aru.GPU.Units.Value() > math.MaxUint32 {
			return Resources{}, errors.New("k8s api: gpu units value overflows uint32")
		}
		res.GPU.Units = uint32(aru.GPU.Units.Value()) // nolint: gosec
		res.GPU.Attributes = aru.GPU.Attributes
	}

	res.Storage = make(ResourceStorage, 0, len(aru.Storage))
	for _, storage := range aru.Storage {
		res.Storage = append(res.Storage, ResourceVolume{
			Name:       storage.Name,
			Size:       strconv.FormatUint(storage.Quantity.Value(), 10),
			Attributes: storage.Attributes.Dup(),
		})
	}

	return res, nil
}
