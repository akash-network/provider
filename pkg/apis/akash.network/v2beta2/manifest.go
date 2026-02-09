package v2beta2

import (
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	rtypes "pkg.akt.dev/go/node/types/resources/v1beta4"

	mani "pkg.akt.dev/go/manifest/v2beta3"
	mtypes "pkg.akt.dev/go/node/market/v1"

	ktypes "github.com/akash-network/provider/cluster/kube/types/v1beta3"
	ctypes "github.com/akash-network/provider/cluster/types/v1beta3"
)

// Manifest store metadata, specifications and status of the Lease
// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type Manifest struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata"`

	Spec ManifestSpec `json:"spec,omitempty"`
}

// ManifestList stores metadata and items list of manifest
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type ManifestList struct {
	metav1.TypeMeta `           json:",inline"`
	metav1.ListMeta `           json:"metadata"`
	Items           []Manifest `json:"items"`
}

// ManifestServiceCredentials stores docker registry credentials
type ManifestServiceCredentials struct {
	Host     string `json:"host"`
	Email    string `json:"email"`
	Username string `json:"username"`
	Password string `json:"password"`
}

// ManifestService stores name, image, args, env, unit, count and expose list of service
type ManifestService struct {
	// Service name
	Name string `json:"name,omitempty"`
	// Docker image
	Image     string    `json:"image,omitempty"`
	Command   []string  `json:"command,omitempty"`
	Args      []string  `json:"args,omitempty"`
	Env       []string  `json:"env,omitempty"`
	Resources Resources `json:"resources"`
	// Number of instances
	Count uint32 `json:"count,omitempty"`
	// Overlay Network Links
	Expose []ManifestServiceExpose `json:"expose,omitempty"`
	// Miscellaneous service parameters
	Params          *ManifestServiceParams      `json:"params,omitempty"`
	SchedulerParams *SchedulerParams            `json:"scheduler_params,omitempty"`
	Credentials     *ManifestServiceCredentials `json:"credentials,omitempty"`
}

// ManifestGroup stores metadata, name and list of SDL manifest services
type ManifestGroup struct {
	// Placement profile name
	Name string `json:"name,omitempty"`
	// Service definitions
	Services []ManifestService `json:"services,omitempty"`
}

// ManifestSpec stores LeaseID, Group and metadata details
type ManifestSpec struct {
	LeaseID LeaseID       `json:"lease_id"`
	Group   ManifestGroup `json:"group"`
}

// ManifestStatus stores state and message of manifest
type ManifestStatus struct {
	State   string `json:"state,omitempty"`
	Message string `json:"message,omitempty"`
}

type ManifestStorageParams struct {
	Name     string `json:"name"     yaml:"name"`
	Mount    string `json:"mount"    yaml:"mount"`
	ReadOnly bool   `json:"readOnly" yaml:"readOnly"`
}

type ManifestServicePermissions struct {
	Read []string `json:"read,omitempty"`
}

type ManifestServiceParams struct {
	Storage     []ManifestStorageParams     `json:"storage,omitempty"`
	Permissions *ManifestServicePermissions `json:"permissions,omitempty"`
}

type SchedulerResourceGPU struct {
	Vendor     string `json:"vendor"`
	Model      string `json:"model"`
	MemorySize string `json:"memory_size"`
	Interface  string `json:"interface"`
}

type SchedulerResources struct {
	GPU *SchedulerResourceGPU `json:"gpu"`
}

type SchedulerParams struct {
	RuntimeClass string              `json:"runtime_class"`
	Resources    *SchedulerResources `json:"resources,omitempty"`
}

type ClusterSettings struct {
	SchedulerParams []*SchedulerParams `json:"scheduler_params"`
}

type ReservationClusterSettings map[uint32]*SchedulerParams

