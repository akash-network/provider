package webhook

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"cosmossdk.io/log"

	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
)

var (
	runtimeScheme = runtime.NewScheme()
	codecs        = serializer.NewCodecFactory(runtimeScheme)
	deserializer  = codecs.UniversalDeserializer()
)

// Config holds configuration for the attestation webhook server.
type Config struct {
	SidecarImage string
	SidecarEnv   []corev1.EnvVar // additional env vars injected into the sidecar container
	ListenAddr   string
	TLSCert      tls.Certificate
	Log          log.Logger
}

// Server handles mutating admission webhook requests for attestation
// sidecar injection into confidential compute pods.
type Server struct {
	server *http.Server
	config Config
}

// NewServer creates a new attestation webhook server.
// Failure mode: fail-closed. A CC workload without an attestation sidecar
// is silently unverifiable — the tenant cannot confirm TEE execution.
func NewServer(cfg Config) *Server {
	s := &Server{config: cfg}

	mux := http.NewServeMux()
	mux.HandleFunc("/mutate", s.handleMutate)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	s.server = &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		TLSConfig: &tls.Config{
			Certificates: []tls.Certificate{cfg.TLSCert},
			MinVersion:   tls.VersionTLS12,
		},
	}

	return s
}

// ListenAndServeTLS starts the webhook HTTPS server.
func (s *Server) ListenAndServeTLS() error {
	return s.server.ListenAndServeTLS("", "")
}

// Shutdown gracefully shuts down the server.
func (s *Server) Shutdown(ctx context.Context) error {
	return s.server.Shutdown(ctx)
}

func (s *Server) handleMutate(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		s.config.Log.Error("failed to read request body", "err", err)
		http.Error(w, "failed to read body", http.StatusBadRequest)
		return
	}

	if len(body) == 0 {
		http.Error(w, "empty body", http.StatusBadRequest)
		return
	}

	var admissionReview admissionv1.AdmissionReview
	if _, _, err := deserializer.Decode(body, nil, &admissionReview); err != nil {
		s.config.Log.Error("failed to decode admission review", "err", err)
		http.Error(w, fmt.Sprintf("decode error: %v", err), http.StatusBadRequest)
		return
	}

	if admissionReview.Request == nil {
		http.Error(w, "missing request in admission review", http.StatusBadRequest)
		return
	}

	response := s.mutate(admissionReview.Request)

	admissionReview.Response = response
	admissionReview.Response.UID = admissionReview.Request.UID

	resp, err := json.Marshal(admissionReview)
	if err != nil {
		s.config.Log.Error("failed to marshal response", "err", err)
		http.Error(w, fmt.Sprintf("marshal error: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write(resp) //nolint:errcheck
}

func (s *Server) mutate(req *admissionv1.AdmissionRequest) *admissionv1.AdmissionResponse {
	if req == nil {
		return &admissionv1.AdmissionResponse{Allowed: true}
	}

	var pod corev1.Pod
	if err := json.Unmarshal(req.Object.Raw, &pod); err != nil {
		s.config.Log.Error("failed to unmarshal pod", "err", err)
		return &admissionv1.AdmissionResponse{
			Allowed: false,
			Result: &metav1.Status{
				Message: fmt.Sprintf("failed to unmarshal pod: %v", err),
			},
		}
	}

	if !ShouldInject(&pod) {
		return &admissionv1.AdmissionResponse{Allowed: true}
	}

	patch, err := BuildSidecarPatch(&pod, s.config.SidecarImage, s.config.SidecarEnv)
	if err != nil {
		s.config.Log.Error("failed to build sidecar patch", "err", err)
		// Fail-closed: reject the pod if we can't build the patch
		return &admissionv1.AdmissionResponse{
			Allowed: false,
			Result: &metav1.Status{
				Message: fmt.Sprintf("failed to build attestation sidecar patch: %v", err),
			},
		}
	}

	patchType := admissionv1.PatchTypeJSONPatch
	return &admissionv1.AdmissionResponse{
		Allowed:   true,
		Patch:     patch,
		PatchType: &patchType,
	}
}
