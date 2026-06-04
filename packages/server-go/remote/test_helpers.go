package remote

import (
	"crypto/tls"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// newTestServer creates a test Server with a temporary storage directory and mock WebSocket handler.
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

// testRequest executes an HTTP request against the server and returns the response recorder.
func testRequest(t *testing.T, s *Server, req *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	rr := httptest.NewRecorder()
	s.mux.ServeHTTP(rr, req)
	return rr
}

// newAuthManager creates a test AuthManager with a temporary directory.
func newAuthManager(t *testing.T) *AuthManager {
	t.Helper()
	dir := t.TempDir()
	am, err := NewAuthManager(dir)
	if err != nil {
		t.Fatalf("NewAuthManager: %v", err)
	}
	return am
}

// newTLSConfig creates a test TLS config with a temporary directory.
func newTLSConfig(t *testing.T) (*tls.Config, string) {
	t.Helper()
	dir := t.TempDir()
	cfg, err := LoadOrCreateTLSConfig(dir)
	if err != nil {
		t.Fatalf("LoadOrCreateTLSConfig: %v", err)
	}
	return cfg, dir
}

// assertStatusCode checks if response status matches expected value.
func assertStatusCode(t *testing.T, got, want int) {
	t.Helper()
	if got != want {
		t.Errorf("status = %d, want %d", got, want)
	}
}

// assertJSONResponse decodes and validates a JSON response.
func assertJSONResponse(t *testing.T, body io.Reader, v interface{}) error {
	t.Helper()
	return json.NewDecoder(body).Decode(v)
}

// defaultAuthToken creates a test token with a reasonable expiration.
func defaultAuthToken(t *testing.T, am *AuthManager) string {
	t.Helper()
	token, err := am.IssueToken("test-device", time.Hour)
	if err != nil {
		t.Fatalf("IssueToken: %v", err)
	}
	return token
}

// mustDecodeJSON decodes a JSON response body, fataling on error.
func mustDecodeJSON(t *testing.T, body io.Reader, v interface{}) {
	t.Helper()
	if err := json.NewDecoder(body).Decode(v); err != nil {
		t.Fatalf("decode response: %v", err)
	}
}

// decodeJSONResponse decodes a JSON response body and returns it as a map.
func decodeJSONResponse(t *testing.T, body io.Reader, _ map[string]any) map[string]any {
	t.Helper()
	var resp map[string]any
	if err := json.NewDecoder(body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return resp
}

// assertJSONField checks if a JSON response field equals expected value.
func assertJSONField(t *testing.T, resp map[string]any, field string, expected interface{}) {
	t.Helper()
	got := resp[field]
	if got != expected {
		t.Errorf("%s = %v, want %v", field, got, expected)
	}
}

// assertJSONFieldNotEmpty checks if a JSON response field is non-empty string.
func assertJSONFieldNotEmpty(t *testing.T, resp map[string]any, field string) {
	t.Helper()
	val := resp[field]
	if str, ok := val.(string); !ok || str == "" {
		t.Errorf("%s = %v, want non-empty string", field, val)
	}
}
