package vpn

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func newTestTunnel(t *testing.T) *Tunnel {
	t.Helper()
	dir := t.TempDir()
	cfg := Config{
		Enabled:     true,
		Port:        "0",
		StoragePath: dir,
	}
	tunnel, err := NewTunnel(cfg)
	if err != nil {
		t.Fatalf("NewTunnel: %v", err)
	}
	return tunnel
}

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.Port != "8443" {
		t.Errorf("Port = %q, want 8443", cfg.Port)
	}
	if cfg.Enabled {
		t.Error("default Enabled should be false")
	}
}

func TestNewTunnel_Creates(t *testing.T) {
	tun := newTestTunnel(t)
	if tun.Identity == nil {
		t.Fatal("expected non-nil Identity")
	}
	if tun.Trust == nil {
		t.Fatal("expected non-nil TrustStore")
	}
	if tun.Auth == nil {
		t.Fatal("expected non-nil AuthManager")
	}
	if tun.Pairing == nil {
		t.Fatal("expected non-nil PairingManager")
	}
}

func newTestMux(t *testing.T, tun *Tunnel) *http.ServeMux {
	t.Helper()
	mux := http.NewServeMux()
	wsHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusSwitchingProtocols)
	})
	tun.RegisterRoutes(mux, wsHandler)
	return mux
}

func TestVPNFingerprint_Handler(t *testing.T) {
	tun := newTestTunnel(t)
	mux := newTestMux(t, tun)

	req := httptest.NewRequest("GET", "/vpn/fingerprint", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rr.Code)
	}
	var resp map[string]string
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["fingerprint"] == "" {
		t.Error("expected non-empty fingerprint")
	}
}

func TestVPNHealth_Handler(t *testing.T) {
	tun := newTestTunnel(t)
	mux := newTestMux(t, tun)

	req := httptest.NewRequest("GET", "/health", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rr.Code)
	}
}

func TestHandleVPNPair_WrongMethod(t *testing.T) {
	tun := newTestTunnel(t)
	mux := newTestMux(t, tun)

	req := httptest.NewRequest("GET", "/vpn/pair", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", rr.Code)
	}
}

func TestHandleVPNPair_InvalidCode(t *testing.T) {
	tun := newTestTunnel(t)
	mux := newTestMux(t, tun)

	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	body, _ := json.Marshal(map[string]string{
		"code":      "000000",
		"publicKey": hex.EncodeToString(pub),
		"name":      "TestDevice",
	})
	req := httptest.NewRequest("POST", "/vpn/pair", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rr.Code)
	}
	var resp map[string]interface{}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["ok"] != false {
		t.Error("expected ok=false for invalid code")
	}
}

func TestHandleVPNPair_ValidCode(t *testing.T) {
	tun := newTestTunnel(t)
	mux := newTestMux(t, tun)

	code, err := tun.Pairing.GenerateCode()
	if err != nil {
		t.Fatalf("GenerateCode: %v", err)
	}

	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	body, _ := json.Marshal(map[string]string{
		"code":      code,
		"publicKey": hex.EncodeToString(pub),
		"name":      "ValidDevice",
	})
	req := httptest.NewRequest("POST", "/vpn/pair", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rr.Code)
	}
	var resp map[string]interface{}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["ok"] != true {
		t.Errorf("ok = %v, want true", resp["ok"])
	}
	if resp["token"] == nil || resp["token"] == "" {
		t.Error("expected non-empty token")
	}
}

func TestHandleVPNPair_BadJSON(t *testing.T) {
	tun := newTestTunnel(t)
	mux := newTestMux(t, tun)

	req := httptest.NewRequest("POST", "/vpn/pair", bytes.NewBufferString("{invalid"))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rr.Code)
	}
}

func TestHandleVPNPair_InvalidPublicKey(t *testing.T) {
	tun := newTestTunnel(t)
	mux := newTestMux(t, tun)

	code, _ := tun.Pairing.GenerateCode()
	body, _ := json.Marshal(map[string]string{
		"code":      code,
		"publicKey": "notahexkey",
	})
	req := httptest.NewRequest("POST", "/vpn/pair", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rr.Code)
	}
}

