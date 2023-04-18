package kube

import (
	"os"
	"testing"
)

func TestConfigFromEnv(t *testing.T) {
	t.Run("should create if environment variables are set correctly", func(t *testing.T) {
		os.Setenv(akashProviderIssuerTypeStr, "cluster-issuer")
		os.Setenv(akashProviderIssuerNameStr, "letsencrypt")
		ccfg, err := configFromEnv()

		if err != nil {
			t.Fatalf("Did not expect an error: %s", err)
		}

		if ccfg.issuerType != "cluster-issuer" {
			t.Errorf("Expected cluster-issuer, got %s", ccfg.issuerType)
		}

		if ccfg.issuerName != "letsencrypt" {
			t.Errorf("Expected letsencrypt, got %s", ccfg.issuerName)
		}
	})

	t.Run("should return error if type not set", func(t *testing.T) {
		os.Clearenv()
		os.Setenv(akashProviderIssuerNameStr, "letsencrypt")
		_, err := configFromEnv()

		if err == nil {
			t.Fatalf("Expected an error")
		}
	})

	t.Run("should return error if name not set", func(t *testing.T) {
		os.Clearenv()
		os.Setenv(akashProviderIssuerTypeStr, "cluster-issuer")
		_, err := configFromEnv()

		if err == nil {
			t.Fatalf("Expected an error")
		}
	})

	t.Run("should return error if type is invalid", func(t *testing.T) {
		os.Clearenv()
		os.Setenv(akashProviderIssuerTypeStr, "fake-issuer-type")
		os.Setenv(akashProviderIssuerNameStr, "letsencrypt")

		_, err := configFromEnv()

		if err == nil {
			t.Fatalf("Expected an error")
		}
	})
}
