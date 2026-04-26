package vpn

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadOrCreateIdentity_GeneratesNew(t *testing.T) {
	dir := t.TempDir()
	id, err := LoadOrCreateIdentity(dir)
	if err != nil {
		t.Fatalf("LoadOrCreateIdentity: %v", err)
	}
	if len(id.PublicKey) == 0 {
		t.Error("expected non-empty public key")
	}
	if len(id.PrivateKey) == 0 {
		t.Error("expected non-empty private key")
	}
}

func TestLoadOrCreateIdentity_LoadsExisting(t *testing.T) {
	dir := t.TempDir()
	id1, err := LoadOrCreateIdentity(dir)
	if err != nil {
		t.Fatalf("first LoadOrCreateIdentity: %v", err)
	}

	id2, err := LoadOrCreateIdentity(dir)
	if err != nil {
		t.Fatalf("second LoadOrCreateIdentity: %v", err)
	}

	if !id1.PublicKey.Equal(id2.PublicKey) {
		t.Error("public keys should match on reload")
	}
}

func TestIdentity_CanSignAndVerify(t *testing.T) {
	dir := t.TempDir()
	id, err := LoadOrCreateIdentity(dir)
	if err != nil {
		t.Fatalf("LoadOrCreateIdentity: %v", err)
	}

	message := []byte("hello engine")
	sig := ed25519.Sign(id.PrivateKey, message)

	if !ed25519.Verify(id.PublicKey, message, sig) {
		t.Error("signature verification failed")
	}
}

func TestNewTrustStore_Empty(t *testing.T) {
	dir := t.TempDir()
	ts, err := NewTrustStore(dir)
	if err != nil {
		t.Fatalf("NewTrustStore: %v", err)
	}
	if len(ts.List()) != 0 {
		t.Errorf("expected 0 devices, got %d", len(ts.List()))
	}
}

func TestNewTrustStore_LoadsExisting(t *testing.T) {
	dir := t.TempDir()
	ts1, _ := NewTrustStore(dir)
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	if err := ts1.AddDevice("dev-1", "TestDevice", pub); err != nil {
		t.Fatalf("AddDevice: %v", err)
	}

	ts2, err := NewTrustStore(dir)
	if err != nil {
		t.Fatalf("second NewTrustStore: %v", err)
	}
	if len(ts2.List()) != 1 {
		t.Errorf("expected 1 device after reload, got %d", len(ts2.List()))
	}
}

func TestTrustStore_AddAndIsTrusted(t *testing.T) {
	dir := t.TempDir()
	ts, _ := NewTrustStore(dir)

	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	if err := ts.AddDevice("dev-trust", "TrustMe", pub); err != nil {
		t.Fatalf("AddDevice: %v", err)
	}

	if !ts.IsTrusted(pub) {
		t.Error("key should be trusted after AddDevice")
	}
}

func TestTrustStore_IsTrusted_NotFound(t *testing.T) {
	dir := t.TempDir()
	ts, _ := NewTrustStore(dir)

	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	if ts.IsTrusted(pub) {
		t.Error("key should not be trusted before adding")
	}
}

func TestTrustStore_RemoveDevice(t *testing.T) {
	dir := t.TempDir()
	ts, _ := NewTrustStore(dir)

	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	if err := ts.AddDevice("to-remove", "RemoveMe", pub); err != nil {
		t.Fatalf("AddDevice: %v", err)
	}

	if err := ts.RemoveDevice("to-remove"); err != nil {
		t.Fatalf("RemoveDevice: %v", err)
	}

	if ts.IsTrusted(pub) {
		t.Error("key should not be trusted after removal")
	}
}

func TestTrustStore_List(t *testing.T) {
	dir := t.TempDir()
	ts, _ := NewTrustStore(dir)

	pub1, _, _ := ed25519.GenerateKey(rand.Reader)
	pub2, _, _ := ed25519.GenerateKey(rand.Reader)
	_ = ts.AddDevice("d1", "Device1", pub1)
	_ = ts.AddDevice("d2", "Device2", pub2)

	devices := ts.List()
	if len(devices) != 2 {
		t.Errorf("expected 2 devices, got %d", len(devices))
	}
}