func TestHandleDevices_RequiresAuth(t *testing.T) {
	tun := newTestTunnel(t)
	mux := newTestMux(t, tun)

	req := httptest.NewRequest("GET", "/vpn/devices", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rr.Code)
	}
}

func TestHandleDevices_WithToken(t *testing.T) {
	tun := newTestTunnel(t)
	mux := newTestMux(t, tun)

	token, err := tun.Auth.IssueToken("test-dev", time.Hour)
	if err != nil {
		t.Fatalf("IssueToken: %v", err)
	}

	req := httptest.NewRequest("GET", "/vpn/devices", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rr.Code)
	}
}

func TestHandleRevoke_RequiresAuth(t *testing.T) {
	tun := newTestTunnel(t)
	mux := newTestMux(t, tun)

	req := httptest.NewRequest("POST", "/vpn/revoke", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rr.Code)
	}
}

func TestHandleRevoke_WithToken(t *testing.T) {
	tun := newTestTunnel(t)
	mux := newTestMux(t, tun)

	token, err := tun.Auth.IssueToken("revoke-dev", time.Hour)
	if err != nil {
		t.Fatalf("IssueToken: %v", err)
	}

	body, _ := json.Marshal(map[string]string{"deviceId": "some-device"})
	req := httptest.NewRequest("POST", "/vpn/revoke", bytes.NewBuffer(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rr.Code)
	}
}

func TestHandleRevoke_WrongMethod(t *testing.T) {
	tun := newTestTunnel(t)
	mux := newTestMux(t, tun)

	token, _ := tun.Auth.IssueToken("dev", time.Hour)
	req := httptest.NewRequest("GET", "/vpn/revoke", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", rr.Code)
	}
}

func TestHandleRevoke_BadJSON(t *testing.T) {
	tun := newTestTunnel(t)
	mux := newTestMux(t, tun)

	token, _ := tun.Auth.IssueToken("dev", time.Hour)
	req := httptest.NewRequest("POST", "/vpn/revoke", bytes.NewBufferString("{bad"))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rr.Code)
	}
}

func TestGetLocalIP_ReturnsNonEmpty(t *testing.T) {
	ip := getLocalIP()
	if ip == "" {
		t.Error("expected non-empty IP")
	}
}

func TestHandleVPNPair_ShortPublicKey(t *testing.T) {
	tun := newTestTunnel(t)
	mux := newTestMux(t, tun)

	code, _ := tun.Pairing.GenerateCode()
	// Valid hex but wrong length (not 32 bytes)
	body, _ := json.Marshal(map[string]string{
		"code":      code,
		"publicKey": "deadbeef",
	})
	req := httptest.NewRequest("POST", "/vpn/pair", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rr.Code)
	}
}

func TestHandleVPNPair_NoName_SetsDefault(t *testing.T) {
	tun := newTestTunnel(t)
	mux := newTestMux(t, tun)

	code, _ := tun.Pairing.GenerateCode()
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	body, _ := json.Marshal(map[string]string{
		"code":      code,
		"publicKey": hex.EncodeToString(pub),
	})
	req := httptest.NewRequest("POST", "/vpn/pair", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rr.Code)
	}
	var resp map[string]interface{}
	json.NewDecoder(rr.Body).Decode(&resp) //nolint:errcheck
	if resp["ok"] != true {
		t.Errorf("ok = %v, want true", resp["ok"])
	}
}

func TestListenAndServeTLS_TLSConfigError(t *testing.T) {
	tun := newTestTunnel(t)
	badStorage := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(badStorage, []byte("x"), 0644); err != nil {
		t.Fatalf("write bad storage marker: %v", err)
	}
	tun.Config.StoragePath = badStorage

	err := tun.ListenAndServeTLS(http.NewServeMux())
	if err == nil {
		t.Fatal("expected ListenAndServeTLS error for invalid storage path")
	}
}
