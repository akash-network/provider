package grpc

import (
	"context"
	"encoding/json"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	leasev1 "pkg.akt.dev/go/provider/lease/v1"
	ajwt "pkg.akt.dev/go/util/jwt"

	"github.com/akash-network/provider/cluster"
	gwutils "github.com/akash-network/provider/gateway/utils"
)

type grpcLeaseV1 struct {
	leasev1.UnimplementedLeaseRPCServer
	cclient cluster.Client
}

var _ leasev1.LeaseRPCServer = (*grpcLeaseV1)(nil)

func (s *grpcLeaseV1) AttestationQuote(ctx context.Context, req *leasev1.AttestationQuoteRequest) (*leasev1.AttestationQuoteResponse, error) {
	claims := ClaimsFromCtx(ctx)
	leaseID := req.GetLeaseId()

	if !claims.AuthorizeLeaseIDForPermissionScope(leaseID, ajwt.PermissionScopeAttestation) {
		return nil, status.Error(codes.PermissionDenied, "unauthorized: attestation scope required")
	}

	// Build the sidecar request body from the typed proto fields.
	sidecarReq := struct {
		Nonce   string `json:"nonce"`
		BindTLS bool   `json:"bind_tls,omitempty"`
	}{
		Nonce:   req.GetNonce(),
		BindTLS: req.GetBindTls(),
	}

	body, err := json.Marshal(sidecarReq)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "marshal sidecar request: %v", err)
	}

	respBody, httpStatus, err := s.cclient.AttestationQuote(ctx, leaseID, body)
	if err != nil {
		return nil, status.Errorf(gwutils.HTTPToGRPCCode(httpStatus), "attestation quote: %v", err)
	}

	// Parse the sidecar JSON response into the proto response.
	var sidecarResp struct {
		Report      string `json:"report"`
		CertChain   string `json:"cert_chain"`
		TEEPlatform string `json:"tee_platform"`
		Auxblob     string `json:"auxblob"`
		GPUReports  []struct {
			DeviceIndex uint32 `json:"device_index"`
			Report      string `json:"report"`
		} `json:"gpu_reports"`
		TLSBound bool `json:"tls_bound"`
	}

	if err := json.Unmarshal(respBody, &sidecarResp); err != nil {
		return nil, status.Errorf(codes.Internal, "unmarshal sidecar response: %v", err)
	}

	resp := &leasev1.AttestationQuoteResponse{
		Report:      sidecarResp.Report,
		CertChain:   sidecarResp.CertChain,
		TeePlatform: sidecarResp.TEEPlatform,
		Auxblob:     sidecarResp.Auxblob,
		TlsBound:    sidecarResp.TLSBound,
	}

	for _, gr := range sidecarResp.GPUReports {
		resp.GpuReports = append(resp.GpuReports, leasev1.AttestationGPUReport{
			DeviceIndex: gr.DeviceIndex,
			Report:      gr.Report,
		})
	}

	return resp, nil
}
