package builder

import (
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/tendermint/tendermint/libs/log"

	"github.com/akash-network/node/sdl"
	sdlutil "github.com/akash-network/node/sdl/util"

	crd "github.com/akash-network/provider/pkg/apis/akash.network/v2beta2"
)

const (
	ResourceGPUNvidia = corev1.ResourceName("nvidia.com/gpu")
	ResourceGPUAMD    = corev1.ResourceName("amd.com/gpu")
	GPUVendorNvidia   = "nvidia"
	GPUVendorAMD      = "amd"
)

type workloadBase interface {
	builderBase
	Name() string
}

type Workload struct {
	builder
	serviceIdx int
}

var _ workloadBase = (*Workload)(nil)

func NewWorkloadBuilder(
	log log.Logger,
	settings Settings,
	deployment IClusterDeployment,
	serviceIdx int) Workload {
	return Workload{
		builder: builder{
			settings:   settings,
			log:        log.With("module", "kube-builder"),
			deployment: deployment,
		},
		serviceIdx: serviceIdx,
	}
}

func (b *Workload) Name() string {
	return b.deployment.ManifestGroup().Services[b.serviceIdx].Name
}

func (b *Workload) container() corev1.Container {
	falseValue := false

	service := &b.deployment.ManifestGroup().Services[b.serviceIdx]
	sparams := b.deployment.ClusterParams().SchedulerParams[b.serviceIdx]

	kcontainer := corev1.Container{
		Name:    service.Name,
		Image:   service.Image,
		Command: service.Command,
		Args:    service.Args,
		Resources: corev1.ResourceRequirements{
			Limits:   make(corev1.ResourceList),
			Requests: make(corev1.ResourceList),
		},
		ImagePullPolicy: corev1.PullIfNotPresent,
		SecurityContext: &corev1.SecurityContext{
			RunAsNonRoot:             &falseValue,
			Privileged:               &falseValue,
			AllowPrivilegeEscalation: &falseValue,
		},
	}

	if cpu := service.Resources.CPU; cpu != nil {
		requestedCPU := sdlutil.ComputeCommittedResources(b.settings.CPUCommitLevel, cpu.Units)
		kcontainer.Resources.Requests[corev1.ResourceCPU] = resource.NewScaledQuantity(int64(requestedCPU.Value()), resource.Milli).DeepCopy()
		kcontainer.Resources.Limits[corev1.ResourceCPU] = resource.NewScaledQuantity(int64(cpu.Units.Value()), resource.Milli).DeepCopy()
	}

	if gpu := service.Resources.GPU; gpu != nil && gpu.Units.Value() > 0 {
		var resourceName corev1.ResourceName

		switch sparams.Resources.GPU.Vendor {
		case GPUVendorNvidia:
			resourceName = ResourceGPUNvidia
		case GPUVendorAMD:
			resourceName = ResourceGPUAMD
		default:
			panic("requested for unsupported GPU vendor")
		}

		// GPUs are only supposed to be specified in the limits section, which means
		//  - can specify GPU limits without specifying requests, because Kubernetes will use the limit as the request value by default.
		//  - can specify GPU in both limits and requests but these two values must be equal.
		//  - cannot specify GPU requests without specifying limits.
		requestedGPU := sdlutil.ComputeCommittedResources(b.settings.GPUCommitLevel, gpu.Units)
		kcontainer.Resources.Requests[resourceName] = resource.NewQuantity(int64(requestedGPU.Value()), resource.DecimalSI).DeepCopy()
		kcontainer.Resources.Limits[resourceName] = resource.NewQuantity(int64(gpu.Units.Value()), resource.DecimalSI).DeepCopy()
	}

	if mem := service.Resources.Memory; mem != nil {
		requestedMem := sdlutil.ComputeCommittedResources(b.settings.MemoryCommitLevel, mem.Quantity)
		kcontainer.Resources.Requests[corev1.ResourceMemory] = resource.NewQuantity(int64(requestedMem.Value()), resource.DecimalSI).DeepCopy()
		kcontainer.Resources.Limits[corev1.ResourceMemory] = resource.NewQuantity(int64(mem.Quantity.Value()), resource.DecimalSI).DeepCopy()
	}

	for _, ephemeral := range service.Resources.Storage {
		attr := ephemeral.Attributes.Find(sdl.StorageAttributePersistent)
		if persistent, _ := attr.AsBool(); !persistent {
			requestedStorage := sdlutil.ComputeCommittedResources(b.settings.StorageCommitLevel, ephemeral.Quantity)
			kcontainer.Resources.Requests[corev1.ResourceEphemeralStorage] = resource.NewQuantity(int64(requestedStorage.Value()), resource.DecimalSI).DeepCopy()
			kcontainer.Resources.Limits[corev1.ResourceEphemeralStorage] = resource.NewQuantity(int64(ephemeral.Quantity.Value()), resource.DecimalSI).DeepCopy()

			break
		}
	}

	if service.Params != nil {
		for _, params := range service.Params.Storage {
			kcontainer.VolumeMounts = append(kcontainer.VolumeMounts, corev1.VolumeMount{
				// matches VolumeName in persistentVolumeClaims below
				Name:      fmt.Sprintf("%s-%s", service.Name, params.Name),
				ReadOnly:  params.ReadOnly,
				MountPath: params.Mount,
			})
		}
	}

	envVarsAdded := make(map[string]int)
	for _, env := range service.Env {
		parts := strings.SplitN(env, "=", 2)
		switch len(parts) {
		case 2:
			kcontainer.Env = append(kcontainer.Env, corev1.EnvVar{Name: parts[0], Value: parts[1]})
		case 1:
			kcontainer.Env = append(kcontainer.Env, corev1.EnvVar{Name: parts[0]})
		}
		envVarsAdded[parts[0]] = 0
	}
	kcontainer.Env = b.addEnvVarsForDeployment(envVarsAdded, kcontainer.Env)

	for _, expose := range service.Expose {
		kcontainer.Ports = append(kcontainer.Ports, corev1.ContainerPort{
			ContainerPort: int32(expose.Port),
		})
	}

	return kcontainer
}

