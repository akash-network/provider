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
	registry *PolicyRuleRegistry
}

var _ Role = (*role)(nil)

// BuildRole creates a new Role builder for the given workload.
// It uses the default PolicyRuleRegistry which includes mappings for
// all supported resource types (logs, events, etc.).
func BuildRole(workload *Workload) Role {
	return &role{
		Workload: workload,
		registry: NewPolicyRuleRegistry(),
	}
}

// BuildRoleWithRegistry creates a new Role builder with a custom PolicyRuleRegistry.
// This is useful for testing or for cases where custom resource mappings are needed.
func BuildRoleWithRegistry(workload *Workload, registry *PolicyRuleRegistry) Role {
	return &role{
		Workload: workload,
		registry: registry,
	}
}

func (b *role) labels() map[string]string {
	return AppendLeaseLabels(b.deployment.LeaseID(), b.Workload.labels())
}

// buildPolicyRules generates Kubernetes RBAC policy rules based on the
// workload permissions. It uses the PolicyRuleRegistry to map resource
// types (logs, events, etc.) to their corresponding RBAC rules.
//
// The registry pattern allows easy extension when new permission types
// (write, delete) or new resources are added in the future.
func (b *role) buildPolicyRules() []rbacv1.PolicyRule {
	permissions := b.GetWorkloadPermissions()
	if permissions == nil || !permissions.HasAny() {
		return nil
	}

	return b.registry.GenerateRules(permissions)
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
