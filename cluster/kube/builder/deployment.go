package builder

import (
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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

func NewDeployment(workload Workload, svc Service) Deployment {
	ss := &deployment{
		Workload: workload,
	}

	ss.Workload.log = ss.Workload.log.With("object", "deployment", "service-name", ss.deployment.ManifestGroup().Services[ss.serviceIdx].Name)
	if svc != nil {
		ss.setService(svc.(*service))
	}

	return ss
}

func (b *deployment) Create() (*appsv1.Deployment, error) { // nolint:golint,unparam
	falseValue := false

	kdeployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:   b.Name(),
			Labels: b.labels(),
		},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: b.labels(),
			},
			Replicas: b.replicas(),
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
					ImagePullSecrets:             b.imagePullSecrets(),
					Volumes:                      b.volumes(),
				},
			},
		},
	}

	return kdeployment, nil
}

func (b *deployment) Update(obj *appsv1.Deployment) (*appsv1.Deployment, error) { // nolint:golint,unparam
	obj.Labels = b.labels()
	obj.Spec.Selector.MatchLabels = b.labels()
	obj.Spec.Replicas = b.replicas()
	obj.Spec.Template.Labels = b.labels()
	obj.Spec.Template.Spec.Affinity = b.affinity()
	obj.Spec.Template.Spec.RuntimeClassName = b.runtimeClass()
	obj.Spec.Template.Spec.Containers = []corev1.Container{b.container()}
	obj.Spec.Template.Spec.ImagePullSecrets = b.imagePullSecrets()

	return obj, nil
}
