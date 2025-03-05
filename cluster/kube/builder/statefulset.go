package builder

import (
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"strconv"
)

type StatefulSet interface {
	workloadBase
	Create() (*appsv1.StatefulSet, error)
	Update(obj *appsv1.StatefulSet) (*appsv1.StatefulSet, error)
}

type statefulSet struct {
	Workload
}

var _ StatefulSet = (*statefulSet)(nil)

func BuildStatefulSet(workload Workload) StatefulSet {
	ss := &statefulSet{
		Workload: workload,
	}

	ss.Workload.log = ss.Workload.log.With("object", "statefulset", "service-name", ss.deployment.ManifestGroup().Services[ss.serviceIdx].Name)

	return ss
}

func (b *statefulSet) Create() (*appsv1.StatefulSet, error) { // nolint:golint,unparam
	falseValue := false
	trueValue := true

	revisionHistoryLimit := int32(1)

	partition := int32(0)
	maxUnavailable := intstr.FromInt32(1)

	// Add config volume
	configVolume := corev1.Volume{
		Name: AkashConfigVolume,
		VolumeSource: corev1.VolumeSource{
			EmptyDir: &corev1.EmptyDirVolumeSource{},
		},
	}

	// Calculate if NodePort is required
	requiresNodePort := false
	service := &b.deployment.ManifestGroup().Services[b.serviceIdx]
	for _, expose := range service.Expose {
		if expose.Global {
			requiresNodePort = true
			break
		}
	}

	// Add init container
	initContainer := corev1.Container{
		Name:  AkashConfigInitName,
		Image: "alpine/curl:3.14",
		Command: []string{
			"/bin/sh",
			"-c",
			akashInitScript,
		},
		Env: []corev1.EnvVar{
			{
				Name:  "SERVICE_NAME",
				Value: b.Name(),
			},
			{
				Name:  "AKASH_CONFIG_PATH",
				Value: AkashConfigMount,
			},
			{
				Name:  "AKASH_CONFIG_FILE",
				Value: AkashConfigEnvFile,
			},
			{
				Name:  "AKASH_REQUIRES_NODEPORT",
				Value: strconv.FormatBool(requiresNodePort),
			},
		},
		VolumeMounts: []corev1.VolumeMount{
			{
				Name:      AkashConfigVolume,
				MountPath: AkashConfigMount,
			},
		},
	}

	kdeployment := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:   b.Name(),
			Labels: b.labels(),
		},
		Spec: appsv1.StatefulSetSpec{
			UpdateStrategy: appsv1.StatefulSetUpdateStrategy{
				Type: appsv1.RollingUpdateStatefulSetStrategyType,
				RollingUpdate: &appsv1.RollingUpdateStatefulSetStrategy{
					Partition:      &partition,
					MaxUnavailable: &maxUnavailable,
				},
			},
			Selector: &metav1.LabelSelector{
				MatchLabels: b.selectorLabels(),
			},
			RevisionHistoryLimit: &revisionHistoryLimit,
			Replicas:             b.replicas(),
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: b.labels(),
				},
				Spec: corev1.PodSpec{
					Affinity:         b.affinity(),
					RuntimeClassName: b.runtimeClass(),
					SecurityContext: &corev1.PodSecurityContext{
						RunAsNonRoot: &falseValue,
					},
					AutomountServiceAccountToken: &trueValue,
					InitContainers:               []corev1.Container{initContainer},
					Containers:                   []corev1.Container{b.container()},
					ImagePullSecrets:             b.secretsRefs,
					Volumes:                      append(b.volumesObjs, configVolume),
				},
			},
			VolumeClaimTemplates: b.pvcsObjs,
		},
	}

	return kdeployment, nil
}

func (b *statefulSet) Update(obj *appsv1.StatefulSet) (*appsv1.StatefulSet, error) { // nolint:golint,unparam
	obj.Labels = updateAkashLabels(obj.Labels, b.labels())
	obj.Spec.Replicas = b.replicas()
	obj.Spec.Selector.MatchLabels = b.selectorLabels()
	obj.Spec.Template.Labels = b.labels()
	obj.Spec.Template.Spec.Affinity = b.affinity()
	obj.Spec.Template.Spec.RuntimeClassName = b.runtimeClass()
	obj.Spec.Template.Spec.Containers = []corev1.Container{b.container()}
	obj.Spec.Template.Spec.ImagePullSecrets = b.imagePullSecrets()
	obj.Spec.VolumeClaimTemplates = b.persistentVolumeClaims()

	return obj, nil
}
