package manifest

import (
	"fmt"

	maniv2beta2 "pkg.akt.dev/go/manifest/v2beta3"
)

// Upper bounds on tenant-supplied nginx proxy buffer options. These render into the
// shared gateway data plane (NGF SnippetsFilter or ingress-nginx annotations) and
// nginx allocates the buffer pool per proxied connection, so without a cap one tenant
// could force the shared gateway to allocate arbitrary memory per connection and
// starve or OOM every co-tenant. All sizes are bytes.
const (
	maxProxyBufferSize      = 128 * 1024
	maxProxyBuffersNumber   = 16
	maxProxyBuffersSize     = 128 * 1024
	maxProxyBusyBuffersSize = 256 * 1024
)

// validateProxyBufferLimits rejects a manifest whose proxy buffer options exceed the
// provider maxima, before it is ever rendered into the shared gateway config. The
// returned error is tagged ErrInvalidManifest so the gateway reports it as a 4xx.
func validateProxyBufferLimits(m maniv2beta2.Manifest) error {
	overLimit := func(svc string, field string, got, max uint32) error {
		return fmt.Errorf("%w: service %q proxy.%s %d exceeds max %d",
			maniv2beta2.ErrInvalidManifest, svc, field, got, max)
	}

	for _, group := range m.GetGroups() {
		for _, service := range group.Services {
			for _, expose := range service.Expose {
				p := expose.HTTPOptions.Proxy
				if p == nil {
					continue
				}
				switch {
				case p.BufferSize > maxProxyBufferSize:
					return overLimit(service.Name, "buffer_size", p.BufferSize, maxProxyBufferSize)
				case p.BuffersNumber > maxProxyBuffersNumber:
					return overLimit(service.Name, "buffers_number", p.BuffersNumber, maxProxyBuffersNumber)
				case p.BuffersSize > maxProxyBuffersSize:
					return overLimit(service.Name, "buffers_size", p.BuffersSize, maxProxyBuffersSize)
				case p.BusyBuffersSize > maxProxyBusyBuffersSize:
					return overLimit(service.Name, "busy_buffers_size", p.BusyBuffersSize, maxProxyBusyBuffersSize)
				}
			}
		}
	}

	return nil
}
