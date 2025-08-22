package builder

import (
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

type StatefulSet interface {
	workloadBase
	Create() (*appsv1.StatefulSet, error)
	Update(obj *appsv1.StatefulSet) (*appsv1.StatefulSet, error)
}

type statefulSet struct {
	*Workload
}

var _ StatefulSet = (*statefulSet)(nil)

func BuildStatefulSet(workload *Workload) StatefulSet {
	ss := &statefulSet{
		Workload: workload,
	}

	ss.log = ss.log.With("object", "statefulset", "service-name", ss.deployment.ManifestGroup().Services[ss.serviceIdx].Name)

	return ss
}

func (b *statefulSet) Create() (*appsv1.StatefulSet, error) { // nolint:unparam
	falseValue := false

	revisionHistoryLimit := int32(1)

	partition := int32(0)
	maxUnavailable := intstr.FromInt32(1)

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
					AutomountServiceAccountToken: &falseValue,
					Containers:                   []corev1.Container{b.container()},
					ImagePullSecrets:             b.secretsRefs,
					Volumes:                      b.volumesObjs,
				},
			},
			VolumeClaimTemplates: b.pvcsObjs,
		},
	}

	return kdeployment, nil
}

func (b *statefulSet) Update(obj *appsv1.StatefulSet) (*appsv1.StatefulSet, error) { // nolint:unparam
	uobj := obj.DeepCopy()

	uobj.Labels = updateAkashLabels(obj.Labels, b.labels())
	uobj.Spec.Replicas = b.replicas()
	uobj.Spec.Selector.MatchLabels = b.selectorLabels()
	uobj.Spec.Template.Labels = b.labels()
	uobj.Spec.Template.Spec.Affinity = b.affinity()
	uobj.Spec.Template.Spec.RuntimeClassName = b.runtimeClass()
	uobj.Spec.Template.Spec.Containers = []corev1.Container{b.container()}
	uobj.Spec.Template.Spec.ImagePullSecrets = b.secretsRefs
	uobj.Spec.Template.Spec.Volumes = b.volumesObjs
	uobj.Spec.VolumeClaimTemplates = b.pvcsObjs

	return uobj, nil
}
