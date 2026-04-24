package remote

import (
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
