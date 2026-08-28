package manifest

import (
	"testing"

	"github.com/stretchr/testify/require"

	maniv2beta2 "pkg.akt.dev/go/manifest/v2beta3"
)

func manifestWithProxy(p *maniv2beta2.ProxyOptions) maniv2beta2.Manifest {
	return maniv2beta2.Manifest{
		maniv2beta2.Group{
			Services: []maniv2beta2.Service{{
				Name: "web",
				Expose: []maniv2beta2.ServiceExpose{{
					HTTPOptions: maniv2beta2.ServiceExposeHTTPOptions{Proxy: p},
				}},
			}},
		},
	}
}

func TestValidateProxyBufferLimits(t *testing.T) {
	require.NoError(t, validateProxyBufferLimits(manifestWithProxy(nil)))

	require.NoError(t, validateProxyBufferLimits(manifestWithProxy(&maniv2beta2.ProxyOptions{
		BufferSize:      16384,
		BuffersNumber:   8,
		BuffersSize:     16384,
		BusyBuffersSize: 32768,
	})))

	overCap := []*maniv2beta2.ProxyOptions{
		{BufferSize: maxProxyBufferSize + 1},
		{BuffersNumber: maxProxyBuffersNumber + 1},
		{BuffersSize: maxProxyBuffersSize + 1},
		{BusyBuffersSize: maxProxyBusyBuffersSize + 1},
		// the DoS scenario from the review
		{BuffersNumber: 100000, BuffersSize: 1000000},
	}
	for _, p := range overCap {
		err := validateProxyBufferLimits(manifestWithProxy(p))
		require.Error(t, err)
		require.ErrorIs(t, err, maniv2beta2.ErrInvalidManifest)
	}
}
