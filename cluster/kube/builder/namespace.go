package builder

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type NS interface {
	builderBase
	Create() (*corev1.Namespace, error)
	Update(obj *corev1.Namespace) (*corev1.Namespace, error)
}

type ns struct {
	builder
}

var _ NS = (*ns)(nil)

func BuildNS(settings Settings, deployment IClusterDeployment) NS {
	return &ns{builder: builder{settings: settings, deployment: deployment}}
}

func (b *ns) labels() map[string]string {
	return AppendLeaseLabels(b.deployment.LeaseID(), b.builder.labels())
}

func (b *ns) Create() (*corev1.Namespace, error) { // nolint:golint,unparam
	return &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name:   b.NS(),
			Labels: b.labels(),
		},
	}, nil
}

func (b *ns) Update(obj *corev1.Namespace) (*corev1.Namespace, error) { // nolint:golint,unparam
	obj.Name = b.NS()
	obj.Labels = b.labels()
	return obj, nil
}
