package grpc

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"

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

type sidecarGPUReport struct {
	DeviceIndex       uint32 `json:"device_index"`
	Report            string `json:"report"`
	AttestationReport string `json:"attestation_report"`
	CECReport         string `json:"cec_report"`
	CertificateChain  string `json:"certificate_chain"`
}

type sidecarAttestationQuoteResponse struct {
	Report      string             `json:"report"`
	CertChain   string             `json:"cert_chain"`
	TEEPlatform string             `json:"tee_platform"`
	Auxblob     string             `json:"auxblob"`
	GPUReports  []sidecarGPUReport `json:"gpu_reports"`
	TLSBound    bool               `json:"tls_bound"`
}

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

	respBody, httpStatus, err := s.cclient.AttestationQuote(ctx, leaseID, "", 0, body)
	if err != nil {
		return nil, status.Errorf(gwutils.HTTPToGRPCCode(httpStatus), "attestation quote: %v", err)
	}

	resp, err := parseSidecarAttestationQuoteResponse(respBody)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "parse sidecar response: %v", err)
	}

	return resp, nil
}

func parseSidecarAttestationQuoteResponse(data []byte) (*leasev1.AttestationQuoteResponse, error) {
	var sidecarResp sidecarAttestationQuoteResponse
	if err := json.Unmarshal(data, &sidecarResp); err != nil {
		return nil, fmt.Errorf("unmarshal JSON: %w", err)
	}
	if _, err := decodeRequiredBase64("CPU attestation report", sidecarResp.Report); err != nil {
		return nil, err
	}

	var isGPUPlatform bool
	switch sidecarResp.TEEPlatform {
	case "snp", "tdx":
	case "snp-gpu", "tdx-gpu":
		isGPUPlatform = true
	default:
		return nil, fmt.Errorf("unsupported TEE platform %q", sidecarResp.TEEPlatform)
	}
	if isGPUPlatform && len(sidecarResp.GPUReports) == 0 {
		return nil, fmt.Errorf("GPU TEE response contains no GPU reports")
	}
	if !isGPUPlatform && len(sidecarResp.GPUReports) != 0 {
		return nil, fmt.Errorf("non-GPU TEE response contains GPU reports")
	}

	resp := &leasev1.AttestationQuoteResponse{
		Report:      sidecarResp.Report,
		CertChain:   sidecarResp.CertChain,
		TeePlatform: sidecarResp.TEEPlatform,
		Auxblob:     sidecarResp.Auxblob,
		TlsBound:    sidecarResp.TLSBound,
	}

	seenDeviceIndices := make(map[uint32]struct{}, len(sidecarResp.GPUReports))
	for _, gr := range sidecarResp.GPUReports {
		if _, duplicate := seenDeviceIndices[gr.DeviceIndex]; duplicate {
			return nil, fmt.Errorf("duplicate GPU device index %d", gr.DeviceIndex)
		}
		seenDeviceIndices[gr.DeviceIndex] = struct{}{}

		legacyReport, err := decodeRequiredBase64("legacy GPU report", gr.Report)
		if err != nil {
			return nil, fmt.Errorf("GPU %d: %w", gr.DeviceIndex, err)
		}

		hasExplicitFields := gr.AttestationReport != "" || gr.CECReport != "" || gr.CertificateChain != ""
		if hasExplicitFields {
			attestationReport, err := decodeRequiredBase64("GPU attestation report", gr.AttestationReport)
			if err != nil {
				return nil, fmt.Errorf("GPU %d: %w", gr.DeviceIndex, err)
			}
			certificateChain, err := decodeRequiredBase64("GPU certificate chain", gr.CertificateChain)
			if err != nil {
				return nil, fmt.Errorf("GPU %d: %w", gr.DeviceIndex, err)
			}

			cecReport := []byte(nil)
			if gr.CECReport != "" {
				cecReport, err = decodeRequiredBase64("GPU CEC report", gr.CECReport)
				if err != nil {
					return nil, fmt.Errorf("GPU %d: %w", gr.DeviceIndex, err)
				}
			}

			expectedLegacyReport := make([]byte, 0, len(attestationReport)+len(cecReport)+len(certificateChain))
			expectedLegacyReport = append(expectedLegacyReport, attestationReport...)
			expectedLegacyReport = append(expectedLegacyReport, cecReport...)
			expectedLegacyReport = append(expectedLegacyReport, certificateChain...)
			if !bytes.Equal(legacyReport, expectedLegacyReport) {
				return nil, fmt.Errorf("GPU %d: legacy report does not match explicit evidence fields", gr.DeviceIndex)
			}
		}

		resp.GpuReports = append(resp.GpuReports, leasev1.AttestationGPUReport{
			DeviceIndex:       gr.DeviceIndex,
			Report:            gr.Report,
			AttestationReport: gr.AttestationReport,
			CecReport:         gr.CECReport,
			CertificateChain:  gr.CertificateChain,
		})
	}

	return resp, nil
}

func decodeRequiredBase64(field, value string) ([]byte, error) {
	if value == "" {
		return nil, fmt.Errorf("%s is empty", field)
	}
	decoded, err := base64.StdEncoding.Strict().DecodeString(value)
	if err != nil {
		return nil, fmt.Errorf("%s is not valid base64: %w", field, err)
	}
	return decoded, nil
}
