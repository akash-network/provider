package builder

import (
	"testing"

	"github.com/stretchr/testify/require"
	rbacv1 "k8s.io/api/rbac/v1"
)

func TestWorkloadPermissions_HasAny(t *testing.T) {
	tests := []struct {
		name     string
		perms    *WorkloadPermissions
		expected bool
	}{
		{
			name:     "nil permissions returns false",
			perms:    nil,
			expected: false,
		},
		{
			name:     "empty permissions returns false",
			perms:    &WorkloadPermissions{},
			expected: false,
		},
		{
			name: "only read permissions returns true",
			perms: &WorkloadPermissions{
				Read: []string{"logs"},
			},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.perms.HasAny()
			require.Equal(t, tt.expected, result)
		})
	}
}

func TestWorkloadPermissions_HasRead(t *testing.T) {
	tests := []struct {
		name     string
		perms    *WorkloadPermissions
		expected bool
	}{
		{
			name:     "nil permissions returns false",
			perms:    nil,
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.perms.HasRead()
			require.Equal(t, tt.expected, result)
		})
	}
}

func TestWorkloadPermissions_GetByType(t *testing.T) {
	perms := &WorkloadPermissions{
		Read: []string{"logs", "events"},
	}

	tests := []struct {
		name     string
		permType PermissionType
		expected []string
	}{
		{
			name:     "get read permissions",
			permType: PermissionTypeRead,
			expected: []string{"logs", "events"},
		},
		{
			name:     "unknown type returns nil",
			permType: PermissionType("unknown"),
			expected: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := perms.GetByType(tt.permType)
			require.Equal(t, tt.expected, result)
		})
	}
}

func TestPolicyRuleRegistry_GenerateRules(t *testing.T) {
	registry := NewPolicyRuleRegistry()

	t.Run("nil permissions returns nil", func(t *testing.T) {
		rules := registry.GenerateRules(nil)
		require.Nil(t, rules)
	})

	t.Run("empty permissions returns nil", func(t *testing.T) {
		rules := registry.GenerateRules(&WorkloadPermissions{})
		require.Nil(t, rules)
	})

	t.Run("logs read permission generates correct rules", func(t *testing.T) {
		perms := &WorkloadPermissions{
			Read: []string{"logs"},
		}
		rules := registry.GenerateRules(perms)

		require.Len(t, rules, 3)

		require.Contains(t, rules[0].Resources, "pods")
		require.Contains(t, rules[0].Verbs, "get")
		require.Contains(t, rules[0].Verbs, "list")
		require.Contains(t, rules[0].Verbs, "watch")

		require.Contains(t, rules[1].Resources, "pods/log")
		require.Contains(t, rules[1].Verbs, "get")

		require.Contains(t, rules[2].Resources, "deployments")
		require.Equal(t, []string{"apps"}, rules[2].APIGroups)
	})

	t.Run("duplicate resources are deduplicated", func(t *testing.T) {
		perms := &WorkloadPermissions{
			Read: []string{"logs", "logs", "logs"},
		}
		rules := registry.GenerateRules(perms)

		// Should only generate rules once for logs
		require.Len(t, rules, 3)
	})

	t.Run("unknown resource generates no rules", func(t *testing.T) {
		perms := &WorkloadPermissions{
			Read: []string{"unknown-resource"},
		}
		rules := registry.GenerateRules(perms)

		require.Len(t, rules, 0)
	})
}

func TestPolicyRuleRegistry_Register(t *testing.T) {
	registry := NewPolicyRuleRegistry()

	// Register a custom resource mapping
	registry.Register(ResourcePolicyMapping{
		Name: "custom-resource",
		Generator: func(resource string, permType PermissionType) []rbacv1.PolicyRule {
			if permType == PermissionTypeRead {
				return []rbacv1.PolicyRule{
					{
						APIGroups: []string{"custom.io"},
						Resources: []string{"customs"},
						Verbs:     []string{"get"},
					},
				}
			}
			return nil
		},
	})

	perms := &WorkloadPermissions{
		Read: []string{"custom-resource"},
	}
	rules := registry.GenerateRules(perms)

	require.Len(t, rules, 1)
	require.Equal(t, []string{"custom.io"}, rules[0].APIGroups)
	require.Contains(t, rules[0].Resources, "customs")
}
