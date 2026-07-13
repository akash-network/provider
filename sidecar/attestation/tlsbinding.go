package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha512"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"time"
)

// TLSBinding holds an ephemeral TLS keypair generated at sidecar startup.
// The public key hash can be embedded in the TEE report_data field to bind
// the TLS channel to the attestation evidence:
//
//	report_data = SHA-512(tls_pubkey_der || nonce)[:64]
//
// This proves to the tenant that the TLS endpoint they're talking to runs
// inside the attested TEE — not a MITM proxy on the host plane.
type TLSBinding struct {
	Certificate tls.Certificate
	PubKeyDER   []byte // DER-encoded public key for channel binding
}

// GenerateEphemeralKeypair creates an ECDSA P-384 TLS keypair with a
// self-signed certificate valid for 24 hours. The keypair is generated
// fresh on each sidecar startup — it's ephemeral by design.
func GenerateEphemeralKeypair() (*TLSBinding, error) {
	key, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate ECDSA key: %w", err)
	}

	pubKeyDER, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		return nil, fmt.Errorf("marshal public key: %w", err)
	}

	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "akash-attestation-sidecar"},
		NotBefore:    time.Now(),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}

	certDER, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return nil, fmt.Errorf("create certificate: %w", err)
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, fmt.Errorf("marshal private key: %w", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})

	tlsCert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, fmt.Errorf("load TLS keypair: %w", err)
	}

	return &TLSBinding{
		Certificate: tlsCert,
		PubKeyDER:   pubKeyDER,
	}, nil
}

// ComputeBoundReportData computes the report_data that binds a TLS channel
// to a tenant-provided nonce:
//
//	report_data = SHA-512(tls_pubkey_der || nonce)[:64]
//
// The tenant verifies this by:
//  1. Getting the sidecar's TLS public key from the /info endpoint
//  2. Requesting a quote with bind_tls=true
//  3. Verifying that the SNP report's REPORT_DATA matches this hash
//  4. Knowing the TLS session they're on uses the same key
func ComputeBoundReportData(pubKeyDER []byte, nonce [64]byte) [64]byte {
	h := sha512.New()
	h.Write(pubKeyDER)
	h.Write(nonce[:])
	sum := h.Sum(nil) // 64 bytes from SHA-512
	var result [64]byte
	copy(result[:], sum[:64])
	return result
}
