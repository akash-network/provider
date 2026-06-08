package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/cloud-j-luna/attestation-sidecar/tee"
)

const protocolVersion = "3"

// QuoteRequest is the request body for POST /quote.
// The tenant generates a nonce and sends it; the sidecar writes it into
// the TEE report_data field. The provider never substitutes the nonce.
type QuoteRequest struct {
	Nonce string `json:"nonce"` // base64-encoded, must decode to exactly 64 bytes

	// BindTLS, when true, computes report_data as SHA-512(tls_pubkey || nonce)[:64]
	// instead of using the raw nonce. This binds the attestation evidence to
	// the TLS channel, proving the endpoint is inside the attested TEE.
	BindTLS bool `json:"bind_tls,omitempty"`
}

// QuoteResponse is the response body for POST /quote.
// Contains raw, hardware-signed attestation evidence.
// The provider never produces or signs this — it comes from the PSP.
type QuoteResponse struct {
	Report    string `json:"report"`     // base64 raw SNP attestation report
	CertChain string `json:"cert_chain"` // base64 VCEK cert chain (may be empty)
	TEEType   string `json:"tee_type"`   // "snp"
	AuxBlob   string `json:"auxblob"`    // empty on NVIDIA-patched kernel

	// GPUReport is the base64-encoded NVIDIA GPU attestation report.
	// Present only when the TEE includes GPU CC (e.g. tee_type "snp-gpu").
	GPUReport string `json:"gpu_report,omitempty"`

	// TLSBound indicates whether report_data was computed with TLS channel binding.
	// When true, the tenant must verify: report_data == SHA-512(tls_pubkey || nonce)[:64]
	TLSBound bool `json:"tls_bound"`
}

// InfoResponse is the response body for GET /info.
type InfoResponse struct {
	TEEType         string `json:"tee_type"`
	ProtocolVersion string `json:"protocol_version"`

	// GPUAvailable indicates whether GPU CC attestation is available.
	GPUAvailable bool `json:"gpu_available,omitempty"`

	// TLSPublicKey is the DER-encoded public key of the sidecar's ephemeral
	// TLS certificate, base64-encoded. The tenant uses this to verify TLS
	// channel binding: report_data == SHA-512(tls_pubkey_der || nonce)[:64]
	TLSPublicKey string `json:"tls_public_key"`
}

// NewServer creates the attestation sidecar HTTP server.
func NewServer(addr string, provider tee.Provider, binding *TLSBinding) *http.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /quote", quoteHandler(provider, binding))
	mux.HandleFunc("GET /info", infoHandler(provider, binding))
	mux.HandleFunc("GET /healthz", healthHandler())

	return &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
}

func quoteHandler(provider tee.Provider, binding *TLSBinding) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req QuoteRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, fmt.Sprintf("invalid request: %v", err), http.StatusBadRequest)
			return
		}

		nonceBytes, err := base64.StdEncoding.DecodeString(req.Nonce)
		if err != nil {
			http.Error(w, "nonce must be valid base64", http.StatusBadRequest)
			return
		}

		if len(nonceBytes) != 64 {
			http.Error(w, fmt.Sprintf("nonce must be exactly 64 bytes, got %d", len(nonceBytes)), http.StatusBadRequest)
			return
		}

		var nonce [64]byte
		copy(nonce[:], nonceBytes)

		// Compute report_data: either raw nonce or TLS-bound hash
		var reportData [64]byte
		tlsBound := false
		if req.BindTLS {
			reportData = ComputeBoundReportData(binding.PubKeyDER, nonce)
			tlsBound = true
		} else {
			reportData = nonce
		}

		report, err := provider.GetQuote(context.Background(), reportData)
		if err != nil {
			http.Error(w, fmt.Sprintf("failed to get attestation quote: %v", err), http.StatusServiceUnavailable)
			return
		}

		resp := QuoteResponse{
			Report:    base64.StdEncoding.EncodeToString(report.Report),
			CertChain: base64.StdEncoding.EncodeToString(report.CertChain),
			TEEType:   provider.Name(),
			AuxBlob:   base64.StdEncoding.EncodeToString(report.AuxBlob),
			TLSBound:  tlsBound,
		}
		if len(report.GPUReport) > 0 {
			resp.GPUReport = base64.StdEncoding.EncodeToString(report.GPUReport)
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp) //nolint:errcheck
	}
}

func infoHandler(provider tee.Provider, binding *TLSBinding) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		name := provider.Name()
		gpuAvailable := name == tee.NameSNPGPU || name == tee.NameTDXGPU
		resp := InfoResponse{
			TEEType:         name,
			ProtocolVersion: protocolVersion,
			GPUAvailable:    gpuAvailable,
			TLSPublicKey:    base64.StdEncoding.EncodeToString(binding.PubKeyDER),
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp) //nolint:errcheck
	}
}

func healthHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok")) //nolint:errcheck
	}
}