func (b *Workload) persistentVolumeClaims() []corev1.PersistentVolumeClaim {
	var pvcs []corev1.PersistentVolumeClaim // nolint:prealloc

	service := &b.deployment.ManifestGroup().Services[b.serviceIdx]

	for _, storage := range service.Resources.Storage {
		attr := storage.Attributes.Find(sdl.StorageAttributePersistent)
		if persistent, valid := attr.AsBool(); !valid || !persistent {
			continue
		}

		volumeMode := corev1.PersistentVolumeFilesystem
		pvc := corev1.PersistentVolumeClaim{
			ObjectMeta: metav1.ObjectMeta{
				Name: fmt.Sprintf("%s-%s", service.Name, storage.Name),
			},
			Spec: corev1.PersistentVolumeClaimSpec{
				AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
				Resources: corev1.ResourceRequirements{
					Limits:   make(corev1.ResourceList),
					Requests: make(corev1.ResourceList),
				},
				VolumeMode:       &volumeMode,
				StorageClassName: nil,
				DataSource:       nil, // bind to existing pvc. akash does not support it. yet
			},
		}

		pvc.Spec.Resources.Requests[corev1.ResourceStorage] = resource.NewQuantity(int64(storage.Quantity.Value()), resource.DecimalSI).DeepCopy()

		attr = storage.Attributes.Find(sdl.StorageAttributeClass)
		if class, valid := attr.AsString(); valid && class != sdl.StorageClassDefault {
			pvc.Spec.StorageClassName = &class
		}

		pvcs = append(pvcs, pvc)
	}

	return pvcs
}

func (b *Workload) runtimeClass() *string {
	params := b.deployment.ClusterParams().SchedulerParams[b.serviceIdx]
	var effectiveRuntimeClassName *string

	if params != nil {
		if len(params.RuntimeClass) != 0 && params.RuntimeClass != runtimeClassNoneValue {
			runtimeClass := new(string)
			*runtimeClass = params.RuntimeClass
			effectiveRuntimeClassName = runtimeClass
		}
	}

	return effectiveRuntimeClassName
}

func (b *Workload) replicas() *int32 {
	replicas := new(int32)
	*replicas = int32(b.deployment.ManifestGroup().Services[b.serviceIdx].Count)

	return replicas
}

func (b *Workload) affinity() *corev1.Affinity {
	svc := b.deployment.ClusterParams().SchedulerParams[b.serviceIdx]

	if svc == nil || svc.Resources == nil {
		return nil
	}

	selectors := nodeSelectorsFromResources(svc.Resources)
	if len(selectors) == 0 {
		return nil
	}

	affinity := &corev1.Affinity{
		NodeAffinity: &corev1.NodeAffinity{
			RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
				NodeSelectorTerms: []corev1.NodeSelectorTerm{
					{
						MatchExpressions: selectors,
					},
				},
			},
		},
	}

	return affinity
}

func nodeSelectorsFromResources(res *crd.SchedulerResources) []corev1.NodeSelectorRequirement {
	if res == nil {
		return nil
	}

	var selectors []corev1.NodeSelectorRequirement

	if gpu := res.GPU; gpu != nil {
		selectors = append(selectors, corev1.NodeSelectorRequirement{
			Key:      fmt.Sprintf("%s.vendor.%s.model.%s", AkashServiceCapabilityGPU, gpu.Vendor, gpu.Model),
			Operator: "In",
			Values: []string{
				"true",
			},
		})
	}

	return selectors
}

func (b *Workload) labels() map[string]string {
	obj := b.builder.labels()
	obj[AkashManifestServiceLabelName] = b.deployment.ManifestGroup().Services[b.serviceIdx].Name
	return obj
}

func (b *Workload) imagePullSecrets() []corev1.LocalObjectReference {
	if b.settings.DockerImagePullSecretsName == "" {
		return nil
	}

	return []corev1.LocalObjectReference{{Name: b.settings.DockerImagePullSecretsName}}
}

func (b *Workload) addEnvVarsForDeployment(envVarsAlreadyAdded map[string]int, env []corev1.EnvVar) []corev1.EnvVar {
	lid := b.deployment.LeaseID()

	// Add each env. var. if it is not already set by the SDL
	env = addIfNotPresent(envVarsAlreadyAdded, env, envVarAkashGroupSequence, lid.GetGSeq())
	env = addIfNotPresent(envVarsAlreadyAdded, env, envVarAkashDeploymentSequence, lid.GetDSeq())
	env = addIfNotPresent(envVarsAlreadyAdded, env, envVarAkashOrderSequence, lid.GetOSeq())
	env = addIfNotPresent(envVarsAlreadyAdded, env, envVarAkashOwner, lid.Owner)
	env = addIfNotPresent(envVarsAlreadyAdded, env, envVarAkashProvider, lid.Provider)
	env = addIfNotPresent(envVarsAlreadyAdded, env, envVarAkashClusterPublicHostname, b.settings.ClusterPublicHostname)

	return env
}
