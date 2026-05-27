package rest

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"

	"cosmossdk.io/log"
	sdk "github.com/cosmos/cosmos-sdk/types"
	gcontext "github.com/gorilla/context"
	"github.com/gorilla/mux"
	mtypes "pkg.akt.dev/go/node/market/v1"

	"github.com/akash-network/provider/cluster"
	"github.com/akash-network/provider/cluster/kube/builder"
)

const attestationProtocolVersion = "2"

// AttestationConfig holds provider-configured attestation parameters
// that are returned as advisory hints in the directory response.
// All values are explicitly untrusted (invariant #4).
type AttestationConfig struct {
	ExpectedLaunchMeasurement string
	ExpectedImageDigest       string
}

// AttestationDirectoryResponse is the response from the directory endpoint.
// This endpoint is explicitly UNTRUSTED (architectural invariant #3).
// All fields are advisory routing hints only. The tenant must verify
// all claims against hardware-signed attestation evidence.
type AttestationDirectoryResponse struct {
	// Endpoint is the lease-scoped URL to reach the attestation sidecar.
	// Tenant calls POST on this path with their nonce.
	Endpoint string `json:"endpoint"`

	// ExpectedLaunchMeasurement is an advisory hint the tenant can compare
	// against the MEASUREMENT field in the hardware-signed SNP report.
	// NOT a standalone trust signal (invariant #4).
	ExpectedLaunchMeasurement string `json:"expected_launch_measurement,omitempty"`

	// ExpectedImageDigest is an advisory hint for the Kata VM image digest.
	// NOT a standalone trust signal (invariant #4).
	ExpectedImageDigest string `json:"expected_image_digest,omitempty"`

	ProtocolVersion string `json:"protocol_version"`
	RuntimeClass    string `json:"runtime_class"`
	TEEType         string `json:"tee_type"` // "amd-sev-snp"
}

// createAttestationDirectoryHandler returns the directory endpoint handler.
// The directory API is unauthenticated — the tenant needs to discover the
// sidecar before establishing a trusted channel. Responses contain no secrets.
func createAttestationDirectoryHandler(log log.Logger, cclient cluster.ReadClient, attestCfg AttestationConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		vars := mux.Vars(r)

		dseq, err := strconv.ParseUint(vars["dseq"], 10, 64)
		if err != nil {
			http.Error(w, "invalid dseq", http.StatusBadRequest)
			return
		}
		gseq, err := strconv.ParseUint(vars["gseq"], 10, 32)
		if err != nil {
			http.Error(w, "invalid gseq", http.StatusBadRequest)
			return
		}
		oseq, err := strconv.ParseUint(vars["oseq"], 10, 32)
		if err != nil {
			http.Error(w, "invalid oseq", http.StatusBadRequest)
			return
		}

		log.Debug("attestation directory request",
			"dseq", dseq, "gseq", gseq, "oseq", oseq)

		// The provider address is stored in the request context by the router middleware.
		providerAddr, ok := gcontext.Get(r, providerContextKey).(sdk.Address)
		if !ok {
			http.Error(w, "provider address unavailable", http.StatusInternalServerError)
			return
		}

		// Construct a LeaseID to look up the manifest group.
		// The directory endpoint is unauthenticated so we don't have the owner address.
		// GetManifestGroup resolves the lease namespace from the full LeaseID via
		// a SHA-224 hash. Without the owner, we pass an empty owner — the namespace
		// hash won't match any real lease. Instead, we need to search differently.
		//
		// For the directory endpoint, we construct a partial lease ID. The provider
		// can look up by dseq since it has the CRD manifest labeled with dseq.
		// The GetManifestGroup uses LidNS which requires a full LeaseID.
		// Since this endpoint is unauthenticated and untrusted (invariant #3),
		// we accept that the owner field is empty — the lookup will work only
		// if the caller also provides it as a query parameter.
		owner := r.URL.Query().Get("owner")

		lid := mtypes.LeaseID{
			Owner:    owner,
			DSeq:     dseq,
			GSeq:     uint32(gseq), //nolint:gosec
			OSeq:     uint32(oseq), //nolint:gosec
			Provider: providerAddr.String(),
		}

		// Look up the manifest to find the runtime class.
		found, mgroup, err := cclient.GetManifestGroup(r.Context(), lid)
		if err != nil {
			log.Error("attestation directory: manifest lookup failed", "err", err)
			writeClusterError(w, err)
			return
		}
		if !found {
			http.Error(w, "lease not found", http.StatusNotFound)
			return
		}

		// Find the first service with a CC runtime class.
		var runtimeClass string
		for _, svc := range mgroup.Services {
			if svc.SchedulerParams != nil && builder.IsConfidentialComputeRuntimeClass(svc.SchedulerParams.RuntimeClass) {
				runtimeClass = svc.SchedulerParams.RuntimeClass
				break
			}
		}

		if runtimeClass == "" {
			http.Error(w, "lease is not a confidential compute deployment", http.StatusNotFound)
			return
		}

		teeType := builder.TEETypeForRuntimeClass(runtimeClass)

		resp := AttestationDirectoryResponse{
			Endpoint:                  fmt.Sprintf("/lease/%d/%d/%d/attestation/quote", dseq, gseq, oseq),
			ExpectedLaunchMeasurement: attestCfg.ExpectedLaunchMeasurement,
			ExpectedImageDigest:       attestCfg.ExpectedImageDigest,
			ProtocolVersion:           attestationProtocolVersion,
			RuntimeClass:              runtimeClass,
			TEEType:                   teeType,
		}

		// Explicitly untrusted — staleness is HTTP-native (invariant #3)
		w.Header().Set("Cache-Control", "max-age=60, must-revalidate")
		w.Header().Set("Content-Type", contentTypeJSON)
		json.NewEncoder(w).Encode(resp) //nolint:errcheck
	}
}

// createAttestationQuoteHandler handles attestation quote requests by calling
// the sidecar via the cluster.Client interface, which resolves the pod IP from
// K8s and calls the sidecar directly — the same pattern used by Exec and LeaseLogs.
//
// The provider forwards the tenant's nonce verbatim to the sidecar and returns
// the hardware-signed evidence verbatim. The provider never inspects or modifies
// either payload (invariants #1 and #5).
func createAttestationQuoteHandler(log log.Logger, cclient cluster.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		leaseID := requestLeaseID(r)

		log.Debug("attestation quote request", "lease", leaseID.String())

		body, err := io.ReadAll(io.LimitReader(r.Body, 4096))
		if err != nil {
			http.Error(w, "failed to read request body", http.StatusBadRequest)
			return
		}

		respBody, statusCode, err := cclient.AttestationQuote(r.Context(), leaseID, body)
		if err != nil {
			log.Error("attestation quote failed", "err", err, "lease", leaseID.String())
			http.Error(w, fmt.Sprintf("attestation quote failed: %v", err), statusCode)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(statusCode)
		w.Write(respBody) //nolint:errcheck
	}
}
