//go:build e2e

package integration

import (
	"context"
	"crypto/tls"
	"fmt"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/protobuf/types/known/emptypb"

	sdk "github.com/cosmos/cosmos-sdk/types"

	dvbeta "pkg.akt.dev/go/node/deployment/v1beta4"
	attrtypes "pkg.akt.dev/go/node/types/attributes/v1"
	rtypes "pkg.akt.dev/go/node/types/resources/v1beta4"
	providerv1 "pkg.akt.dev/go/provider/v1"
)

type E2EBidScreening struct {
	IntegrationTestSuite
}

const (
	mi = 1024 * 1024
	gi = 1024 * 1024 * 1024
)

func (s *E2EBidScreening) dialGRPC() (*grpc.ClientConn, providerv1.ProviderRPCClient) {
	tlsCfg := &tls.Config{
		InsecureSkipVerify: true,
		MinVersion:         tls.VersionTLS13,
	}

	conn, err := grpc.DialContext(s.ctx, s.grpcHost,
		grpc.WithTransportCredentials(credentials.NewTLS(tlsCfg)),
	)
	s.Require().NoError(err)

	return conn, providerv1.NewProviderRPCClient(conn)
}

func (s *E2EBidScreening) TestBidScreening() {
	conn, client := s.dialGRPC()
	defer conn.Close()

	tests := []struct {
		name    string
		req     *providerv1.BidScreeningRequest
		pass    bool
		reason  string // substring expected in reasons (if non-empty)
	}{
		{
			name: "valid_small_deployment",
			req: &providerv1.BidScreeningRequest{
				GroupSpec: makeGroupSpec("test", 100, 0, 16*mi, 128*mi, 1, 1000),
			},
			pass: true,
		},
		{
			name:   "nil_group_spec",
			req:    &providerv1.BidScreeningRequest{},
			pass:   false,
			reason: "missing group spec",
		},
		{
			name: "empty_resources",
			req: &providerv1.BidScreeningRequest{
				GroupSpec: &dvbeta.GroupSpec{Name: "empty", Resources: dvbeta.ResourceUnits{}},
			},
			pass: false,
		},
		{
			name: "exceeds_cpu_capacity",
			req: &providerv1.BidScreeningRequest{
				GroupSpec: makeGroupSpec("big-cpu", 200000, 0, 16*mi, 128*mi, 1, 1000),
			},
			pass:   false,
			reason: "insufficient capacity",
		},
		{
			name: "exceeds_memory_capacity",
			req: &providerv1.BidScreeningRequest{
				GroupSpec: makeGroupSpec("big-mem", 100, 0, 200*gi, 128*mi, 1, 1000),
			},
			pass:   false,
			reason: "insufficient capacity",
		},
		{
			name: "gpu_not_available",
			req: &providerv1.BidScreeningRequest{
				GroupSpec: makeGPUGroupSpec("gpu-test", 100, 1, 256*mi, 1*gi, 1, 1000),
			},
			pass:   false,
			reason: "incompatible attributes for resources requirements",
		},
		{
			name: "multiple_replicas_within_capacity",
			req: &providerv1.BidScreeningRequest{
				GroupSpec: makeGroupSpec("replicas", 100, 0, 16*mi, 128*mi, 3, 3000),
			},
			pass: true,
		},
		{
			name: "multiple_replicas_exceeds_capacity",
			req: &providerv1.BidScreeningRequest{
				GroupSpec: makeGroupSpec("too-many", 5000, 0, 4*gi, 10*gi, 10, 50000),
			},
			pass:   false,
			reason: "insufficient capacity",
		},
		{
			name: "unmatched_required_attributes",
			req: &providerv1.BidScreeningRequest{
				GroupSpec: makeGroupSpecWithAttrs("attr-test", 100, 0, 16*mi, 128*mi, 1, 1000,
					attrtypes.Attributes{{Key: "region", Value: "eu-west"}}),
			},
			pass:   false,
			reason: "incompatible provider attributes",
		},
		{
			name: "multiple_storage_volumes_within_limit",
			req: &providerv1.BidScreeningRequest{
				GroupSpec: makeMultiStorageGroupSpec("multi-vol", 100, 0, 16*mi, 1, 1000,
					[]uint64{128 * mi, 256 * mi}),
			},
			pass: true,
		},
		{
			name: "exceeds_max_group_volumes",
			req: &providerv1.BidScreeningRequest{
				GroupSpec: makeMultiStorageGroupSpec("too-many-vols", 100, 0, 16*mi, 1, 1000,
					[]uint64{64 * mi, 64 * mi, 64 * mi, 64 * mi, 64 * mi}),
			},
			pass:   false,
			reason: "group volumes count exceeds limit",
		},
		{
			name: "auditor_signature_required",
			req: &providerv1.BidScreeningRequest{
				GroupSpec: makeSignedByGroupSpec("signed-test", 100, 0, 16*mi, 128*mi, 1, 1000,
					[]string{"akash1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqq7cdc78"}),
			},
			pass:   false,
			reason: "attribute signature requirements not met",
		},
		{
			name: "many_resource_units",
			req: &providerv1.BidScreeningRequest{
				GroupSpec: makeManyResourcesGroupSpec(5),
			},
			pass: true,
		},
	}

	for _, tt := range tests {
		s.Run(tt.name, func() {
			tctx, cancel := context.WithTimeout(s.ctx, 10*time.Second)
			defer cancel()

			resp, err := client.BidScreening(tctx, tt.req)
			s.Require().NoError(err, "rpc call failed")
			s.Require().Equal(tt.pass, resp.Passed, "expected passed=%v, got reasons=%v", tt.pass, resp.Reasons)

			if resp.Passed {
				s.Require().NotNil(resp.Price, "passed screening should include price")
				s.Require().NotEmpty(resp.Price.Denom, "price should have denom")
				s.Require().True(resp.Price.Amount.IsPositive(), "price should be positive")
				s.Require().NotNil(resp.ResourceOffer, "passed screening should include resource offer")
			} else {
				s.Require().NotEmpty(resp.Reasons, "failed screening should include reasons")
				if tt.reason != "" {
					found := false
					for _, r := range resp.Reasons {
						if containsSubstring(r, tt.reason) {
							found = true
							break
						}
					}
					s.Require().True(found, "expected reason containing %q in %v", tt.reason, resp.Reasons)
				}
			}
		})
	}
}

