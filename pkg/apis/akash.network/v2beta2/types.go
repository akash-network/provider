package v2beta2

import (
	"errors"
	"fmt"
	"math"
	"strconv"

	sdk "github.com/cosmos/cosmos-sdk/types"

	mani "pkg.akt.dev/go/manifest/v2beta3"
	mtypes "pkg.akt.dev/go/node/market/v1"
	attrtypes "pkg.akt.dev/go/node/types/attributes/v1"
	rtypes "pkg.akt.dev/go/node/types/resources/v1beta4"
)

var (
	ErrInvalidArgs = fmt.Errorf("crd/%s: invalid args", crdVersion)
)

type Status struct {
	State   string `json:"state,omitempty"`
	Message string `json:"message,omitempty"`
}

type deployment struct {
	lid             mtypes.LeaseID
	group           mani.Group
	cparams         interface{}
	resourceVersion string
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

func (d deployment) ResourceVersion() string {
	return d.resourceVersion
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
	Units      uint32               `json:"units"`
	Attributes attrtypes.Attributes `json:"attributes,omitempty"`
}

type ResourceGPU struct {
	Units      uint32               `json:"units"`
	Attributes attrtypes.Attributes `json:"attributes,omitempty"`
}

type ResourceMemory struct {
	Size       string               `json:"size"`
	Attributes attrtypes.Attributes `json:"attributes,omitempty"`
}

type ResourceVolume struct {
	Name       string               `json:"name"`
	Size       string               `json:"size"`
	Attributes attrtypes.Attributes `json:"attributes,omitempty"`
}

type ResourceStorage []ResourceVolume

func (ru ResourceCPU) ToAkash() *rtypes.CPU {
	return &rtypes.CPU{
		Units:      rtypes.NewResourceValue(uint64(ru.Units)),
		Attributes: ru.Attributes.Dup(),
	}
}

func (ru ResourceGPU) ToAkash() *rtypes.GPU {
	return &rtypes.GPU{
		Units:      rtypes.NewResourceValue(uint64(ru.Units)),
		Attributes: ru.Attributes.Dup(),
	}
}

func (ru ResourceMemory) ToAkash() *rtypes.Memory {
	size, _ := strconv.ParseUint(ru.Size, 10, 64)

	return &rtypes.Memory{
		Quantity:   rtypes.NewResourceValue(size),
		Attributes: ru.Attributes.Dup(),
	}
}

func (ru ResourceVolume) ToAkash() rtypes.Storage {
	size, _ := strconv.ParseUint(ru.Size, 10, 64)

	return rtypes.Storage{
		Name:       ru.Name,
		Quantity:   rtypes.NewResourceValue(size),
		Attributes: ru.Attributes.Dup(),
	}
}

func (ru ResourceStorage) ToAkash() rtypes.Volumes {
	res := make(rtypes.Volumes, 0, len(ru))

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

func (ru *Resources) ToAkash() (rtypes.Resources, error) {
	return rtypes.Resources{
		ID:      ru.ID,
		CPU:     ru.CPU.ToAkash(),
		GPU:     ru.GPU.ToAkash(),
		Memory:  ru.Memory.ToAkash(),
		Storage: ru.Storage.ToAkash(),
	}, nil
}

func resourcesFromAkash(aru rtypes.Resources) (Resources, error) {
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
