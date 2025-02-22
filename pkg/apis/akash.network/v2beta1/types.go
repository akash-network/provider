package v2beta1

import (
	"math"
	"strconv"

	"github.com/pkg/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	sdk "github.com/cosmos/cosmos-sdk/types"

	maniv2beta1 "github.com/akash-network/akash-api/go/manifest/v2beta1"
	mtypes "github.com/akash-network/akash-api/go/node/market/v1beta2"
	types "github.com/akash-network/akash-api/go/node/types/v1beta2"

	ktypes "github.com/akash-network/provider/cluster/kube/types/v1beta0"
	ctypes "github.com/akash-network/provider/cluster/types/v1beta2"
)

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// Manifest store metadata, specifications and status of the Lease
type Manifest struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata"`

	Spec   ManifestSpec   `json:"spec,omitempty"`
	Status ManifestStatus `json:"status,omitempty"`
}

// ManifestStatus stores state and message of manifest
type ManifestStatus struct {
	State   string `json:"state,omitempty"`
	Message string `json:"message,omitempty"`
}

// ManifestSpec stores LeaseID, Group and metadata details
type ManifestSpec struct {
	LeaseID LeaseID       `json:"lease_id"`
	Group   ManifestGroup `json:"group"`
}

// Deployment returns the cluster.Deployment that the saved manifest represents.
func (m Manifest) Deployment() (ctypes.Deployment, error) {
	lid, err := m.Spec.LeaseID.ToAkash()
	if err != nil {
		return nil, err
	}

	group, err := m.Spec.Group.toAkash()
	if err != nil {
		return nil, err
	}
	return deployment{lid: lid, group: group}, nil
}

type deployment struct {
	lid   mtypes.LeaseID
	group ctypes.Group
}

func (d deployment) LeaseID() mtypes.LeaseID {
	return d.lid
}

func (d deployment) ManifestGroup() ctypes.Group {
	return d.group
}

// NewManifest creates new manifest with provided details. Returns error in case of failure.
func NewManifest(ns string, lid mtypes.LeaseID, mgroup *ctypes.Group) (*Manifest, error) {
	group, err := manifestGroupFromAkash(mgroup)
	if err != nil {
		return nil, err
	}

	return &Manifest{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Manifest",
			APIVersion: "akash.network/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      ktypes.LeaseIDToNamespace(lid),
			Namespace: ns,
		},
		Spec: ManifestSpec{
			Group:   group,
			LeaseID: LeaseIDFromAkash(lid),
		},
	}, nil
}

// LeaseID stores deployment, group sequence, order, provider and metadata
type LeaseID struct {
	Owner    string `json:"owner"`
	DSeq     string `json:"dseq"`
	GSeq     uint32 `json:"gseq"`
	OSeq     uint32 `json:"oseq"`
	Provider string `json:"provider"`
}

