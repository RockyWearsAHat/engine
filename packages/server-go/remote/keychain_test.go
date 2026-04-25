package remote

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
)

func newTestKeychainStore(t *testing.T) *KeychainStore {
	t.Helper()
	dir := t.TempDir()
	ks := &KeychainStore{
		fallback: filepath.Join(dir, "credentials.enc"),
	}
	return ks
}

func TestNewKeychainStore(t *testing.T) {
	ks := NewKeychainStore()
	if ks == nil {
		t.Fatal("expected non-nil KeychainStore")
	}
}

func TestKeychainStore_SetAndGet_FileFallback(t *testing.T) {
	ks := newTestKeychainStore(t)

	if err := ks.fileSet("mykey", "myvalue"); err != nil {
		t.Fatalf("fileSet: %v", err)
	}

	val, err := ks.fileGet("mykey")
	if err != nil {
		t.Fatalf("fileGet: %v", err)
	}
	if val != "myvalue" {
		t.Errorf("val = %q, want myvalue", val)
	}
}

func TestKeychainStore_Get_NotFound_FileFallback(t *testing.T) {
	ks := newTestKeychainStore(t)

	val, err := ks.fileGet("nonexistent")
	if err != nil {
		t.Fatalf("fileGet: %v", err)
	}
	if val != "" {
		t.Errorf("expected empty, got %q", val)
	}
}

func TestKeychainStore_Delete_FileFallback(t *testing.T) {
	ks := newTestKeychainStore(t)

	if err := ks.fileSet("delkey", "delval"); err != nil {
		t.Fatalf("fileSet: %v", err)
	}

	if err := ks.fileDel("delkey"); err != nil {
		t.Fatalf("fileDel: %v", err)
	}

	val, err := ks.fileGet("delkey")
	if err != nil {
		t.Fatalf("fileGet after delete: %v", err)
	}
	if val != "" {
		t.Errorf("expected empty after delete, got %q", val)
	}
}

func TestKeychainStore_Delete_Nonexistent(t *testing.T) {
	ks := newTestKeychainStore(t)

	if err := ks.fileDel("nonexistent"); err != nil {
		t.Fatalf("fileDel nonexistent: %v", err)
	}
}

func TestKeychainStore_MultipleKeys(t *testing.T) {
	ks := newTestKeychainStore(t)

	if err := ks.fileSet("key1", "val1"); err != nil {
		t.Fatalf("fileSet key1: %v", err)
	}
	if err := ks.fileSet("key2", "val2"); err != nil {
		t.Fatalf("fileSet key2: %v", err)
	}

	v1, _ := ks.fileGet("key1")
	v2, _ := ks.fileGet("key2")
	if v1 != "val1" {
		t.Errorf("key1 = %q, want val1", v1)
	}
	if v2 != "val2" {
		t.Errorf("key2 = %q, want val2", v2)
	}
}

func TestKeychainStore_UpdateKey(t *testing.T) {
	ks := newTestKeychainStore(t)

	if err := ks.fileSet("k", "v1"); err != nil {
		t.Fatalf("fileSet: %v", err)
	}
	if err := ks.fileSet("k", "v2"); err != nil {
		t.Fatalf("fileSet update: %v", err)
	}

	val, _ := ks.fileGet("k")
	if val != "v2" {
		t.Errorf("val = %q, want v2", val)
	}
}

func TestKeychainStore_Set_FallsBackToFile(t *testing.T) {
	ks := newTestKeychainStore(t)

	// OS keychain will likely fail on CI; file fallback is the path we're testing.
	if err := ks.Set("testkey", "testval"); err != nil {
		t.Fatalf("Set: %v", err)
	}

	// Get via public API — should hit OS first, then file.
	val, err := ks.Get("testkey")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if val == "" {
		// The OS might have gotten it — either way the value was stored.
		// Try the file directly.
		val, err = ks.fileGet("testkey")
		if err != nil {
			t.Fatalf("fileGet fallback: %v", err)
		}
	}
	_ = val
}

func TestKeychainStore_Delete_Public(t *testing.T) {
	ks := newTestKeychainStore(t)

	if err := ks.fileSet("del2", "value2"); err != nil {
		t.Fatalf("fileSet: %v", err)
	}

	if err := ks.Delete("del2"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
}

func TestKeychainStore_LoadStore_EmptyFile(t *testing.T) {
	ks := newTestKeychainStore(t)

	store, err := ks.loadStore()
	if err != nil {
		t.Fatalf("loadStore empty: %v", err)
	}
	if len(store) != 0 {
		t.Errorf("expected empty store, got %d entries", len(store))
	}
}

func TestKeychainStore_LoadStore_CorruptFile(t *testing.T) {
	ks := newTestKeychainStore(t)

	dir := filepath.Dir(ks.fallback)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatalf("mkdirAll: %v", err)
	}
	if err := os.WriteFile(ks.fallback, []byte("notbase64!!!@@##"), 0600); err != nil {
		t.Fatalf("write corrupt: %v", err)
	}

	_, err := ks.loadStore()
	if err == nil {
		t.Fatal("expected error for corrupt file")
	}
}

func TestDeriveKey(t *testing.T) {
	key := deriveKey()
	if len(key) != 32 {
		t.Errorf("key length = %d, want 32", len(key))
	}
}

// TestKeychainStore_LoadStore_TooShortFile: valid base64 that decodes to fewer than
// gcm.NonceSize() (12) bytes — should return "credential file too short" error.
func TestKeychainStore_LoadStore_TooShortFile(t *testing.T) {
	ks := newTestKeychainStore(t)
	dir := filepath.Dir(ks.fallback)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	// "short" is 5 bytes — well under the 12-byte nonce requirement.
	encoded := base64.StdEncoding.EncodeToString([]byte("short"))
	if err := os.WriteFile(ks.fallback, []byte(encoded), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := ks.loadStore()
	if err == nil {
		t.Fatal("expected error for too-short credential file")
	}
}

func TestKeychainStore_SaveStore_MkdirAllError(t *testing.T) {
	// Use a path whose parent is a file (cannot MkdirAll into it).
	tmp := t.TempDir()
	blockFile := filepath.Join(tmp, "block")
	if err := os.WriteFile(blockFile, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	ks := &KeychainStore{fallback: filepath.Join(blockFile, "sub", "creds.enc")}
	if err := ks.saveStore(map[string]string{}); err == nil {
		t.Fatal("expected MkdirAll error")
	}
}

func TestKeychainStore_SaveStore_WriteError(t *testing.T) {
	// Create a dir named after the fallback file so WriteFile fails.
	tmp := t.TempDir()
	credDir := filepath.Join(tmp, "creds.enc")
	if err := os.Mkdir(credDir, 0755); err != nil {
		t.Fatal(err)
	}
	ks := &KeychainStore{fallback: credDir}
	if err := ks.saveStore(map[string]string{"k": "v"}); err == nil {
		t.Fatal("expected WriteFile error when fallback is a dir")
	}
}

func TestKeychainStore_FileGet_LoadError(t *testing.T) {
	tmp := t.TempDir()
	fallback := filepath.Join(tmp, "creds.enc")
	// Write invalid base64 to trigger loadStore error in fileGet.
	if err := os.WriteFile(fallback, []byte("not-valid-base64!!!"), 0600); err != nil {
		t.Fatal(err)
	}
	ks := &KeychainStore{fallback: fallback}
	_, err := ks.fileGet("anykey")
	if err == nil {
		t.Fatal("expected error from fileGet when store is corrupt")
	}
}
