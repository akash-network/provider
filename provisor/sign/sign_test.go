package sign

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/akash-network/provider/provisor/trust"
)

const (
	helperEnabled = "PROVISOR_SIGN_HELPER"
	helperKey     = "PROVISOR_SIGN_HELPER_KEY"
	helperMode    = "PROVISOR_SIGN_HELPER_MODE"
)

// TestSignHelperProcess is not a test. It is the external signing program the
// Command tests delegate to, reached by re-executing this binary, so the
// command-backed path is exercised without depending on anything installed.
func TestSignHelperProcess(t *testing.T) {
	if os.Getenv(helperEnabled) != "1" {
		t.Skip("helper process, invoked only by the Command tests")
	}

	switch os.Getenv(helperMode) {
	case "fail":
		os.Stderr.WriteString("token locked\n")
		os.Exit(1)
	case "short":
		os.Stdout.Write([]byte("too short"))
		os.Exit(0)
	case "hang":
		time.Sleep(time.Minute)
		os.Exit(0)
	}

	privateKey, err := hex.DecodeString(os.Getenv(helperKey))
	if err != nil {
		os.Stderr.WriteString("bad key\n")
		os.Exit(1)
	}
	payload, err := io.ReadAll(os.Stdin)
	if err != nil {
		os.Stderr.WriteString("bad payload\n")
		os.Exit(1)
	}

	os.Stdout.Write(ed25519.Sign(ed25519.PrivateKey(privateKey), payload))
	os.Exit(0)
}

func helperCommand(t *testing.T, id string, privateKey ed25519.PrivateKey, mode string) *Command {
	t.Helper()

	cmd, err := NewCommand(id, os.Args[0], "-test.run=TestSignHelperProcess")
	if err != nil {
		t.Fatalf("NewCommand() error = %v", err)
	}
	t.Setenv(helperEnabled, "1")
	t.Setenv(helperKey, hex.EncodeToString(privateKey))
	t.Setenv(helperMode, mode)
	return cmd
}

func generateKey(t *testing.T, id string) (ed25519.PublicKey, *Key) {
	t.Helper()

	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	signer, err := NewKey(id, priv)
	if err != nil {
		t.Fatalf("NewKey() error = %v", err)
	}
	return pub, signer
}

func TestEnvelopeFromKeysSatisfiesThreshold(t *testing.T) {
	payload := []byte(`{"release":"0.18.0"}`)

	pub1, signer1 := generateKey(t, "op-1")
	pub2, signer2 := generateKey(t, "op-2")

	envelope, err := Envelope(context.Background(), payload, signer1, signer2)
	if err != nil {
		t.Fatalf("Envelope() error = %v", err)
	}

	keySet := trust.KeySet{Threshold: 2, Keys: map[string]ed25519.PublicKey{"op-1": pub1, "op-2": pub2}}
	if outcome := keySet.Verify(envelope.Payload, envelope.Signatures); outcome != trust.Satisfied {
		t.Fatalf("Verify() = %v, want Satisfied", outcome)
	}
}

// The property that makes custody swappable: the verifier is handed an
// envelope, not a provenance story, so where the private half lived while it
// signed leaves no trace it could act on.
func TestEnvelopeIsIndistinguishableRegardlessOfSigner(t *testing.T) {
	payload := []byte(`{"release":"0.18.0"}`)

	publicKey, privateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	inProcess, err := NewKey("op-1", privateKey)
	if err != nil {
		t.Fatalf("NewKey() error = %v", err)
	}
	external := helperCommand(t, "op-1", privateKey, "")

	fromKey, err := Envelope(context.Background(), payload, inProcess)
	if err != nil {
		t.Fatalf("Envelope(Key) error = %v", err)
	}
	fromCommand, err := Envelope(context.Background(), payload, external)
	if err != nil {
		t.Fatalf("Envelope(Command) error = %v", err)
	}

	if !bytes.Equal(fromKey.Signatures[0].Signature, fromCommand.Signatures[0].Signature) {
		t.Fatal("signatures differ between an in-process key and an external signer")
	}

	keySet := trust.KeySet{Threshold: 1, Keys: map[string]ed25519.PublicKey{"op-1": publicKey}}
	for name, envelope := range map[string]trust.Envelope{"key": fromKey, "command": fromCommand} {
		if outcome := keySet.Verify(envelope.Payload, envelope.Signatures); outcome != trust.Satisfied {
			t.Fatalf("Verify(%s) = %v, want Satisfied", name, outcome)
		}
	}
}

func TestEnvelopeRejectsDuplicateKeyIDs(t *testing.T) {
	_, signer1 := generateKey(t, "op-1")
	_, signer2 := generateKey(t, "op-1")

	_, err := Envelope(context.Background(), []byte("payload"), signer1, signer2)
	if err == nil {
		t.Fatal("Envelope() accepted two signers sharing a key id")
	}
	if !strings.Contains(err.Error(), "duplicate key id") {
		t.Fatalf("Envelope() error = %v, want it to name the duplicate", err)
	}
}

func TestEnvelopeRejectsNoSigners(t *testing.T) {
	if _, err := Envelope(context.Background(), []byte("payload")); err == nil {
		t.Fatal("Envelope() accepted an empty signer set")
	}
}

func TestEnvelopeSurfacesSignerFailure(t *testing.T) {
	_, privateKey, _ := ed25519.GenerateKey(nil)
	failing := helperCommand(t, "op-1", privateKey, "fail")

	_, err := Envelope(context.Background(), []byte("payload"), failing)
	if err == nil {
		t.Fatal("Envelope() accepted a signer that exited non-zero")
	}
	if !strings.Contains(err.Error(), "token locked") {
		t.Fatalf("Envelope() error = %v, want the signer's own stderr carried through", err)
	}
}

func TestEnvelopeRejectsMalformedSignature(t *testing.T) {
	_, privateKey, _ := ed25519.GenerateKey(nil)
	truncating := helperCommand(t, "op-1", privateKey, "short")

	_, err := Envelope(context.Background(), []byte("payload"), truncating)
	if err == nil {
		t.Fatal("Envelope() accepted a signature of the wrong length")
	}
	if !strings.Contains(err.Error(), "want 64") {
		t.Fatalf("Envelope() error = %v, want it to name the expected length", err)
	}
}

func TestEnvelopeAbortsHangingSigner(t *testing.T) {
	_, privateKey, _ := ed25519.GenerateKey(nil)
	hanging := helperCommand(t, "op-1", privateKey, "hang")

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	start := time.Now()
	if _, err := Envelope(ctx, []byte("payload"), hanging); err == nil {
		t.Fatal("Envelope() waited out a hanging signer")
	}
	if elapsed := time.Since(start); elapsed > 30*time.Second {
		t.Fatalf("Envelope() took %s to abort, want the context deadline to bound it", elapsed)
	}
}
