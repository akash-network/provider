package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/akash-network/attestation-sidecar/tee"
)

type fixedQuoteProvider struct {
	result *tee.QuoteResult
}

func (p fixedQuoteProvider) Name() string { return tee.NameSNPGPU }
func (p fixedQuoteProvider) Available() bool {
	return true
}
func (p fixedQuoteProvider) GetQuote(context.Context, [64]byte) (*tee.QuoteResult, error) {
	return p.result, nil
}

func TestQuoteResponseSeparatesEveryGPUReportComponent(t *testing.T) {
	provider := fixedQuoteProvider{result: &tee.QuoteResult{
		Report: []byte("cpu-report"),
		GPUReports: []tee.GPUDeviceReport{
			{
				DeviceIndex:       2,
				Report:            []byte("gpu-2cec-2cert-2"),
				AttestationReport: []byte("gpu-2"),
				CECReport:         []byte("cec-2"),
				CertificateChain:  []byte("cert-2"),
			},
			{
				DeviceIndex:       7,
				Report:            []byte("gpu-7cert-7"),
				AttestationReport: []byte("gpu-7"),
				CertificateChain:  []byte("cert-7"),
			},
		},
	}}

	body, err := json.Marshal(QuoteRequest{Nonce: base64.StdEncoding.EncodeToString(make([]byte, 64))})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/quote", bytes.NewReader(body))
	recorder := httptest.NewRecorder()
	quoteHandler(provider, &TLSBinding{}).ServeHTTP(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("unexpected status %d: %s", recorder.Code, recorder.Body.String())
	}

	var response QuoteResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.GPUReports) != 2 {
		t.Fatalf("expected two GPU reports, got %d", len(response.GPUReports))
	}

	want := []GPUReportEntry{
		{
			DeviceIndex:       2,
			Report:            base64.StdEncoding.EncodeToString([]byte("gpu-2cec-2cert-2")),
			AttestationReport: base64.StdEncoding.EncodeToString([]byte("gpu-2")),
			CECReport:         base64.StdEncoding.EncodeToString([]byte("cec-2")),
			CertificateChain:  base64.StdEncoding.EncodeToString([]byte("cert-2")),
		},
		{
			DeviceIndex:       7,
			Report:            base64.StdEncoding.EncodeToString([]byte("gpu-7cert-7")),
			AttestationReport: base64.StdEncoding.EncodeToString([]byte("gpu-7")),
			CertificateChain:  base64.StdEncoding.EncodeToString([]byte("cert-7")),
		},
	}
	for i := range want {
		if response.GPUReports[i] != want[i] {
			t.Fatalf("GPU report %d mismatch\nwant: %#v\n got: %#v", i, want[i], response.GPUReports[i])
		}
	}
}
