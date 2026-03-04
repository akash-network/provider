package builder

import (
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// RoleBinding is a builder for creating or updating a Kubernetes RoleBinding.
//
// The builder always creates RoleBindings with:
//   - RoleRef pointing to a Role of the same name in the same namespace
//   - A single Subject referencing a ServiceAccount of the same name in the same namespace
//
// Note: Kubernetes does not allow updating RoleRef on an existing RoleBinding.
// If the RoleRef needs to change, the RoleBinding must be deleted and recreated.
type RoleBinding interface {
	builderBase
	Create() (*rbacv1.RoleBinding, error)
	Update(obj *rbacv1.RoleBinding) (*rbacv1.RoleBinding, error)
}

type roleBinding struct {
	*Workload
}

var _ RoleBinding = (*roleBinding)(nil)

// BuildRoleBinding creates a new RoleBinding builder for the given workload.
func BuildRoleBinding(workload *Workload) RoleBinding {
	return &roleBinding{
		Workload: workload,
	}
}

func (b *roleBinding) labels() map[string]string {
	return AppendLeaseLabels(b.deployment.LeaseID(), b.Workload.labels())
}

// Create returns a new RoleBinding with the name, labels, RoleRef, and Subjects
// derived from the builder's workload.
//
// The RoleBinding will reference a Role and ServiceAccount with the same name
// in the same namespace as the workload.
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

// Update modifies an existing RoleBinding by updating its labels to match the
// builder's workload. The RoleRef and Subjects are reset to their canonical
// values (pointing to a Role and ServiceAccount of the same name).
//
// Note: Since Kubernetes RoleRef is immutable, this method will fail at the
// API level if the caller attempts to change the RoleRef. The only meaningful
// change this method can apply is to labels.
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
