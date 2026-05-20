package bidengine

import (
	clientmocks "pkg.akt.dev/go/mocks/node/client"
	vtypes "pkg.akt.dev/go/node/verification/v1"
)

type bidengineTestQueryClient struct {
	*clientmocks.QueryClient
}

func (bidengineTestQueryClient) Verification() vtypes.QueryClient {
	panic("unexpected verification query in bidengine test")
}
