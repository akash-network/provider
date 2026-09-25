package grpc

import (
	"encoding/base64"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	leasev1 "pkg.akt.dev/go/provider/lease/v1"
)

func encoded(value string) string {
	return base64.StdEncoding.EncodeToString([]byte(value))
}

func TestParseSidecarAttestationQuoteResponsePreservesEveryGPUComponent(t *testing.T) {
	sidecarResponse := sidecarAttestationQuoteResponse{
		Report:      encoded("cpu-report"),
		CertChain:   encoded("cpu-chain"),
		TEEPlatform: "snp-gpu",
		Auxblob:     encoded("auxblob"),
		TLSBound:    true,
	}
	sidecarResponse.GPUReports = append(sidecarResponse.GPUReports,
		sidecarGPUReport{
			DeviceIndex:       2,
			Architecture:      "BLACKWELL",
			UUID:              "GPU-00000000-0000-0000-0000-000000000002",
			Report:            encoded("report-2cec-2chain-2"),
			AttestationReport: encoded("report-2"),
			CECReport:         encoded("cec-2"),
			CertificateChain:  encoded("chain-2"),
		},
		sidecarGPUReport{
			DeviceIndex:       7,
			Architecture:      "BLACKWELL",
			UUID:              "GPU-00000000-0000-0000-0000-000000000007",
			Report:            encoded("report-7chain-7"),
			AttestationReport: encoded("report-7"),
			CertificateChain:  encoded("chain-7"),
		},
	)

	data, err := json.Marshal(sidecarResponse)
	require.NoError(t, err)

	actual, err := parseSidecarAttestationQuoteResponse(data)
	require.NoError(t, err)
	require.Equal(t, &leasev1.AttestationQuoteResponse{
		Report:      encoded("cpu-report"),
		CertChain:   encoded("cpu-chain"),
		TeePlatform: "snp-gpu",
		Auxblob:     encoded("auxblob"),
		GpuReports: []leasev1.AttestationGPUReport{
			{
				DeviceIndex:       2,
				Architecture:      "BLACKWELL",
				UUID:              "GPU-00000000-0000-0000-0000-000000000002",
				Report:            encoded("report-2cec-2chain-2"),
				AttestationReport: encoded("report-2"),
				CecReport:         encoded("cec-2"),
				CertificateChain:  encoded("chain-2"),
			},
			{
				DeviceIndex:       7,
				Architecture:      "BLACKWELL",
				UUID:              "GPU-00000000-0000-0000-0000-000000000007",
				Report:            encoded("report-7chain-7"),
				AttestationReport: encoded("report-7"),
				CertificateChain:  encoded("chain-7"),
			},
		},
		TlsBound: true,
	}, actual)
}

func TestParseSidecarAttestationQuoteResponsePreservesLegacyGPUReportWithHardwareIdentity(t *testing.T) {
	data := []byte(`{"report":"Y3B1","tee_platform":"snp-gpu","gpu_reports":[{"device_index":3,"architecture":"BLACKWELL","uuid":"GPU-00000000-0000-0000-0000-000000000003","report":"bGVnYWN5"}]}`)

	actual, err := parseSidecarAttestationQuoteResponse(data)
	require.NoError(t, err)
	require.Equal(t, []leasev1.AttestationGPUReport{{
		DeviceIndex:  3,
		Architecture: "BLACKWELL",
		UUID:         "GPU-00000000-0000-0000-0000-000000000003",
		Report:       "bGVnYWN5",
	}}, actual.GpuReports)
}

