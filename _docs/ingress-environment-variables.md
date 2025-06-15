# Ingress Environment Variables

Akash Provider automatically injects ingress-related environment variables into deployment containers, enabling applications to discover their external endpoints programmatically.

## Environment Variables

### AKASH_INGRESS_URIS

Comma-separated list of all accessible ingress URIs for the deployment.

**Format:** `protocol://hostname[:port][,...]`

**Examples:**

```bash
# Single endpoint
AKASH_INGRESS_URIS="http://abc123.ingress.example.com"

# Multiple endpoints with mixed protocols/ports
AKASH_INGRESS_URIS="http://abc123.ingress.akash.network,https://myapp.example.com:8443"
```

**Includes:**

- Provider-generated static hostnames (when enabled)
- Custom hostnames from SDL `expose.accept.hosts`
- Auto-detected protocols (HTTP/HTTPS based on port 443)
- Port numbers (omitted for standard 80/443)

### AKASH_PROVIDER_INGRESS

Provider's public hostname or IP address for nodePort services, health checks, and inter-service communication.

**Examples:**

```bash
AKASH_PROVIDER_INGRESS="provider.akash.network"
AKASH_PROVIDER_INGRESS="192.168.1.100"
```

## SDL Examples

### Basic HTTP Service

```yaml
services:
  web:
    image: nginx:latest
    expose:
      - port: 80
        as: 80
        proto: http
        accept:
          - "myapp.example.com"
        to:
          - global: true
```

**Result:**

```bash
AKASH_INGRESS_URIS="http://abc123.ingress.provider.com,http://myapp.example.com"
AKASH_PROVIDER_INGRESS="provider.akash.network"
```

### HTTPS Service

```yaml
services:
  secure-web:
    image: nginx:latest
    expose:
      - port: 443
        as: 443
        proto: tcp
        accept:
          - "secure.example.com"
        to:
          - global: true
```

**Result:**

```bash
AKASH_INGRESS_URIS="https://def456.ingress.provider.com,https://secure.example.com"
```

### Custom Ports

```yaml
services:
  api:
    image: node:latest
    expose:
      - port: 3000
        as: 8080
        proto: http
        accept:
          - "api.example.com"
        to:
          - global: true
```

**Result:**

```bash
AKASH_INGRESS_URIS="http://ghi789.ingress.provider.com:8080,http://api.example.com:8080"
```

## Application Usage

### Go Example

```go
package main

import (
    "os"
    "strings"
    "fmt"
)

func main() {
    // Parse ingress URIs
    ingressURIs := []string{}
    if uris := os.Getenv("AKASH_INGRESS_URIS"); uris != "" {
        for _, uri := range strings.Split(uris, ",") {
            ingressURIs = append(ingressURIs, strings.TrimSpace(uri))
        }
    }

    providerIngress := os.Getenv("AKASH_PROVIDER_INGRESS")

    fmt.Printf("Available endpoints: %v\n", ingressURIs)
    fmt.Printf("Provider: %s\n", providerIngress)

    // Use for CORS, webhooks, service discovery, etc.
}
```

### Use Cases

- **Service Discovery**: Applications discover their own endpoints
- **CORS Configuration**: Dynamic allowed origins setup
- **Webhook URLs**: Generate callback URLs for external services
- **Health Checks**: Build monitoring endpoints
- **Inter-service Communication**: Connect services within provider

## Implementation Details

**Location:** [`cluster/kube/builder/workload.go`](../cluster/kube/builder/workload.go)

**Process:**

1. Scans all deployment services for ingress-capable configurations
2. Auto-detects protocols (HTTP for most ports, HTTPS for port 443)
3. Includes provider-generated static hosts and custom SDL hosts
4. Formats URLs with proper port handling
5. Injects variables during container creation

**Testing:** [`cluster/kube/builder/deployment_test.go`](../cluster/kube/builder/deployment_test.go)

- `TestDeploySetsEnvironmentVariables`
- `TestDeploySetsIngressEnvironmentVariables`

## Limitations & Requirements

**Limitations:**

- URLs calculated at deployment time (static)
- Protocol detection limited to HTTP/HTTPS by port
- Requires deployment restart for ingress changes

**Requirements:**

- Provider must support static host generation (`DeploymentIngressStaticHosts`)
- No configuration changes needed for existing providers

**Compatibility:**

- Fully backward-compatible
- Optional usage - applications can ignore variables
- Works with existing SDL configurations

## Future Enhancements

- Runtime environment variable updates
- Enhanced protocol detection beyond port-based
- Custom protocol support
- Ingress filtering options

The feature is production-ready and provides a solid foundation for dynamic application configuration in Akash deployments.
