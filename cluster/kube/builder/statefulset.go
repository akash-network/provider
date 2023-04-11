package builder

import (
	mani "github.com/akash-network/akash-api/go/manifest/v2beta2"
	"github.com/tendermint/tendermint/libs/log"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	mtypes "github.com/akash-network/akash-api/go/node/market/v1beta3"

	crd "github.com/akash-network/provider/pkg/apis/akash.network/v2beta2"
)

type StatefulSet interface {
	workloadBase
	Create() (*appsv1.StatefulSet, error)
	Update(obj *appsv1.StatefulSet) (*appsv1.StatefulSet, error)
}

type statefulSet struct {
	workload
}

var _ StatefulSet = (*statefulSet)(nil)

func BuildStatefulSet(
	log log.Logger,
	settings Settings,
	lid mtypes.LeaseID,
	group *mani.Group,
	sparams crd.ParamsServices,
	serviceIdx int) StatefulSet {
	return &statefulSet{
		workload: newWorkloadBuilder(log, settings, lid, group, sparams, serviceIdx),
	}
}

func (b *statefulSet) Create() (*appsv1.StatefulSet, error) { // nolint:golint,unparam
	service := &b.group.Services[b.serviceIdx]
	params := &b.sparams[b.serviceIdx].Params

	replicas := int32(service.Count)
	falseValue := false

	// fixme b.runtimeClassName is updated on call to the container()
	containers := []corev1.Container{b.container()}

	var effectiveRuntimeClassName *string
	if len(params.RuntimeClass) != 0 && params.RuntimeClass != runtimeClassNoneValue {
		runtimeClass := params.RuntimeClass
		effectiveRuntimeClassName = &runtimeClass
	}

	kdeployment := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:   b.Name(),
			Labels: b.labels(),
		},
		Spec: appsv1.StatefulSetSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: b.labels(),
			},
			Replicas: &replicas,
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: b.labels(),
				},
				Spec: corev1.PodSpec{
					RuntimeClassName: effectiveRuntimeClassName,
					SecurityContext: &corev1.PodSecurityContext{
						RunAsNonRoot: &falseValue,
					},
					AutomountServiceAccountToken: &falseValue,
					Containers:                   containers,
					ImagePullSecrets:             b.imagePullSecrets(),
				},
			},
			VolumeClaimTemplates: b.persistentVolumeClaims(),
		},
	}

	return kdeployment, nil
}

func (b *statefulSet) Update(obj *appsv1.StatefulSet) (*appsv1.StatefulSet, error) { // nolint:golint,unparam
	service := &b.group.Services[b.serviceIdx]
	replicas := int32(service.Count)

	obj.Labels = b.labels()
	obj.Spec.Selector.MatchLabels = b.labels()
	obj.Spec.Replicas = &replicas
	obj.Spec.Template.Labels = b.labels()
	obj.Spec.Template.Spec.Containers = []corev1.Container{b.container()}
	obj.Spec.Template.Spec.ImagePullSecrets = b.imagePullSecrets()
	obj.Spec.VolumeClaimTemplates = b.persistentVolumeClaims()

	return obj, nil
}
