package rest

import (
	"fmt"
	"io"
	"net/http"
	"strconv"

	"cosmossdk.io/log"

	"github.com/akash-network/provider/cluster"
)

// createAttestationQuoteHandler handles attestation quote requests by calling
// the sidecar via the cluster.Client interface, which resolves the pod IP from
// K8s and calls the sidecar directly — the same pattern used by Exec and LeaseLogs.
//
// The provider forwards the tenant's nonce verbatim to the sidecar and returns
// the hardware-signed evidence verbatim. The provider never inspects or modifies
// either payload.
func createAttestationQuoteHandler(log log.Logger, cclient cluster.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		leaseID := requestLeaseID(r)

		service := r.URL.Query().Get("service")
		var podIndex uint
		if pidx := r.URL.Query().Get("podIndex"); pidx != "" {
			if service == "" {
				http.Error(w, "podIndex requires service parameter", http.StatusBadRequest)
				return
			}
			idx, err := strconv.ParseUint(pidx, 10, 31)
			if err != nil {
				http.Error(w, "invalid podIndex parameter", http.StatusBadRequest)
				return
			}
			podIndex = uint(idx)
		}

		log.Debug("attestation quote request", "lease", leaseID.String(), "service", service, "podIndex", podIndex)

		body, err := io.ReadAll(io.LimitReader(r.Body, 4096))
		if err != nil {
			http.Error(w, "failed to read request body", http.StatusBadRequest)
			return
		}

		respBody, statusCode, err := cclient.AttestationQuote(r.Context(), leaseID, service, podIndex, body)
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