// ToAkash returns LeaseID from LeaseID details
func (id LeaseID) ToAkash() (mtypes.LeaseID, error) {
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

// ManifestGroup stores metadata, name and list of SDL manifest services
type ManifestGroup struct {
	// Placement profile name
	Name string `json:"name,omitempty"`
	// Service definitions
	Services []ManifestService `json:"services,omitempty"`
}

// ToAkash returns akash group details formatted from manifest group
func (m ManifestGroup) toAkash() (ctypes.Group, error) {
	am := ctypes.Group{
		Name:     m.Name,
		Services: make([]ctypes.Service, 0, len(m.Services)),
	}

	for _, svc := range m.Services {
		asvc, err := svc.toAkash()
		if err != nil {
			return am, err
		}
		am.Services = append(am.Services, asvc)
	}

	return am, nil
}

// ManifestGroupFromAkash returns manifest group instance from akash group
func manifestGroupFromAkash(m *ctypes.Group) (ManifestGroup, error) {
	ma := ManifestGroup{
		Name:     m.Name,
		Services: make([]ManifestService, 0, len(m.Services)),
	}

	for _, svc := range m.Services {
		service, err := manifestServiceFromAkash(svc)
		if err != nil {
			return ManifestGroup{}, err
		}

		ma.Services = append(ma.Services, service)
	}

	return ma, nil
}

type ManifestStorageParams struct {
	Name     string `json:"name" yaml:"name"`
	Mount    string `json:"mount" yaml:"mount"`
	ReadOnly bool   `json:"readOnly" yaml:"readOnly"`
}

type ManifestServiceParams struct {
	Storage []ManifestStorageParams `json:"storage,omitempty"`
}

// ManifestService stores name, image, args, env, unit, count and expose list of service
type ManifestService struct {
	// Service name
	Name string `json:"name,omitempty"`
	// Docker image
	Image   string   `json:"image,omitempty"`
	Command []string `json:"command,omitempty"`
	Args    []string `json:"args,omitempty"`
	Env     []string `json:"env,omitempty"`
	// Resource requirements
	// in current version of CRD it is named as unit
	Resources ResourceUnits `json:"unit"`
	// Number of instances
	Count uint32 `json:"count,omitempty"`
	// Overlay Network Links
	Expose []ManifestServiceExpose `json:"expose,omitempty"`
	// Miscellaneous service parameters
	Params *ManifestServiceParams `json:"params,omitempty"`
}

func (ms ManifestService) toAkash() (ctypes.Service, error) {
	res, err := ms.Resources.ToAkash()
	if err != nil {
		return ctypes.Service{}, err
	}

	ams := &ctypes.Service{
		Service: maniv2beta1.Service{
			Name:      ms.Name,
			Image:     ms.Image,
			Command:   ms.Command,
			Args:      ms.Args,
			Env:       ms.Env,
			Resources: res,
			Count:     ms.Count,
			Expose:    make([]maniv2beta1.ServiceExpose, 0, len(ms.Expose)),
		},
	}

	for _, expose := range ms.Expose {
		value, err := expose.toAkash()
		if err != nil {
			return ctypes.Service{}, err
		}
		ams.Expose = append(ams.Expose, value)

		if len(value.IP) != 0 {
			res.Endpoints = append(res.Endpoints, types.Endpoint{
				Kind:           types.Endpoint_LEASED_IP,
				SequenceNumber: value.EndpointSequenceNumber,
			})
		}
	}

	if ms.Params != nil {
		ams.Params = &maniv2beta1.ServiceParams{
			Storage: make([]maniv2beta1.StorageParams, 0, len(ms.Params.Storage)),
		}

		for _, storage := range ms.Params.Storage {
			ams.Params.Storage = append(ams.Params.Storage, maniv2beta1.StorageParams{
				Name:     storage.Name,
				Mount:    storage.Mount,
				ReadOnly: storage.ReadOnly,
			})
		}
	}

	return *ams, nil
}

func manifestServiceFromAkash(ams ctypes.Service) (ManifestService, error) {
	resources, err := resourceUnitsFromAkash(ams.Resources)
	if err != nil {
		return ManifestService{}, err
	}

	ms := ManifestService{
		Name:      ams.Name,
		Image:     ams.Image,
		Command:   ams.Command,
		Args:      ams.Args,
		Env:       ams.Env,
		Resources: resources,
		Count:     ams.Count,
		Expose:    make([]ManifestServiceExpose, 0, len(ams.Expose)),
	}

	for _, expose := range ams.Expose {
		ms.Expose = append(ms.Expose, manifestServiceExposeFromAkash(expose))
	}

	if ams.Params != nil {
		ms.Params = &ManifestServiceParams{
			Storage: make([]ManifestStorageParams, 0, len(ams.Params.Storage)),
		}

		for _, storage := range ams.Params.Storage {
			ms.Params.Storage = append(ms.Params.Storage, ManifestStorageParams{
				Name:     storage.Name,
				Mount:    storage.Mount,
				ReadOnly: storage.ReadOnly,
			})
		}
	}

	return ms, nil
}

// ManifestServiceExpose stores exposed ports and accepted hosts details
type ManifestServiceExpose struct {
	Port         uint16 `json:"port,omitempty"`
	ExternalPort uint16 `json:"external_port,omitempty"`
	Proto        string `json:"proto,omitempty"`
	Service      string `json:"service,omitempty"`
	Global       bool   `json:"global,omitempty"`
	// accepted hostnames
	Hosts                  []string                         `json:"hosts,omitempty"`
	HTTPOptions            ManifestServiceExposeHTTPOptions `json:"http_options,omitempty"`
	IP                     string                           `json:"ip,omitempty"`
	EndpointSequenceNumber uint32                           `json:"endpoint_sequence_number"`
}

type ManifestServiceExposeHTTPOptions struct {
	MaxBodySize uint32   `json:"max_body_size,omitempty"`
	ReadTimeout uint32   `json:"read_timeout,omitempty"`
	SendTimeout uint32   `json:"send_timeout,omitempty"`
	NextTries   uint32   `json:"next_tries,omitempty"`
	NextTimeout uint32   `json:"next_timeout,omitempty"`
	NextCases   []string `json:"next_cases,omitempty"`
}

func (mse ManifestServiceExpose) toAkash() (maniv2beta1.ServiceExpose, error) {
	proto, err := maniv2beta1.ParseServiceProtocol(mse.Proto)
	if err != nil {
		return maniv2beta1.ServiceExpose{}, err
	}
	return maniv2beta1.ServiceExpose{
		Port:                   uint32(mse.Port),
		ExternalPort:           uint32(mse.ExternalPort),
		Proto:                  proto,
		Service:                mse.Service,
		Global:                 mse.Global,
		Hosts:                  mse.Hosts,
		EndpointSequenceNumber: mse.EndpointSequenceNumber,
		IP:                     mse.IP,
		HTTPOptions: maniv2beta1.ServiceExposeHTTPOptions{
			MaxBodySize: mse.HTTPOptions.MaxBodySize,
			ReadTimeout: mse.HTTPOptions.ReadTimeout,
			SendTimeout: mse.HTTPOptions.SendTimeout,
			NextTries:   mse.HTTPOptions.NextTries,
			NextTimeout: mse.HTTPOptions.NextTimeout,
			NextCases:   mse.HTTPOptions.NextCases,
		},
	}, nil
}

func (mse ManifestServiceExpose) DetermineExposedExternalPort() uint16 {
	if mse.ExternalPort == 0 {
		return mse.Port
	}
	return mse.ExternalPort
}

func manifestServiceExposeFromAkash(amse maniv2beta1.ServiceExpose) ManifestServiceExpose {
	return ManifestServiceExpose{
		Port:                   uint16(amse.Port),         // nolint: gosec
		ExternalPort:           uint16(amse.ExternalPort), // nolint: gosec
		Proto:                  amse.Proto.ToString(),
		Service:                amse.Service,
		Global:                 amse.Global,
		Hosts:                  amse.Hosts,
		IP:                     amse.IP,
		EndpointSequenceNumber: amse.EndpointSequenceNumber,
		HTTPOptions: ManifestServiceExposeHTTPOptions{
			MaxBodySize: amse.HTTPOptions.MaxBodySize,
			ReadTimeout: amse.HTTPOptions.ReadTimeout,
			SendTimeout: amse.HTTPOptions.SendTimeout,
			NextTries:   amse.HTTPOptions.NextTries,
			NextTimeout: amse.HTTPOptions.NextTimeout,
			NextCases:   amse.HTTPOptions.NextCases,
		},
	}
}

type ManifestServiceStorage struct {
	Name string `json:"name"`
	Size string `json:"size"`
}

// ResourceUnits stores cpu, memory and storage details
type ResourceUnits struct {
	CPU     uint32                   `json:"cpu,omitempty"`
	Memory  string                   `json:"memory,omitempty"`
	Storage []ManifestServiceStorage `json:"storage,omitempty"`
}

func (ru ResourceUnits) ToAkash() (types.ResourceUnits, error) {
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
		res.CPU = uint32(aru.CPU.Units.Value()) // nolint: gosec
	}
	if aru.Memory != nil {
		res.Memory = strconv.FormatUint(aru.Memory.Quantity.Value(), 10)
	}

	res.Storage = make([]ManifestServiceStorage, 0, len(aru.Storage))
	for _, storage := range aru.Storage {
		res.Storage = append(res.Storage, ManifestServiceStorage{
			Name: storage.Name,
			Size: strconv.FormatUint(storage.Quantity.Value(), 10),
		})
	}

	return res, nil
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// ManifestList stores metadata and items list of manifest
type ManifestList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`
	Items           []Manifest `json:"items"`
}

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

type ProviderHost struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata"`

	Spec   ProviderHostSpec   `json:"spec,omitempty"`
	Status ProviderHostStatus `json:"status,omitempty"`
}

