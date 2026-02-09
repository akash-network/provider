package builder

import (
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type RoleBinding interface {
	builderBase
	Create() (*rbacv1.RoleBinding, error)
	Update(obj *rbacv1.RoleBinding) (*rbacv1.RoleBinding, error)
}

type roleBinding struct {
	*Workload
}

var _ RoleBinding = (*roleBinding)(nil)

func BuildRoleBinding(workload *Workload) RoleBinding {
	return &roleBinding{
		Workload: workload,
	}
}

func (b *roleBinding) labels() map[string]string {
	return AppendLeaseLabels(b.deployment.LeaseID(), b.builder.labels())
}

func (b *roleBinding) Create() (*rbacv1.RoleBinding, error) {
	return &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:   b.Name(),
			Labels: b.labels(),
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "Role",
			Name:     b.Name(),
		},
		Subjects: []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				Name:      b.Name(),
				Namespace: b.NS(),
			},
		},
	}, nil
}

func (b *roleBinding) Update(obj *rbacv1.RoleBinding) (*rbacv1.RoleBinding, error) {
	uobj := obj.DeepCopy()
	uobj.Name = b.Name()
	uobj.Labels = updateAkashLabels(obj.Labels, b.labels())
	uobj.RoleRef = rbacv1.RoleRef{
		APIGroup: "rbac.authorization.k8s.io",
		Kind:     "Role",
		Name:     b.Name(),
	}
	uobj.Subjects = []rbacv1.Subject{
		{
			Kind:      "ServiceAccount",
			Name:      b.Name(),
			Namespace: b.NS(),
		},
	}
	return uobj, nil
}
