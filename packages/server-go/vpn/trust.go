package vpn

import (
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// TrustedDevice represents a client approved for VPN tunnel access.
type TrustedDevice struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	PublicKey string `json:"publicKey"`
	AddedAt   string `json:"addedAt"`
}

// TrustStore manages the set of approved client identities.
type TrustStore struct {
	mu      sync.RWMutex
	devices []TrustedDevice
	path    string
}

// NewTrustStore loads or creates the trust store at the given path.
func NewTrustStore(storagePath string) (*TrustStore, error) {
	path := filepath.Join(storagePath, "trusted_devices.json")
	store := &TrustStore{path: path}

	data, err := os.ReadFile(path)
	if err == nil {
		if err := json.Unmarshal(data, &store.devices); err != nil {
			store.devices = nil
		}
	}

	return store, nil
}

// AddDevice registers a new trusted client public key.
func (ts *TrustStore) AddDevice(id, name string, pubKey ed25519.PublicKey) error {
	ts.mu.Lock()
	defer ts.mu.Unlock()

	ts.devices = append(ts.devices, TrustedDevice{
		ID:        id,
		Name:      name,
		PublicKey: hex.EncodeToString(pubKey),
	})

	return ts.save()
}

// RemoveDevice revokes trust for a device by ID.
func (ts *TrustStore) RemoveDevice(id string) error {
	ts.mu.Lock()
	defer ts.mu.Unlock()

	filtered := ts.devices[:0]
	for _, d := range ts.devices {
		if d.ID != id {
			filtered = append(filtered, d)
		}
	}
	ts.devices = filtered

	return ts.save()
}

// IsTrusted checks whether a public key belongs to a trusted device.
func (ts *TrustStore) IsTrusted(pubKey ed25519.PublicKey) bool {
	ts.mu.RLock()
	defer ts.mu.RUnlock()

	keyHex := hex.EncodeToString(pubKey)
	for _, d := range ts.devices {
		if d.PublicKey == keyHex {
			return true
		}
	}
	return false
}

// List returns all trusted devices.
func (ts *TrustStore) List() []TrustedDevice {
	ts.mu.RLock()
	defer ts.mu.RUnlock()

	out := make([]TrustedDevice, len(ts.devices))
	copy(out, ts.devices)
	return out
}

func (ts *TrustStore) save() error {
	data, err := json.MarshalIndent(ts.devices, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal trust store: %w", err)
	}
	return os.WriteFile(ts.path, data, 0600)
}
