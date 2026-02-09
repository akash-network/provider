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

func (b *role) buildPolicyRules() []rbacv1.PolicyRule {
	permissions := b.GetPermissions()
	if len(permissions) == 0 {
		return nil
	}

	var rules []rbacv1.PolicyRule
	seen := make(map[string]bool)

	for _, resource := range permissions {
		if seen[resource] {
			continue
		}
		seen[resource] = true

		switch resource {
		case "logs":
			rules = append(rules,
				rbacv1.PolicyRule{
					APIGroups: []string{""},
					Resources: []string{"pods"},
					Verbs:     []string{"get", "list", "watch"},
				},
				rbacv1.PolicyRule{
					APIGroups: []string{""},
					Resources: []string{"pods/log"},
					Verbs:     []string{"get", "list"},
				},
				rbacv1.PolicyRule{
					APIGroups: []string{"apps"},
					Resources: []string{"deployments"},
					Verbs:     []string{"get", "list", "watch"},
				},
			)
		case "events":
			rules = append(rules,
				rbacv1.PolicyRule{
					APIGroups: []string{""},
					Resources: []string{"events"},
					Verbs:     []string{"get", "list", "watch"},
				},
			)
		}
	}

	return rules
}

func (b *role) Create() (*rbacv1.Role, error) {
	return &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Name:   b.Name(),
			Labels: b.labels(),
		},
		Rules: b.buildPolicyRules(),
	}, nil
}

func (b *role) Update(obj *rbacv1.Role) (*rbacv1.Role, error) {
	uobj := obj.DeepCopy()
	uobj.Name = b.Name()
	uobj.Labels = updateAkashLabels(obj.Labels, b.labels())
	uobj.Rules = b.buildPolicyRules()
	return uobj, nil
}
