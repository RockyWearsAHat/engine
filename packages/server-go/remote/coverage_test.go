package remote

import (
	"crypto/cipher"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// ── auth.go ──────────────────────────────────────────────────────────────────

func TestNewAuthManager_RandReadError(t *testing.T) {
	orig := randReadFn
	randReadFn = func(b []byte) (int, error) { return 0, errors.New("injected rand error") }
	defer func() { randReadFn = orig }()
	_, err := NewAuthManager(t.TempDir())
	if err == nil {
		t.Fatal("expected error when rand.Read fails")
	}
}

func TestIssueToken_MarshalError(t *testing.T) {
	am, err := NewAuthManager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	orig := jsonMarshalFn
	jsonMarshalFn = func(v any) ([]byte, error) { return nil, errors.New("injected marshal error") }
	defer func() { jsonMarshalFn = orig }()
	_, err = am.IssueToken("dev1", time.Hour)
	if err == nil {
		t.Fatal("expected error when json.Marshal fails")
	}
}

func TestRotateSecret_RandReadError(t *testing.T) {
	dir := t.TempDir()
	am, err := NewAuthManager(dir)
	if err != nil {
		t.Fatal(err)
	}
	orig := randReadFn
	randReadFn = func(b []byte) (int, error) { return 0, errors.New("injected rand error") }
	defer func() { randReadFn = orig }()
	if err := am.RotateSecret(dir); err == nil {
		t.Fatal("expected error when rand.Read fails during rotate")
	}
}

// ── pairing.go ────────────────────────────────────────────────────────────────

func TestGenerateCode_RandIntError(t *testing.T) {
	orig := randIntFn
	randIntFn = func(_ io.Reader, _ *big.Int) (*big.Int, error) {
		return nil, errors.New("injected rand int error")
	}
	defer func() { randIntFn = orig }()
	pm := NewPairingManager()
	_, err := pm.GenerateCode()
	if err == nil {
		t.Fatal("expected error when rand.Int fails")
	}
}

// ── keychain.go ───────────────────────────────────────────────────────────────

func TestKeychainStore_Get_FileFallback(t *testing.T) {
	origGet := osKeychainGetFn
	osKeychainGetFn = func(_, _ string) (string, error) { return "", errors.New("injected get error") }
	defer func() { osKeychainGetFn = origGet }()
	k := &KeychainStore{fallback: filepath.Join(t.TempDir(), "creds.enc")}
	if err := k.fileSet("k1", "v1"); err != nil {
		t.Fatal(err)
	}
	val, err := k.Get("k1")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if val != "v1" {
		t.Errorf("expected v1, got %q", val)
	}
}

func TestKeychainStore_Get_OSPath(t *testing.T) {
	origGet := osKeychainGetFn
	osKeychainGetFn = func(_, _ string) (string, error) { return "injected", nil }
	defer func() { osKeychainGetFn = origGet }()
	k := &KeychainStore{fallback: filepath.Join(t.TempDir(), "creds.enc")}
	val, err := k.Get("anykey")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if val != "injected" {
		t.Errorf("expected injected, got %q", val)
	}
}

func TestOsKeychainGet_NotFound(t *testing.T) {
	_, err := osKeychainGet("engine-nonexistent-svc-abc123", "nonexistent-key-xyz-456")
	if err == nil {
		t.Fatal("expected error for non-existent keychain entry")
	}
}

func TestDeriveKey_UsernameEnv(t *testing.T) {
	t.Setenv("USER", "")
	t.Setenv("USERNAME", "testuser-coverage")
	key := deriveKey()
	if len(key) != 32 {
		t.Errorf("expected 32-byte key, got %d", len(key))
	}
}

func TestLoadStore_ReadError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("chmod 000 not supported on Windows")
	}
	fallback := filepath.Join(t.TempDir(), "creds.enc")
	if err := os.WriteFile(fallback, []byte("data"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(fallback, 0000); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(fallback, 0600) //nolint:errcheck
	k := &KeychainStore{fallback: fallback}
	_, err := k.loadStore()
	if err == nil {
		t.Fatal("expected error reading unreadable file")
	}
}

func TestLoadStore_AESError(t *testing.T) {
	orig := aesCipherFn
	aesCipherFn = func(key []byte) (cipher.Block, error) { return nil, errors.New("injected AES error") }
	defer func() { aesCipherFn = orig }()
	fallback := filepath.Join(t.TempDir(), "creds.enc")
	if err := os.WriteFile(fallback, []byte("dGVzdA=="), 0600); err != nil {
		t.Fatal(err)
	}
	k := &KeychainStore{fallback: fallback}
	_, err := k.loadStore()
	if err == nil {
		t.Fatal("expected AES cipher error")
	}
}

func TestLoadStore_GCMDecryptError(t *testing.T) {
	fallback := filepath.Join(t.TempDir(), "creds.enc")
	raw := make([]byte, 28)
	encoded := base64.StdEncoding.EncodeToString(raw)
	if err := os.WriteFile(fallback, []byte(encoded), 0600); err != nil {
		t.Fatal(err)
	}
	k := &KeychainStore{fallback: fallback}
	_, err := k.loadStore()
	if err == nil {
		t.Fatal("expected GCM decrypt error for bogus ciphertext")
	}
}

func TestLoadStore_JSONError(t *testing.T) {
	fallback := filepath.Join(t.TempDir(), "creds.enc")
	k := &KeychainStore{fallback: fallback, mu: sync.RWMutex{}}
	if err := k.saveStore(map[string]string{"x": "y"}); err != nil {
		t.Fatal(err)
	}
	orig := jsonUnmarshalStoreFn
	jsonUnmarshalStoreFn = func(data []byte, v any) error { return errors.New("injected unmarshal error") }
	defer func() { jsonUnmarshalStoreFn = orig }()
	_, err := k.loadStore()
	if err == nil {
		t.Fatal("expected unmarshal error")
	}
}

func TestSaveStore_JSONMarshalError(t *testing.T) {
	orig := jsonMarshalStoreFn
	jsonMarshalStoreFn = func(v any) ([]byte, error) { return nil, errors.New("injected marshal error") }
	defer func() { jsonMarshalStoreFn = orig }()
	k := &KeychainStore{fallback: filepath.Join(t.TempDir(), "creds.enc")}
	if err := k.saveStore(map[string]string{"a": "b"}); err == nil {
		t.Fatal("expected marshal error")
	}
}

func TestSaveStore_AESError(t *testing.T) {
	orig := aesCipherFn
	aesCipherFn = func(key []byte) (cipher.Block, error) { return nil, errors.New("injected AES error") }
	defer func() { aesCipherFn = orig }()
	k := &KeychainStore{fallback: filepath.Join(t.TempDir(), "creds.enc")}
	if err := k.saveStore(map[string]string{"a": "b"}); err == nil {
		t.Fatal("expected AES cipher error")
	}
}

func TestSaveStore_RandReadError(t *testing.T) {
	orig := randReadFullFn
	randReadFullFn = func(_ io.Reader, b []byte) (int, error) { return 0, errors.New("injected read error") }
	defer func() { randReadFullFn = orig }()
	k := &KeychainStore{fallback: filepath.Join(t.TempDir(), "creds.enc")}
	if err := k.saveStore(map[string]string{"a": "b"}); err == nil {
		t.Fatal("expected rand read error")
	}
}

func TestFileSet_LoadError(t *testing.T) {
	origSet := osKeychainSetFn
	osKeychainSetFn = func(_, _, _ string) error { return errors.New("injected OS error") }
	defer func() { osKeychainSetFn = origSet }()
	dir := t.TempDir()
	fallback := filepath.Join(dir, "sub", "creds.enc")
	if err := os.MkdirAll(filepath.Dir(fallback), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fallback, []byte("!!!not-base64!!!"), 0600); err != nil {
		t.Fatal(err)
	}
	k := &KeychainStore{fallback: fallback}
	if err := k.Set("key", "val"); err != nil {
		t.Fatalf("expected Set to succeed even with loadStore error: %v", err)
	}
}

func TestFileDel_LoadError(t *testing.T) {
	fallback := filepath.Join(t.TempDir(), "creds.enc")
	if err := os.WriteFile(fallback, []byte("!!!not-base64!!!"), 0600); err != nil {
		t.Fatal(err)
	}
	k := &KeychainStore{fallback: fallback}
	if err := k.fileDel("anykey"); err == nil {
		t.Fatal("expected error when loadStore fails in fileDel")
	}
}

// ── tls.go ────────────────────────────────────────────────────────────────────

func TestLoadOrCreateTLSConfig_ECDSAError(t *testing.T) {
	orig := ecdsaGenKeyFn
	ecdsaGenKeyFn = func(_ elliptic.Curve, _ io.Reader) (*ecdsa.PrivateKey, error) {
		return nil, errors.New("injected ECDSA error")
	}
	defer func() { ecdsaGenKeyFn = orig }()
	_, err := LoadOrCreateTLSConfig(t.TempDir())
	if err == nil {
		t.Fatal("expected error when ECDSA key generation fails")
	}
}

func TestLoadOrCreateTLSConfig_SerialError(t *testing.T) {
	orig := randIntTLSFn
	randIntTLSFn = func(_ io.Reader, _ *big.Int) (*big.Int, error) {
		return nil, errors.New("injected serial error")
	}
	defer func() { randIntTLSFn = orig }()
	_, err := LoadOrCreateTLSConfig(t.TempDir())
	if err == nil {
		t.Fatal("expected error when serial generation fails")
	}
}

func TestLoadOrCreateTLSConfig_CreateCertError(t *testing.T) {
	orig := x509CreateCertFn
	x509CreateCertFn = func(_ io.Reader, _, _ *x509.Certificate, _ any, _ any) ([]byte, error) {
		return nil, errors.New("injected cert creation error")
	}
	defer func() { x509CreateCertFn = orig }()
	_, err := LoadOrCreateTLSConfig(t.TempDir())
	if err == nil {
		t.Fatal("expected error when x509.CreateCertificate fails")
	}
}

func TestLoadOrCreateTLSConfig_PemEncodeCertError(t *testing.T) {
	orig := pemEncodeWriterFn
	call := 0
	pemEncodeWriterFn = func(w io.Writer, b *pem.Block) error {
		call++
		if call == 1 {
			return errors.New("injected pem encode cert error")
		}
		return pem.Encode(w, b)
	}
	defer func() { pemEncodeWriterFn = orig }()
	_, err := LoadOrCreateTLSConfig(t.TempDir())
	if err == nil {
		t.Fatal("expected error when pem.Encode for cert fails")
	}
}

func TestLoadOrCreateTLSConfig_MarshalECKeyError(t *testing.T) {
	orig := x509MarshalECKeyFn
	x509MarshalECKeyFn = func(_ *ecdsa.PrivateKey) ([]byte, error) {
		return nil, errors.New("injected marshal EC key error")
	}
	defer func() { x509MarshalECKeyFn = orig }()
	_, err := LoadOrCreateTLSConfig(t.TempDir())
	if err == nil {
		t.Fatal("expected error when x509.MarshalECPrivateKey fails")
	}
}

func TestLoadOrCreateTLSConfig_PemEncodeKeyError(t *testing.T) {
	orig := pemEncodeWriterFn
	call := 0
	pemEncodeWriterFn = func(w io.Writer, b *pem.Block) error {
		call++
		if call == 2 {
			return errors.New("injected pem encode key error")
		}
		return pem.Encode(w, b)
	}
	defer func() { pemEncodeWriterFn = orig }()
	_, err := LoadOrCreateTLSConfig(t.TempDir())
	if err == nil {
		t.Fatal("expected error when pem.Encode for key fails")
	}
}

func TestLoadOrCreateTLSConfig_LoadX509Error(t *testing.T) {
	orig := tlsLoadX509KeyPairFn
	tlsLoadX509KeyPairFn = func(_, _ string) (tls.Certificate, error) {
		return tls.Certificate{}, errors.New("injected load X509 error")
	}
	defer func() { tlsLoadX509KeyPairFn = orig }()
	_, err := LoadOrCreateTLSConfig(t.TempDir())
	if err == nil {
		t.Fatal("expected error when tls.LoadX509KeyPair fails")
	}
}

// ── server.go ─────────────────────────────────────────────────────────────────

func newTestServerCov(t *testing.T) *Server {
	t.Helper()
	srv, err := NewServer(Config{StoragePath: t.TempDir(), Port: "0"}, nil)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	return srv
}

func TestHandlePair_RandReadError(t *testing.T) {
	srv := newTestServerCov(t)
	code, _ := srv.Pairing.GenerateCode()
	orig := randReadFn2
	randReadFn2 = func(b []byte) (int, error) { return 0, errors.New("injected rand error") }
	defer func() { randReadFn2 = orig }()
	req := httptest.NewRequest(http.MethodPost, "/remote/pair",
		strings.NewReader(`{"code":"`+code+`"}`))
	rr := httptest.NewRecorder()
	srv.mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("expected 500 on rand error, got %d", rr.Code)
	}
}

