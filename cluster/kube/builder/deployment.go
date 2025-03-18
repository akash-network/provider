package builder

import (
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

type Deployment interface {
	workloadBase
	Create() (*appsv1.Deployment, error)
	Update(obj *appsv1.Deployment) (*appsv1.Deployment, error)
}

type deployment struct {
	Workload
}

var _ Deployment = (*deployment)(nil)

func NewDeployment(workload Workload) Deployment {
	ss := &deployment{
		Workload: workload,
	}

	ss.Workload.log = ss.Workload.log.With("object", "deployment", "service-name", ss.deployment.ManifestGroup().Services[ss.serviceIdx].Name)

	return ss
}

func (b *deployment) Create() (*appsv1.Deployment, error) { // nolint:golint,unparam
	falseValue := false

	revisionHistoryLimit := int32(10)

	maxSurge := intstr.FromInt32(0)
	maxUnavailable := intstr.FromInt32(1)

	kdeployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:   b.Name(),
			Labels: b.labels(),
		},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: b.selectorLabels(),
			},
			Strategy: appsv1.DeploymentStrategy{
				Type: appsv1.RollingUpdateDeploymentStrategyType,
				RollingUpdate: &appsv1.RollingUpdateDeployment{
					MaxUnavailable: &maxUnavailable,
					MaxSurge:       &maxSurge,
				},
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
					AutomountServiceAccountToken: &falseValue,
					Containers:                   []corev1.Container{b.container()},
					ImagePullSecrets:             b.secretsRefs,
					Volumes:                      b.volumesObjs,
				},
			},
		},
	}

	return kdeployment, nil
}

func (b *deployment) Update(obj *appsv1.Deployment) (*appsv1.Deployment, error) { // nolint:golint,unparam
	uobj := obj.DeepCopy()

	uobj.Labels = updateAkashLabels(obj.Labels, b.labels())
	uobj.Spec.Selector.MatchLabels = b.selectorLabels()
	uobj.Spec.Replicas = b.replicas()
	uobj.Spec.Template.Labels = b.labels()
	uobj.Spec.Template.Spec.Affinity = b.affinity()
	uobj.Spec.Template.Spec.RuntimeClassName = b.runtimeClass()
	uobj.Spec.Template.Spec.Containers = []corev1.Container{b.container()}
	uobj.Spec.Template.Spec.ImagePullSecrets = b.secretsRefs
	uobj.Spec.Template.Spec.Volumes = b.volumesObjs

	return uobj, nil
}