type ProviderHostStatus struct {
	State   string `json:"state,omitempty"`
	Message string `json:"message,omitempty"`
}

type ProviderHostSpec struct {
	Owner        string `json:"owner"`
	Provider     string `json:"provider"`
	Hostname     string `json:"hostname"`
	Dseq         uint64 `json:"dseq"`
	Gseq         uint32 `json:"gseq"`
	Oseq         uint32 `json:"oseq"`
	ServiceName  string `json:"service_name"`
	ExternalPort uint32 `json:"external_port"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type ProviderHostList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`
	Items           []ProviderHost `json:"items"`
}

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type ProviderLeasedIP struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata"`

	Spec   ProviderLeasedIPSpec   `json:"spec,omitempty"`
	Status ProviderLeasedIPStatus `json:"status,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type ProviderLeasedIPList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`
	Items           []ProviderLeasedIP `json:"items"`
}

type ProviderLeasedIPStatus struct {
	State   string `json:"state,omitempty"`
	Message string `json:"message,omitempty"`
}

type ProviderLeasedIPSpec struct {
	LeaseID      LeaseID `json:"lease_id"`
	ServiceName  string  `json:"service_name"`
	Port         uint32  `json:"port"`
	ExternalPort uint32  `json:"external_port"`
	SharingKey   string  `json:"sharing_key"`
	Protocol     string  `json:"protocol"`
}
