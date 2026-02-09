package gateway

import (
	"fmt"
	"sort"

	"cosmossdk.io/log"
)

// Registry manages available Gateway API implementations.
// Implementations register themselves via the Register method,
// typically during package initialization.
type Registry struct {
	implementations map[string]func(log.Logger) Implementation
}

var defaultRegistry = &Registry{
	implementations: make(map[string]func(log.Logger) Implementation),
}

func init() {
	// Register NGINX Gateway Fabric as the default implementation
	defaultRegistry.Register("nginx", NewNginxGateway)
}

// Register adds a Gateway implementation factory to the registry.
// The factory function takes a logger and returns an Implementation instance.
func (r *Registry) Register(name string, factory func(log.Logger) Implementation) {
	r.implementations[name] = factory
}

// Get retrieves a Gateway implementation by name.
// Returns an error if the implementation is not registered.
func (r *Registry) Get(name string, logger log.Logger) (Implementation, error) {
	factory, exists := r.implementations[name]
	if !exists {
		return nil, fmt.Errorf("unknown gateway implementation: %s (supported: %v)",
			name, r.SupportedNames())
	}
	return factory(logger), nil
}

// SupportedNames returns a sorted list of all registered implementation names.
func (r *Registry) SupportedNames() []string {
	names := make([]string, 0, len(r.implementations))
	for name := range r.implementations {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// GetImplementation retrieves a Gateway implementation from the default registry.
// This is the primary entry point for obtaining Gateway implementations.
func GetImplementation(name string, logger log.Logger) (Implementation, error) {
	return defaultRegistry.Get(name, logger)
}