func TestParseSidecarAttestationQuoteResponseRejectsInvalidEvidence(t *testing.T) {
	tests := []struct {
		name    string
		data    string
		wantErr string
	}{
		{name: "malformed JSON", data: `{`, wantErr: "unmarshal JSON"},
		{name: "empty CPU report", data: `{}`, wantErr: "CPU attestation report is empty"},
		{name: "invalid CPU report", data: `{"report":"***"}`, wantErr: "CPU attestation report is not valid base64"},
		{name: "unsupported platform", data: `{"report":"Y3B1","tee_platform":"future-tee"}`, wantErr: "unsupported TEE platform"},
		{name: "GPU platform without reports", data: `{"report":"Y3B1","tee_platform":"snp-gpu"}`, wantErr: "contains no GPU reports"},
		{name: "CPU platform with GPU reports", data: `{"report":"Y3B1","tee_platform":"snp","gpu_reports":[{"device_index":1,"report":"b25l"}]}`, wantErr: "non-GPU TEE response contains GPU reports"},
		{name: "missing hardware identity", data: `{"report":"Y3B1","tee_platform":"snp-gpu","gpu_reports":[{"device_index":1,"report":"bGVnYWN5"}]}`, wantErr: "unsupported architecture"},
		{name: "unsupported architecture", data: `{"report":"Y3B1","tee_platform":"snp-gpu","gpu_reports":[{"device_index":1,"architecture":"AMPERE","uuid":"GPU-00000000-0000-0000-0000-000000000001","report":"bGVnYWN5"}]}`, wantErr: "unsupported architecture"},
		{name: "invalid UUID", data: `{"report":"Y3B1","tee_platform":"snp-gpu","gpu_reports":[{"device_index":1,"architecture":"BLACKWELL","uuid":"tenant-value","report":"bGVnYWN5"}]}`, wantErr: "invalid NVML UUID"},
		{name: "empty legacy report", data: `{"report":"Y3B1","tee_platform":"snp-gpu","gpu_reports":[{"device_index":1,"architecture":"BLACKWELL","uuid":"GPU-00000000-0000-0000-0000-000000000001"}]}`, wantErr: "legacy GPU report is empty"},
		{name: "explicit fields without legacy report", data: `{"report":"Y3B1","tee_platform":"snp-gpu","gpu_reports":[{"device_index":1,"architecture":"BLACKWELL","uuid":"GPU-00000000-0000-0000-0000-000000000001","attestation_report":"cmVwb3J0","certificate_chain":"Y2hhaW4="}]}`, wantErr: "legacy GPU report is empty"},
		{name: "partial explicit report", data: `{"report":"Y3B1","tee_platform":"snp-gpu","gpu_reports":[{"device_index":1,"architecture":"BLACKWELL","uuid":"GPU-00000000-0000-0000-0000-000000000001","report":"bGVnYWN5","attestation_report":"cmVwb3J0"}]}`, wantErr: "GPU certificate chain is empty"},
		{name: "invalid CEC report", data: `{"report":"Y3B1","tee_platform":"snp-gpu","gpu_reports":[{"device_index":1,"architecture":"BLACKWELL","uuid":"GPU-00000000-0000-0000-0000-000000000001","report":"bGVnYWN5","attestation_report":"cmVwb3J0","cec_report":"***","certificate_chain":"Y2hhaW4="}]}`, wantErr: "GPU CEC report is not valid base64"},
		{name: "legacy report disagrees with explicit fields", data: `{"report":"Y3B1","tee_platform":"snp-gpu","gpu_reports":[{"device_index":1,"architecture":"BLACKWELL","uuid":"GPU-00000000-0000-0000-0000-000000000001","report":"bGVnYWN5","attestation_report":"cmVwb3J0","certificate_chain":"Y2hhaW4="}]}`, wantErr: "legacy report does not match explicit evidence fields"},
		{name: "duplicate device", data: `{"report":"Y3B1","tee_platform":"snp-gpu","gpu_reports":[{"device_index":1,"architecture":"BLACKWELL","uuid":"GPU-00000000-0000-0000-0000-000000000001","report":"b25l"},{"device_index":1,"architecture":"BLACKWELL","uuid":"GPU-00000000-0000-0000-0000-000000000001","report":"dHdv"}]}`, wantErr: "duplicate GPU device index 1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseSidecarAttestationQuoteResponse([]byte(tt.data))
			require.ErrorContains(t, err, tt.wantErr)
		})
	}
}
