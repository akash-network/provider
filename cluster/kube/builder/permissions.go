package builder

import (
	rbacv1 "k8s.io/api/rbac/v1"
)

// PermissionType represents the type of permission action (read, write, delete)
type PermissionType string

const (
	PermissionTypeRead PermissionType = "read"
)

// WorkloadPermissions holds permissions organized by type for a workload.
// This structure supports Read permissions.
type WorkloadPermissions struct {
	Read []string
}

// HasAny returns true if any permissions are defined
func (p *WorkloadPermissions) HasAny() bool {
	if p == nil {
		return false
	}
	return len(p.Read) > 0
}

// HasRead returns true if any read permissions are defined
func (p *WorkloadPermissions) HasRead() bool {
	if p == nil {
		return false
	}
	return len(p.Read) > 0
}

// GetByType returns the permissions for a given type
func (p *WorkloadPermissions) GetByType(permType PermissionType) []string {
	if p == nil {
		return nil
	}
	switch permType {
	case PermissionTypeRead:
		return p.Read
	default:
		return nil
	}
}

// PolicyRuleGenerator generates Kubernetes RBAC policy rules for a given resource
type PolicyRuleGenerator func(resource string, permType PermissionType) []rbacv1.PolicyRule

// ResourcePolicyMapping defines the policy rules for a specific resource type.
// This allows easy extension when new resources need to be supported.
type ResourcePolicyMapping struct {
	// Name is the resource identifier (e.g., "logs", "events")
	Name string
	// Generator creates the policy rules for this resource based on permission type
	Generator PolicyRuleGenerator
}

// PolicyRuleRegistry holds all registered resource policy mappings
type PolicyRuleRegistry struct {
	mappings map[string]ResourcePolicyMapping
}

// NewPolicyRuleRegistry creates a new registry with default resource mappings
func NewPolicyRuleRegistry() *PolicyRuleRegistry {
	registry := &PolicyRuleRegistry{
		mappings: make(map[string]ResourcePolicyMapping),
	}

	// Register default resource handlers
	registry.Register(logsResourceMapping())
	// TODO: enable events in a different PR and test them separately.
	// registry.Register(eventsResourceMapping())

	return registry
}

// Register adds a new resource policy mapping to the registry
func (r *PolicyRuleRegistry) Register(mapping ResourcePolicyMapping) {
	r.mappings[mapping.Name] = mapping
}

// GenerateRules generates all policy rules for the given permissions
func (r *PolicyRuleRegistry) GenerateRules(permissions *WorkloadPermissions) []rbacv1.PolicyRule {
	if permissions == nil {
		return nil
	}

	var rules []rbacv1.PolicyRule
	seen := make(map[string]map[PermissionType]bool)

	// Process each permission type
	for _, permConfig := range []struct {
		permType  PermissionType
		resources []string
	}{
		{PermissionTypeRead, permissions.Read},
	} {
		for _, resource := range permConfig.resources {
			// Initialize the inner map if needed
			if seen[resource] == nil {
				seen[resource] = make(map[PermissionType]bool)
			}

			// Skip if we've already processed this resource+permType combination
			if seen[resource][permConfig.permType] {
				continue
			}
			seen[resource][permConfig.permType] = true

			// Look up the mapping and generate rules
			if mapping, ok := r.mappings[resource]; ok {
				rules = append(rules, mapping.Generator(resource, permConfig.permType)...)
			}
		}
	}

	return rules
}

// logsResourceMapping returns the policy mapping for "logs" resource
func logsResourceMapping() ResourcePolicyMapping {
	return ResourcePolicyMapping{
		Name: "logs",
		Generator: func(resource string, permType PermissionType) []rbacv1.PolicyRule {
			switch permType {
			case PermissionTypeRead:
				return []rbacv1.PolicyRule{
					{
						APIGroups: []string{""},
						Resources: []string{"pods"},
						Verbs:     []string{"get", "list", "watch"},
					},
					{
						APIGroups: []string{""},
						Resources: []string{"pods/log"},
						Verbs:     []string{"get"},
					},
					{
						APIGroups: []string{"apps"},
						Resources: []string{"deployments"},
						Verbs:     []string{"get", "list", "watch"},
					},
				}
			default:
				return nil
			}
		},
	}
}

// TODO: enable events in a different PR and test them separately.
// eventsResourceMapping returns the policy mapping for "events" resource
// func eventsResourceMapping() ResourcePolicyMapping {
// 	return ResourcePolicyMapping{
// 		Name: "events",
// 		Generator: func(resource string, permType PermissionType) []rbacv1.PolicyRule {
// 			switch permType {
// 			case PermissionTypeRead:
// 				return []rbacv1.PolicyRule{
// 					{
// 						APIGroups: []string{""},
// 						Resources: []string{"events"},
// 						Verbs:     []string{"get", "list", "watch"},
// 					},
// 				}
// 			case PermissionTypeWrite:
// 				// Future: Add write rules for events when implemented
// 				return nil
// 			case PermissionTypeDelete:
// 				// Future: Add delete rules for events when implemented
// 				return nil
// 			default:
// 				return nil
// 			}
// 		},
// 	}
// }
