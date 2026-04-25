package remote

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.Port != "3443" {
		t.Errorf("Port = %q, want 3443", cfg.Port)
	}
	if cfg.Enabled {
		t.Error("default Enabled should be false")
	}
}

func TestEnsureStorageDir(t *testing.T) {
	dir := t.TempDir()
	cfg := Config{StoragePath: dir + "/sub/path"}
	if err := EnsureStorageDir(cfg); err != nil {
		t.Fatalf("EnsureStorageDir: %v", err)
	}
}

func newTestServer(t *testing.T) *Server {
	t.Helper()
	dir := t.TempDir()
	cfg := Config{
		Enabled:     true,
		Port:        "0",
		StoragePath: dir,
	}
	wsHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusSwitchingProtocols)
	})
	s, err := NewServer(cfg, wsHandler)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	return s
}

func TestNewServer_CreatesServer(t *testing.T) {
	s := newTestServer(t)
	if s == nil {
		t.Fatal("expected non-nil server")
	}
	if s.Auth == nil {
		t.Fatal("expected Auth manager")
	}
	if s.Pairing == nil {
		t.Fatal("expected Pairing manager")
	}
}

func TestRemoteStatus_Handler(t *testing.T) {
	s := newTestServer(t)
	req := httptest.NewRequest("GET", "/remote/status", nil)
	rr := httptest.NewRecorder()
	s.mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rr.Code)
	}
	var resp map[string]interface{}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["engine"] != true {
		t.Error("expected engine=true in status")
	}
}

func TestHealth_Handler(t *testing.T) {
	s := newTestServer(t)
	req := httptest.NewRequest("GET", "/health", nil)
	rr := httptest.NewRecorder()
	s.mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rr.Code)
	}
	var resp map[string]interface{}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["status"] != "ok" {
		t.Error("expected status=ok in health")
	}
}

func TestHandlePair_WrongMethod(t *testing.T) {
	s := newTestServer(t)
	req := httptest.NewRequest("GET", "/remote/pair", nil)
	rr := httptest.NewRecorder()
	s.mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", rr.Code)
	}
}

func TestHandlePair_InvalidCode(t *testing.T) {
	s := newTestServer(t)
	body := bytes.NewBufferString(`{"code": "000000"}`)
	req := httptest.NewRequest("POST", "/remote/pair", body)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	s.mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rr.Code)
	}
	var resp map[string]interface{}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["ok"] != false {
		t.Error("expected ok=false for invalid code")
	}
}

func TestHandlePair_ValidCode(t *testing.T) {
	s := newTestServer(t)
	code, err := s.Pairing.GenerateCode()
	if err != nil {
		t.Fatalf("GenerateCode: %v", err)
	}

	body, _ := json.Marshal(map[string]string{"code": code})
	req := httptest.NewRequest("POST", "/remote/pair", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	s.mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rr.Code)
	}
	var resp map[string]interface{}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["ok"] != true {
		t.Errorf("expected ok=true, got %v", resp["ok"])
	}
	if resp["token"] == "" {
		t.Error("expected non-empty token")
	}
}

func TestHandlePair_BadJSON(t *testing.T) {
	s := newTestServer(t)
	req := httptest.NewRequest("POST", "/remote/pair", bytes.NewBufferString("{invalid"))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	s.mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rr.Code)
	}
}

func TestHandleRefresh_ValidToken(t *testing.T) {
	s := newTestServer(t)
	token, err := s.Auth.IssueToken("refresh-device", 3600*1000000000)
	if err != nil {
		t.Fatalf("IssueToken: %v", err)
	}

	req := httptest.NewRequest("POST", "/remote/refresh", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	s.mux.ServeHTTP(rr, req)

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
}

func TestHandleRefresh_InvalidToken(t *testing.T) {
	s := newTestServer(t)
	req := httptest.NewRequest("POST", "/remote/refresh", nil)
	rr := httptest.NewRecorder()
	s.mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rr.Code)
	}
}

func TestWS_RequiresAuth(t *testing.T) {
	s := newTestServer(t)
	req := httptest.NewRequest("GET", "/ws", nil)
	rr := httptest.NewRecorder()
	s.mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rr.Code)
	}
}

func TestListenAndServeTLS_TLSConfigError(t *testing.T) {
	s := newTestServer(t)
	badStorage := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(badStorage, []byte("x"), 0644); err != nil {
		t.Fatalf("write bad storage marker: %v", err)
	}
	s.Config.StoragePath = badStorage

	err := s.ListenAndServeTLS()
	if err == nil {
		t.Fatal("expected ListenAndServeTLS error for invalid storage path")
	}
}
