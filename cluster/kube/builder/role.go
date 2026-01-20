package builder

import (
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type Role interface {
	builderBase
	Create() (*rbacv1.Role, error)
	Update(obj *rbacv1.Role) (*rbacv1.Role, error)
}

type role struct {
	*Workload
}

var _ Role = (*role)(nil)

func BuildRole(workload *Workload) Role {
	return &role{
		Workload: workload,
	}
}

func (b *role) labels() map[string]string {
	return AppendLeaseLabels(b.deployment.LeaseID(), b.builder.labels())
}

func (b *role) Create() (*rbacv1.Role, error) {
	return &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Name:   b.Name(),
			Labels: b.labels(),
		},
		Rules: []rbacv1.PolicyRule{
			{
				APIGroups: []string{""},
				Resources: []string{"pods"},
				Verbs:     []string{"get", "list", "watch"},
			},
			{
				APIGroups: []string{""},
				Resources: []string{"pods/log"},
				Verbs:     []string{"get", "list"},
			},
		},
	}, nil
}

func (b *role) Update(obj *rbacv1.Role) (*rbacv1.Role, error) {
	uobj := obj.DeepCopy()
	uobj.Name = b.Name()
	uobj.Labels = updateAkashLabels(obj.Labels, b.labels())
	uobj.Rules = []rbacv1.PolicyRule{
		{
			APIGroups: []string{""},
			Resources: []string{"pods"},
			Verbs:     []string{"get", "list", "watch"},
		},
		{
			APIGroups: []string{""},
			Resources: []string{"pods/log"},
			Verbs:     []string{"get", "list"},
		},
	}
	return uobj, nil
}
