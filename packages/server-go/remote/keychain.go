// Package remote — OS keychain integration for secure token storage.
// Uses the OS-native credential store on each platform:
//   - macOS: Keychain Services
//   - Linux: libsecret (via the Secret Service D-Bus API)
//   - Windows: Windows Credential Manager
//
// Falls back to an encrypted file store (~/.engine/credentials.enc) when the
// OS keychain is unavailable (e.g., headless CI environments).
package remote

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sync"
)

const (
	keychainService = "engine-editor"
)

// KeychainStore persists credentials in the OS keychain with a file-based fallback.
type KeychainStore struct {
	mu       sync.RWMutex
	fallback string // path to fallback encrypted JSON file
}

// NewKeychainStore creates a KeychainStore whose fallback lives at ~/.engine/credentials.enc.
func NewKeychainStore() *KeychainStore {
	home, _ := os.UserHomeDir()
	return &KeychainStore{
		fallback: filepath.Join(home, ".engine", "credentials.enc"),
	}
}

// Set stores a credential under the given key.
// Prefers the OS keychain; falls back to an AES-256-GCM encrypted file.
func (k *KeychainStore) Set(key, value string) error {
	if err := k.osSet(key, value); err == nil {
		return nil
	}
	return k.fileSet(key, value)
}

// Get retrieves a credential by key. Returns ("", nil) if not found.
func (k *KeychainStore) Get(key string) (string, error) {
	if val, err := k.osGet(key); err == nil {
		return val, nil
	}
	return k.fileGet(key)
}

// Delete removes a credential by key.
func (k *KeychainStore) Delete(key string) error {
	_ = k.osDel(key) // best-effort
	return k.fileDel(key)
}

// ── OS-native keychain (platform-specific build tags) ────────────────────────

func (k *KeychainStore) osSet(key, value string) error {
	return osKeychainSet(keychainService, key, value)
}

func (k *KeychainStore) osGet(key string) (string, error) {
	return osKeychainGet(keychainService, key)
}

func (k *KeychainStore) osDel(key string) error {
	return osKeychainDelete(keychainService, key)
}

// ── File-based AES-256-GCM fallback ──────────────────────────────────────────

// deriveKey derives a 32-byte AES key from a machine-unique secret.
// Uses the hostname + username as the key material (deterministic but unique per machine).
func deriveKey() []byte {
	hostname, _ := os.Hostname()
	user := os.Getenv("USER")
	if user == "" {
		user = os.Getenv("USERNAME")
	}
	material := fmt.Sprintf("engine-keychain:%s:%s:%s", runtime.GOOS, hostname, user)
	h := sha256.Sum256([]byte(material))
	return h[:]
}

func (k *KeychainStore) loadStore() (map[string]string, error) {
	data, err := os.ReadFile(k.fallback)
	if os.IsNotExist(err) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, err
	}

	key := deriveKey()
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, _ := cipher.NewGCM(block)

	raw, err := base64.StdEncoding.DecodeString(string(data))
	if err != nil {
		return nil, err
	}
	if len(raw) < gcm.NonceSize() {
		return nil, fmt.Errorf("credential file too short")
	}
	nonce, ciphertext := raw[:gcm.NonceSize()], raw[gcm.NonceSize():]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("decrypt credentials: %w", err)
	}

	var store map[string]string
	if err := json.Unmarshal(plaintext, &store); err != nil {
		return nil, err
	}
	return store, nil
}

func (k *KeychainStore) saveStore(store map[string]string) error {
	if err := os.MkdirAll(filepath.Dir(k.fallback), 0o700); err != nil {
		return err
	}

	plaintext, err := json.Marshal(store)
	if err != nil {
		return err
	}

	key := deriveKey()
	block, err := aes.NewCipher(key)
	if err != nil {
		return err
	}
	gcm, _ := cipher.NewGCM(block)

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return err
	}

	ciphertext := gcm.Seal(nonce, nonce, plaintext, nil)
	encoded := base64.StdEncoding.EncodeToString(ciphertext)
	return os.WriteFile(k.fallback, []byte(encoded), 0o600)
}

func (k *KeychainStore) fileSet(key, value string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	store, err := k.loadStore()
	if err != nil {
		store = map[string]string{}
	}
	store[key] = value
	return k.saveStore(store)
}

func (k *KeychainStore) fileGet(key string) (string, error) {
	k.mu.RLock()
	defer k.mu.RUnlock()
	store, err := k.loadStore()
	if err != nil {
		return "", err
	}
	val, ok := store[key]
	if !ok {
		return "", nil
	}
	return val, nil
}

func (k *KeychainStore) fileDel(key string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	store, err := k.loadStore()
	if err != nil {
		return err
	}
	delete(store, key)
	return k.saveStore(store)
}