func (s *E2EBidScreening) TestBidScreeningConcurrent() {
	conn, client := s.dialGRPC()
	defer conn.Close()

	const n = 10
	var wg sync.WaitGroup
	errs := make(chan error, n)

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tctx, cancel := context.WithTimeout(s.ctx, 10*time.Second)
			defer cancel()

			req := &providerv1.BidScreeningRequest{
				GroupSpec: makeGroupSpec("concurrent", 100, 0, 16*mi, 128*mi, 1, 1000),
			}
			resp, err := client.BidScreening(tctx, req)
			if err != nil {
				errs <- fmt.Errorf("rpc: %w", err)
				return
			}
			if !resp.Passed {
				errs <- fmt.Errorf("expected pass, got reasons=%v", resp.Reasons)
			}
		}()
	}

	wg.Wait()
	close(errs)

	for err := range errs {
		s.Require().NoError(err)
	}
}

func (s *E2EBidScreening) TestGetStatusViaGRPC() {
	conn, client := s.dialGRPC()
	defer conn.Close()

	tctx, cancel := context.WithTimeout(s.ctx, 10*time.Second)
	defer cancel()

	resp, err := client.GetStatus(tctx, &emptypb.Empty{})
	s.Require().NoError(err)
	s.Require().NotNil(resp)
}

