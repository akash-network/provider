package gateway

import (
	"context"
	"fmt"
	"sort"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

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
	defaultRegistry.Register("nginx", NewNginxGateway)
	defaultRegistry.Register("envoy", NewEnvoyGateway)
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

// AutoProvider selects the implementation from the Gateway's GatewayClass.
const AutoProvider = "auto"

var (
	// GatewayGVR is the GroupVersionResource for Gateway API Gateways.
	GatewayGVR = schema.GroupVersion(gatewayv1.GroupVersion).WithResource("gateways")
	// GatewayClassGVR is the GroupVersionResource for Gateway API GatewayClasses.
	GatewayClassGVR = schema.GroupVersion(gatewayv1.GroupVersion).WithResource("gatewayclasses")

	// controllerProviders maps a GatewayClass controllerName to its provider.
	controllerProviders = map[string]string{
		nginxControllerName: "nginx",
		envoyControllerName: "envoy",
	}
)

// DetectProvider returns the provider name for the controller that owns the
// Gateway's GatewayClass. Any controller without a registered provider is an error.
func DetectProvider(ctx context.Context, dc dynamic.Interface, gatewayNamespace, gatewayName string) (string, error) {
	gw, err := dc.Resource(GatewayGVR).Namespace(gatewayNamespace).Get(ctx, gatewayName, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("gateway provider detection: get gateway %s/%s: %w", gatewayNamespace, gatewayName, err)
	}

	className, _, _ := unstructured.NestedString(gw.Object, "spec", "gatewayClassName")
	if className == "" {
		return "", fmt.Errorf("gateway provider detection: gateway %s/%s has no gatewayClassName", gatewayNamespace, gatewayName)
	}

	class, err := dc.Resource(GatewayClassGVR).Get(ctx, className, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("gateway provider detection: get gatewayclass %q: %w", className, err)
	}

	controller, _, _ := unstructured.NestedString(class.Object, "spec", "controllerName")
	name, ok := controllerProviders[controller]
	if !ok {
		return "", fmt.Errorf("gateway provider detection: gatewayclass %q uses unsupported controller %q", className, controller)
	}

	return name, nil
}

// ResolveProvider returns the named provider, or detects it from the Gateway's
// GatewayClass when name is AutoProvider or empty. Detection reads the cluster once;
// callers resolve at startup, so a class switch takes effect on their next restart.
func ResolveProvider(ctx context.Context, dc dynamic.Interface, name, gatewayNamespace, gatewayName string, logger log.Logger) (GatewayProvider, error) {
	if name == "" || name == AutoProvider {
		detected, err := DetectProvider(ctx, dc, gatewayNamespace, gatewayName)
		if err != nil {
			return nil, err
		}
		name = detected
	}

	return GetProvider(name, logger)
}
