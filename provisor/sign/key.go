package sign

import (
	"context"
	"crypto/ed25519"
	"fmt"
)

// Key signs in process with an Ed25519 private key held in memory. It is the
// right backend when the publishing process may legitimately hold key material
// itself, which in practice means development and nothing else.
type Key struct {
	id         string
	privateKey ed25519.PrivateKey
}

func NewKey(id string, privateKey ed25519.PrivateKey) (*Key, error) {
	if id == "" {
		return nil, fmt.Errorf("sign: key id is empty")
	}
	if len(privateKey) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("sign: key %q is %d bytes, want %d", id, len(privateKey), ed25519.PrivateKeySize)
	}
	return &Key{id: id, privateKey: privateKey}, nil
}

func (k *Key) KeyID() string { return k.id }

func (k *Key) Sign(_ context.Context, payload []byte) ([]byte, error) {
	return ed25519.Sign(k.privateKey, payload), nil
}
