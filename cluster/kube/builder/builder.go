package builder

import (
	"errors"
	"fmt"
	"reflect"
	"strconv"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	"github.com/tendermint/tendermint/libs/log"

	mani "github.com/akash-network/akash-api/go/manifest/v2beta2"
	mtypes "github.com/akash-network/akash-api/go/node/market/v1beta3"

	ctypes "github.com/akash-network/provider/cluster/types/v1beta3"
	clusterUtil "github.com/akash-network/provider/cluster/util"
	crd "github.com/akash-network/provider/pkg/apis/akash.network/v2beta2"
)

const (
	AkashManagedLabelName         = "akash.network"
	AkashManifestServiceLabelName = "akash.network/manifest-service"
	AkashNetworkStorageClasses    = "akash.network/storageclasses"
	AkashServiceTarget            = "akash.network/service-target"
	AkashServiceCapabilityGPU     = "akash.network/capabilities.gpu"
	AkashMetalLB                  = "metal-lb"
	akashDeploymentPolicyName     = "akash-deployment-restrictions"
	akashNetworkNamespace         = "akash.network/namespace"
	AkashLeaseOwnerLabelName      = "akash.network/lease.id.owner"
	AkashLeaseDSeqLabelName       = "akash.network/lease.id.dseq"
	AkashLeaseGSeqLabelName       = "akash.network/lease.id.gseq"
	AkashLeaseOSeqLabelName       = "akash.network/lease.id.oseq"
	AkashLeaseProviderLabelName   = "akash.network/lease.id.provider"
	AkashLeaseManifestVersion     = "akash.network/manifest.version"
)

const (
	runtimeClassNoneValue = "none"
	runtimeClassNvidia    = "nvidia"
)

const (
	envVarAkashGroupSequence         = "AKASH_GROUP_SEQUENCE"
	envVarAkashDeploymentSequence    = "AKASH_DEPLOYMENT_SEQUENCE"
	envVarAkashOrderSequence         = "AKASH_ORDER_SEQUENCE"
	envVarAkashOwner                 = "AKASH_OWNER"
	envVarAkashProvider              = "AKASH_PROVIDER"
	envVarAkashClusterPublicHostname = "AKASH_CLUSTER_PUBLIC_HOSTNAME"
)

var (
	ErrKubeBuilder = errors.New("kube-builder")
)

var (
	dnsPort     = intstr.FromInt(53)
	udpProtocol = corev1.Protocol("UDP")
	tcpProtocol = corev1.Protocol("TCP")
)

type IClusterDeployment interface {
	LeaseID() mtypes.LeaseID
	ManifestGroup() *mani.Group
	ClusterParams() crd.ClusterSettings
}

type ClusterDeployment struct {
	Lid     mtypes.LeaseID
	Group   *mani.Group
	Sparams crd.ClusterSettings
}

var _ IClusterDeployment = (*ClusterDeployment)(nil)

func ClusterDeploymentFromDeployment(d ctypes.IDeployment) (IClusterDeployment, error) {
	cparams, valid := d.ClusterParams().(crd.ClusterSettings)
	if !valid {
		// nolint: goerr113
		return nil, fmt.Errorf("%w: unexpected type from ClusterParams(). expected (%s), actual (%s)",
			ErrKubeBuilder,
			reflect.TypeOf(crd.ClusterSettings{}),
			reflect.TypeOf(d.ClusterParams()),
		)
	}

	cd := &ClusterDeployment{
		Lid:     d.LeaseID(),
		Group:   d.ManifestGroup(),
		Sparams: cparams,
	}

	if err := cd.validate(); err != nil {
		return cd, err
	}

	return cd, nil
}

func (d *ClusterDeployment) LeaseID() mtypes.LeaseID {
	return d.Lid
}

func (d *ClusterDeployment) ManifestGroup() *mani.Group {
	return d.Group
}

func (d *ClusterDeployment) ClusterParams() crd.ClusterSettings {
	return d.Sparams
}

func (d *ClusterDeployment) validate() error {
	if len(d.Group.Services) != len(d.Sparams.SchedulerParams) {
		return fmt.Errorf("%w: group services count does not match scheduler params count (%d) != (%d)",
			ErrKubeBuilder,
			len(d.Group.Services),
			len(d.Sparams.SchedulerParams))
	}

	for idx := range d.Group.Services {
		svc := d.Group.Services[idx]
		sParams := d.Sparams.SchedulerParams[idx]

		if svc.Resources.CPU == nil {
			return fmt.Errorf("%w: service %s. resource CPU cannot be nil", ErrKubeBuilder, svc.Name)
		}

		if svc.Resources.GPU == nil {
			return fmt.Errorf("%w: service %s. resource GPU cannot be nil", ErrKubeBuilder, svc.Name)
		}

		if svc.Resources.Memory == nil {
			return fmt.Errorf("%w: service %s. resource Memory cannot be nil", ErrKubeBuilder, svc.Name)
		}

		if svc.Resources.GPU.Units.Value() > 0 {
			if sParams == nil ||
				sParams.Resources == nil ||
				sParams.Resources.GPU == nil {
				return fmt.Errorf("%w: service %s. SchedulerParams.Resources.GPU must not be nil when GPU > 0", ErrKubeBuilder, svc.Name)
			}
		}
	}

	return nil
}

type builderBase interface {
	NS() string
	Name() string
	Validate() error
}

type builder struct {
	log        log.Logger
	settings   Settings
	deployment IClusterDeployment
}

var _ builderBase = (*builder)(nil)

func (b *builder) NS() string {
	return LidNS(b.deployment.LeaseID())
}

func (b *builder) Name() string {
	return b.NS()
}

func (b *builder) labels() map[string]string {
	return map[string]string{
		AkashManagedLabelName: "true",
		akashNetworkNamespace: LidNS(b.deployment.LeaseID()),
	}
}

func (b *builder) Validate() error {
	return nil
}

func addIfNotPresent(envVarsAlreadyAdded map[string]int, env []corev1.EnvVar, key string, value interface{}) []corev1.EnvVar {
	_, exists := envVarsAlreadyAdded[key]
	if exists {
		return env
	}

	env = append(env, corev1.EnvVar{Name: key, Value: fmt.Sprintf("%v", value)})
	return env
}

const SuffixForNodePortServiceName = "-np"

func makeGlobalServiceNameFromBasename(basename string) string {
	return fmt.Sprintf("%s%s", basename, SuffixForNodePortServiceName)
}

// LidNS generates a unique sha256 sum for identifying a provider's object name.
func LidNS(lid mtypes.LeaseID) string {
	return clusterUtil.LeaseIDToNamespace(lid)
}

func AppendLeaseLabels(lid mtypes.LeaseID, labels map[string]string) map[string]string {
	labels[AkashLeaseOwnerLabelName] = lid.Owner
	labels[AkashLeaseDSeqLabelName] = strconv.FormatUint(lid.DSeq, 10)
	labels[AkashLeaseGSeqLabelName] = strconv.FormatUint(uint64(lid.GSeq), 10)
	labels[AkashLeaseOSeqLabelName] = strconv.FormatUint(uint64(lid.OSeq), 10)
	labels[AkashLeaseProviderLabelName] = lid.Provider
	return labels
}