// ManifestServiceExpose stores exposed ports and accepted hosts details
type ManifestServiceExpose struct {
	Port                   uint16                           `json:"port,omitempty"`
	ExternalPort           uint16                           `json:"external_port,omitempty"`
	Proto                  string                           `json:"proto,omitempty"`
	Service                string                           `json:"service,omitempty"`
	Global                 bool                             `json:"global,omitempty"`
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

// NewManifest creates new manifest with provided details. Returns error in case of failure.
func NewManifest(ns string, lid mtypes.LeaseID, mgroup *mani.Group, settings ClusterSettings) (*Manifest, error) {
	if len(mgroup.Services) != len(settings.SchedulerParams) {
		return nil, fmt.Errorf("%w: group services don't not match scheduler services count (%d) != (%d)",
			ErrInvalidArgs,
			len(mgroup.Services),
			len(settings.SchedulerParams),
		)
	}

	group, err := manifestGroupToCRD(mgroup, settings)
	if err != nil {
		return nil, err
	}

	return &Manifest{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Manifest",
			APIVersion: "akash.network/v2beta2",
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

// Deployment returns the cluster.Deployment that the saved manifest represents.
func (m *Manifest) Deployment() (ctypes.IDeployment, error) {
	lid, err := m.Spec.LeaseID.FromCRD()
	if err != nil {
		return nil, err
	}

	group, schedulerParams, err := m.Spec.Group.FromCRD()
	if err != nil {
		return nil, err
	}

	return &deployment{
		lid:   lid,
		group: group,
		cparams: ClusterSettings{
			SchedulerParams: schedulerParams,
		},
		resourceVersion: m.ResourceVersion,
	}, nil
}

// FromCRD returns akash group details formatted from manifest group
func (m *ManifestGroup) FromCRD() (mani.Group, []*SchedulerParams, error) {
	am := mani.Group{
		Name:     m.Name,
		Services: make([]mani.Service, 0, len(m.Services)),
	}

	scheduleParams := make([]*SchedulerParams, 0, len(m.Services))

	for _, svc := range m.Services {
		asvc, err := svc.fromCRD()
		if err != nil {
			return am, nil, err
		}
		am.Services = append(am.Services, asvc)

		scheduleParams = append(scheduleParams, svc.SchedulerParams)
	}

	return am, scheduleParams, nil
}

// manifestGroupToCRD returns manifest group instance from akash group
func manifestGroupToCRD(m *mani.Group, settings ClusterSettings) (ManifestGroup, error) {
	ma := ManifestGroup{
		Name:     m.Name,
		Services: make([]ManifestService, 0, len(m.Services)),
	}

	for i, svc := range m.Services {
		service, err := manifestServiceFromProvider(svc, settings.SchedulerParams[i])
		if err != nil {
			return ManifestGroup{}, err
		}

		ma.Services = append(ma.Services, service)
	}

	return ma, nil
}

func (ms *ManifestService) fromCRD() (mani.Service, error) {
	res, err := ms.Resources.ToAkash()
	if err != nil {
		return mani.Service{}, err
	}

	ams := &mani.Service{
		Name:      ms.Name,
		Image:     ms.Image,
		Command:   ms.Command,
		Args:      ms.Args,
		Env:       ms.Env,
		Resources: res,
		Count:     ms.Count,
		Expose:    make([]mani.ServiceExpose, 0, len(ms.Expose)),
	}

	if ms.Credentials != nil {
		ams.Credentials = &mani.ImageCredentials{
			Host:     ms.Credentials.Host,
			Email:    ms.Credentials.Email,
			Username: ms.Credentials.Username,
			Password: ms.Credentials.Password,
		}
	}

	for _, expose := range ms.Expose {
		value, err := expose.toAkash()
		if err != nil {
			return mani.Service{}, err
		}
		ams.Expose = append(ams.Expose, value)

		if len(value.IP) != 0 {
			res.Endpoints = append(res.Endpoints, rtypes.Endpoint{
				Kind:           rtypes.Endpoint_LEASED_IP,
				SequenceNumber: value.EndpointSequenceNumber,
			})
		}
	}

	if ms.Params != nil {
		ams.Params = &mani.ServiceParams{
			Storage: make([]mani.StorageParams, 0, len(ms.Params.Storage)),
		}

		for _, storage := range ms.Params.Storage {
			ams.Params.Storage = append(ams.Params.Storage, mani.StorageParams{
				Name:     storage.Name,
				Mount:    storage.Mount,
				ReadOnly: storage.ReadOnly,
			})
		}

		if ms.Params.Permissions != nil {
			ams.Params.Permissions = &mani.ServicePermissions{
				Read: ms.Params.Permissions.Read,
			}
		}
	}

	return *ams, nil
}

func manifestServiceFromProvider(ams mani.Service, schedulerParams *SchedulerParams) (ManifestService, error) {
	resources, err := resourcesFromAkash(ams.Resources)
	if err != nil {
		return ManifestService{}, err
	}

	ms := ManifestService{
		Name:            ams.Name,
		Image:           ams.Image,
		Command:         ams.Command,
		Args:            ams.Args,
		Env:             ams.Env,
		Resources:       resources,
		Count:           ams.Count,
		Expose:          make([]ManifestServiceExpose, 0, len(ams.Expose)),
		SchedulerParams: schedulerParams,
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

		if ams.Params.Permissions != nil {
			ms.Params.Permissions = &ManifestServicePermissions{
				Read: ams.Params.Permissions.Read,
			}
		}
	}

	if ams.Credentials != nil {
		ms.Credentials = &ManifestServiceCredentials{
			Host:     ams.Credentials.Host,
			Email:    ams.Credentials.Email,
			Username: ams.Credentials.Username,
			Password: ams.Credentials.Password,
		}
	}

	return ms, nil
}

func (mse ManifestServiceExpose) toAkash() (mani.ServiceExpose, error) {
	proto, err := mani.ParseServiceProtocol(mse.Proto)
	if err != nil {
		return mani.ServiceExpose{}, err
	}
	return mani.ServiceExpose{
		Port:                   uint32(mse.Port),
		ExternalPort:           uint32(mse.ExternalPort),
		Proto:                  proto,
		Service:                mse.Service,
		Global:                 mse.Global,
		Hosts:                  mse.Hosts,
		EndpointSequenceNumber: mse.EndpointSequenceNumber,
		IP:                     mse.IP,
		HTTPOptions: mani.ServiceExposeHTTPOptions{
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

func manifestServiceExposeFromAkash(amse mani.ServiceExpose) ManifestServiceExpose {
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