func TestHandlePair_IssueTokenError(t *testing.T) {
	srv := newTestServerCov(t)
	code, _ := srv.Pairing.GenerateCode()
	orig := jsonMarshalFn
	jsonMarshalFn = func(v any) ([]byte, error) { return nil, errors.New("injected marshal error") }
	defer func() { jsonMarshalFn = orig }()
	req := httptest.NewRequest(http.MethodPost, "/remote/pair",
		strings.NewReader(`{"code":"`+code+`"}`))
	rr := httptest.NewRecorder()
	srv.mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("expected 500 on IssueToken error, got %d", rr.Code)
	}
}

func TestHandleRefresh_IssueTokenError(t *testing.T) {
	srv := newTestServerCov(t)
	token, _ := srv.Auth.IssueToken("dev1", time.Hour)
	orig := jsonMarshalFn
	jsonMarshalFn = func(v any) ([]byte, error) { return nil, errors.New("injected marshal error") }
	defer func() { jsonMarshalFn = orig }()
	req := httptest.NewRequest(http.MethodPost, "/remote/refresh", http.NoBody)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	srv.mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("expected 500 on IssueToken error in refresh, got %d", rr.Code)
	}
}

func TestListenAndServeTLS_PairingCodeError(t *testing.T) {
	srv := newTestServerCov(t)
	orig := serverGenPairingCodeFn
	serverGenPairingCodeFn = func(_ *PairingManager) (string, error) {
		return "", errors.New("injected pairing code error")
	}
	defer func() { serverGenPairingCodeFn = orig }()
	if err := srv.ListenAndServeTLS(); err == nil {
		t.Fatal("expected error when pairing code generation fails")
	}
}

func TestListenAndServeTLS_ServeSuccess(t *testing.T) {
	srv := newTestServerCov(t)
	origTimer := serverPairingCleanupTimerFn
	serverPairingCleanupTimerFn = func() {}
	defer func() { serverPairingCleanupTimerFn = origTimer }()
	origServe := serverListenAndServeTLSFn
	serverListenAndServeTLSFn = func(_ *http.Server) error { return nil }
	defer func() { serverListenAndServeTLSFn = origServe }()
	if err := srv.ListenAndServeTLS(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	runtime.Gosched()
}

func TestDefaultServerListenAndServeTLSFn(t *testing.T) {
	// Call the real lambda body to cover it. It will fail because no TLS cert is configured.
	s := &http.Server{Addr: "localhost:0"}
	origFn := serverListenAndServeTLSFn
	err := origFn(s)
	if err == nil {
		t.Fatal("expected error from real ListenAndServeTLS with no TLS config")
	}
}
