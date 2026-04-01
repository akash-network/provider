package gateway

import (
	"fmt"
	"sort"

	"cosmossdk.io/log"
)

// Registry manages available Gateway API providers.
// Providers register themselves via the Register method,
// typically during package initialization.
type Registry struct {
	providers map[string]func(log.Logger) GatewayProvider
}

var defaultRegistry = &Registry{
	providers: make(map[string]func(log.Logger) GatewayProvider),
}

func init() {
	// Register NGINX Gateway Fabric as the default provider
	defaultRegistry.Register("nginx", NewNginxGateway)
}

// Register adds a Gateway provider factory to the registry.
// The factory function takes a logger and returns a GatewayProvider instance.
func (r *Registry) Register(name string, factory func(log.Logger) GatewayProvider) {
	r.providers[name] = factory
}

// Get retrieves a Gateway provider by name.
// Returns an error if the provider is not registered.
func (r *Registry) Get(name string, logger log.Logger) (GatewayProvider, error) {
	factory, exists := r.providers[name]
	if !exists {
		return nil, fmt.Errorf("unknown gateway provider: %s (supported: %v)",
			name, r.SupportedNames())
	}
	return factory(logger), nil
}

// SupportedNames returns a sorted list of all registered provider names.
func (r *Registry) SupportedNames() []string {
	names := make([]string, 0, len(r.providers))
	for name := range r.providers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// GetProvider retrieves a Gateway provider from the default registry.
// This is the primary entry point for obtaining Gateway providers.
func GetProvider(name string, logger log.Logger) (GatewayProvider, error) {
	return defaultRegistry.Get(name, logger)
}
