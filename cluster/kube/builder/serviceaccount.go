package builder

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type ServiceAccount interface {
	builderBase
	Create() (*corev1.ServiceAccount, error)
	Update(obj *corev1.ServiceAccount) (*corev1.ServiceAccount, error)
}

type serviceAccount struct {
	*Workload
}

var _ ServiceAccount = (*serviceAccount)(nil)

func BuildServiceAccount(workload *Workload) ServiceAccount {
	return &serviceAccount{
		Workload: workload,
	}
}

func (b *serviceAccount) labels() map[string]string {
	return AppendLeaseLabels(b.deployment.LeaseID(), b.builder.labels())
}

func (b *serviceAccount) Create() (*corev1.ServiceAccount, error) {
	return &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:   b.Name(),
			Labels: b.labels(),
		},
	}, nil
}

func (b *serviceAccount) Update(obj *corev1.ServiceAccount) (*corev1.ServiceAccount, error) {
	uobj := obj.DeepCopy()
	uobj.Name = b.Name()
	uobj.Labels = updateAkashLabels(obj.Labels, b.labels())
	return uobj, nil
}
