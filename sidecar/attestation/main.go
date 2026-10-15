package main

import (
	"context"
	"crypto/tls"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/akash-network/attestation-sidecar/internal/logging"
	"github.com/akash-network/attestation-sidecar/tee"
)

func main() {
	logCfg, err := logging.ConfigFromEnv(os.Getenv)
	if err != nil {
		logging.New(os.Stdout, logging.DefaultConfig()).Error("invalid logging configuration", "err", err)
		os.Exit(1)
	}

	logger := logging.New(os.Stdout, logCfg).With("module", "attestation-sidecar")
	tee.SetLogger(logger.With("module", "tee"))

	listenAddr := os.Getenv("ATTESTATION_LISTEN_ADDR")
	if listenAddr == "" {
		listenAddr = ":8790"
	}

	provider, err := tee.Detect()
	if err != nil {
		logger.Error("no TEE attestation surface available", "err", err)
		os.Exit(1)
	}

	logger.Info("detected TEE", "tee", provider.Name())

	// Generate ephemeral TLS keypair for channel binding.
	// The public key hash can be embedded in report_data to prove
	// the TLS endpoint runs inside the attested TEE.
	binding, err := GenerateEphemeralKeypair()
	if err != nil {
		logger.Error("failed to generate TLS keypair", "err", err)
		os.Exit(1)
	}

	logger.Info("generated ephemeral TLS keypair for channel binding")

	srv := NewServer(listenAddr, provider, binding)

	// Configure TLS
	srv.TLSConfig = &tls.Config{
		Certificates: []tls.Certificate{binding.Certificate},
		MinVersion:   tls.VersionTLS12,
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	go func() {
		logger.Info("attestation sidecar listening", "listen_addr", listenAddr, "tls", true)
		if err := srv.ListenAndServeTLS("", ""); err != nil && err != http.ErrServerClosed {
			logger.Error("server error", "err", err)
			cancel()
		}
	}()

	<-ctx.Done()
	logger.Info("shutting down")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	_ = srv.Shutdown(shutdownCtx)
}