func TestTrustStore_RemoveNonexistent(t *testing.T) {
	dir := t.TempDir()
	ts, _ := NewTrustStore(dir)

	if err := ts.RemoveDevice("nonexistent"); err != nil {
		t.Fatalf("RemoveDevice nonexistent: %v", err)
	}
}

func TestTrustStore_Save_Error(t *testing.T) {
	dir := t.TempDir()
	ts, err := NewTrustStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	// Make the storage dir read-only so save() fails on write.
	if err := os.Chmod(dir, 0444); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(dir, 0755) //nolint:errcheck
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	if err := ts.AddDevice("id1", "dev1", pub); err == nil {
		t.Fatal("expected save error when dir is read-only")
	}
}

func TestLoadOrCreateIdentity_WritePubKeyError(t *testing.T) {
	dir := t.TempDir()
	// Block writing the public key by creating a directory named identity.pub.
	if err := os.Mkdir(filepath.Join(dir, "identity.pub"), 0755); err != nil {
		t.Fatal(err)
	}
	_, err := LoadOrCreateIdentity(dir)
	if err == nil {
		t.Fatal("expected error when identity.pub is a directory")
	}
}

func TestLoadOrCreateIdentity_KeyGenError(t *testing.T) {
	dir := t.TempDir()
	orig := ed25519GenKey
	ed25519GenKey = func(_ io.Reader) (ed25519.PublicKey, ed25519.PrivateKey, error) {
		return nil, nil, errors.New("injected key gen error")
	}
	defer func() { ed25519GenKey = orig }()
	_, err := LoadOrCreateIdentity(dir)
	if err == nil {
		t.Fatal("expected error when key gen fails")
	}
}

func TestNewTrustStore_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	// Write invalid JSON to trusted_devices.json.
	if err := os.WriteFile(filepath.Join(dir, "trusted_devices.json"), []byte("not-json"), 0600); err != nil {
		t.Fatal(err)
	}
	ts, err := NewTrustStore(dir)
	if err != nil {
		t.Fatalf("NewTrustStore should not error: %v", err)
	}
	// devices should be nil/empty after unmarshal failure
	if len(ts.List()) != 0 {
		t.Errorf("expected 0 devices after invalid JSON, got %d", len(ts.List()))
	}
}

func TestTrustStore_RemoveDevice_KeepOthers(t *testing.T) {
	dir := t.TempDir()
	ts, _ := NewTrustStore(dir)

	pub1, _, _ := ed25519.GenerateKey(rand.Reader)
	pub2, _, _ := ed25519.GenerateKey(rand.Reader)
	_ = ts.AddDevice("keep-me", "Keeper", pub1)
	_ = ts.AddDevice("remove-me", "Remover", pub2)

	if err := ts.RemoveDevice("remove-me"); err != nil {
		t.Fatalf("RemoveDevice: %v", err)
	}

	devices := ts.List()
	if len(devices) != 1 || devices[0].ID != "keep-me" {
		t.Errorf("expected keep-me to remain, got %v", devices)
	}
}

func TestTrustStore_Save_MarshalError(t *testing.T) {
	dir := t.TempDir()
	ts, _ := NewTrustStore(dir)
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	_ = ts.AddDevice("d1", "Dev1", pub)

	orig := jsonMarshalIndentFn
	jsonMarshalIndentFn = func(_ any, _, _ string) ([]byte, error) {
		return nil, errors.New("injected marshal error")
	}
	defer func() { jsonMarshalIndentFn = orig }()

	pub2, _, _ := ed25519.GenerateKey(rand.Reader)
	if err := ts.AddDevice("d2", "Dev2", pub2); err == nil {
		t.Fatal("expected error when marshal fails")
	}
}