func containsSubstring(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && findSubstring(s, substr))
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func makeGroupSpec(name string, cpuMillis, gpuUnits, memBytes, storageBytes uint64, count uint32, priceUakt int64) *dvbeta.GroupSpec {
	return &dvbeta.GroupSpec{
		Name: name,
		Resources: dvbeta.ResourceUnits{
			{
				Resources: rtypes.Resources{
					ID:      1,
					CPU:     &rtypes.CPU{Units: rtypes.NewResourceValue(cpuMillis)},
					GPU:     &rtypes.GPU{Units: rtypes.NewResourceValue(gpuUnits)},
					Memory:  &rtypes.Memory{Quantity: rtypes.NewResourceValue(memBytes)},
					Storage: rtypes.Volumes{{Name: "default", Quantity: rtypes.NewResourceValue(storageBytes)}},
					Endpoints: rtypes.Endpoints{
						{Kind: rtypes.Endpoint_SHARED_HTTP},
					},
				},
				Count: count,
				Price: sdk.NewInt64DecCoin("uakt", priceUakt),
			},
		},
	}
}

func makeGPUGroupSpec(name string, cpuMillis, gpuUnits, memBytes, storageBytes uint64, count uint32, priceUakt int64) *dvbeta.GroupSpec {
	gs := makeGroupSpec(name, cpuMillis, gpuUnits, memBytes, storageBytes, count, priceUakt)
	gs.Resources[0].GPU.Attributes = attrtypes.Attributes{
		{Key: "vendor/nvidia/model/*", Value: "true"},
	}
	return gs
}

func makeGroupSpecWithAttrs(name string, cpuMillis, gpuUnits, memBytes, storageBytes uint64, count uint32, priceUakt int64, attrs attrtypes.Attributes) *dvbeta.GroupSpec {
	gs := makeGroupSpec(name, cpuMillis, gpuUnits, memBytes, storageBytes, count, priceUakt)
	gs.Requirements.Attributes = attrs
	return gs
}

func makeSignedByGroupSpec(name string, cpuMillis, gpuUnits, memBytes, storageBytes uint64, count uint32, priceUakt int64, auditors []string) *dvbeta.GroupSpec {
	gs := makeGroupSpec(name, cpuMillis, gpuUnits, memBytes, storageBytes, count, priceUakt)
	gs.Requirements.SignedBy = attrtypes.SignedBy{AllOf: auditors}
	return gs
}

func makeMultiStorageGroupSpec(name string, cpuMillis, gpuUnits, memBytes uint64, count uint32, priceUakt int64, storageSizes []uint64) *dvbeta.GroupSpec {
	var vols rtypes.Volumes
	for i, sz := range storageSizes {
		vols = append(vols, rtypes.Storage{
			Name:     fmt.Sprintf("vol%d", i),
			Quantity: rtypes.NewResourceValue(sz),
		})
	}

	return &dvbeta.GroupSpec{
		Name: name,
		Resources: dvbeta.ResourceUnits{
			{
				Resources: rtypes.Resources{
					ID:      1,
					CPU:     &rtypes.CPU{Units: rtypes.NewResourceValue(cpuMillis)},
					GPU:     &rtypes.GPU{Units: rtypes.NewResourceValue(gpuUnits)},
					Memory:  &rtypes.Memory{Quantity: rtypes.NewResourceValue(memBytes)},
					Storage: vols,
					Endpoints: rtypes.Endpoints{
						{Kind: rtypes.Endpoint_SHARED_HTTP},
					},
				},
				Count: count,
				Price: sdk.NewInt64DecCoin("uakt", priceUakt),
			},
		},
	}
}

func makeManyResourcesGroupSpec(n int) *dvbeta.GroupSpec {
	var units dvbeta.ResourceUnits
	for i := 0; i < n; i++ {
		units = append(units, dvbeta.ResourceUnit{
			Resources: rtypes.Resources{
				ID:      uint32(i + 1),
				CPU:     &rtypes.CPU{Units: rtypes.NewResourceValue(100)},
				GPU:     &rtypes.GPU{Units: rtypes.NewResourceValue(0)},
				Memory:  &rtypes.Memory{Quantity: rtypes.NewResourceValue(16 * mi)},
				Storage: rtypes.Volumes{{Name: "default", Quantity: rtypes.NewResourceValue(128 * mi)}},
				Endpoints: rtypes.Endpoints{
					{Kind: rtypes.Endpoint_SHARED_HTTP},
				},
			},
			Count: 1,
			Price: sdk.NewInt64DecCoin("uakt", 1000),
		})
	}
	return &dvbeta.GroupSpec{Name: "many-resources", Resources: units}
}
