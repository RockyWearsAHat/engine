package vpn

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
)

// ed25519GenKey is injectable for tests to simulate key-generation failure.
var ed25519GenKey = ed25519.GenerateKey

// Identity holds an Ed25519 key pair for mutual authentication.
type Identity struct {
	PublicKey  ed25519.PublicKey
	PrivateKey ed25519.PrivateKey
}

// LoadOrCreateIdentity loads an Ed25519 identity from disk or generates one.
func LoadOrCreateIdentity(storagePath string) (*Identity, error) {
	pubPath := filepath.Join(storagePath, "identity.pub")
	keyPath := filepath.Join(storagePath, "identity.key")

	if data, err := os.ReadFile(keyPath); err == nil {
		block, _ := pem.Decode(data)
		if block != nil && len(block.Bytes) == ed25519.PrivateKeySize {
			priv := ed25519.PrivateKey(block.Bytes)
			pub := priv.Public().(ed25519.PublicKey)
			return &Identity{PublicKey: pub, PrivateKey: priv}, nil
		}
	}

	pub, priv, err := ed25519GenKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate ed25519 key: %w", err)
	}

	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "ED25519 PRIVATE KEY", Bytes: priv})
	if err := os.WriteFile(keyPath, keyPEM, 0600); err != nil {
		return nil, fmt.Errorf("write private key: %w", err)
	}

	pubPEM := pem.EncodeToMemory(&pem.Block{Type: "ED25519 PUBLIC KEY", Bytes: pub})
	if err := os.WriteFile(pubPath, pubPEM, 0644); err != nil {
		return nil, fmt.Errorf("write public key: %w", err)
	}

	return &Identity{PublicKey: pub, PrivateKey: priv}, nil
}
