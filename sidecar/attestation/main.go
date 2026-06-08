package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/cloud-j-luna/attestation-sidecar/tee"
)

func main() {
	listenAddr := os.Getenv("ATTESTATION_LISTEN_ADDR")
	if listenAddr == "" {
		listenAddr = ":8790"
	}

	provider, err := tee.Detect()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: no TEE attestation surface available: %v\n", err)
		os.Exit(1)
	}

	fmt.Fprintf(os.Stderr, "detected TEE: %s\n", provider.Name())

	// Generate ephemeral TLS keypair for channel binding.
	// The public key hash can be embedded in report_data to prove
	// the TLS endpoint runs inside the attested TEE.
	binding, err := GenerateEphemeralKeypair()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: failed to generate TLS keypair: %v\n", err)
		os.Exit(1)
	}

	fmt.Fprintf(os.Stderr, "generated ephemeral TLS keypair for channel binding\n")

	srv := NewServer(listenAddr, provider, binding)

	// Configure TLS
	srv.TLSConfig = &tls.Config{
		Certificates: []tls.Certificate{binding.Certificate},
		MinVersion:   tls.VersionTLS12,
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	go func() {
		fmt.Fprintf(os.Stderr, "attestation sidecar listening on %s (TLS)\n", listenAddr)
		if err := srv.ListenAndServeTLS("", ""); err != nil && err != http.ErrServerClosed {
			fmt.Fprintf(os.Stderr, "server error: %v\n", err)
			cancel()
		}
	}()

	<-ctx.Done()
	fmt.Fprintln(os.Stderr, "shutting down...")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	_ = srv.Shutdown(shutdownCtx)
}
