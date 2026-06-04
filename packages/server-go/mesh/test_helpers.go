package mesh

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// newMeshServer creates an httptest server with mesh handlers.
func newMeshServer(t *testing.T, cfg *Config) *httptest.Server {
	t.Helper()
	srv := NewServer(cfg)
	mux := http.NewServeMux()
	srv.Register(mux)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts
}

// newSignedRequest constructs an HTTP request with mesh signature headers.
func newSignedRequest(t *testing.T, ts *httptest.Server, path, method, secret, origin string, body []byte) *http.Request {
	t.Helper()
	timestamp, sig := signRequest(secret, origin, body)
	req, err := http.NewRequest(method, ts.URL+path, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(originHeader, origin)
	req.Header.Set(timestampHeader, timestamp)
	req.Header.Set(signatureHeader, sig)
	return req
}

// defaultMeshTestConfig returns a standard config for mesh server tests.
func defaultMeshTestConfig() *Config {
	return &Config{
		SelfName: "host",
		Peers:    []Peer{{Name: "mac", Secret: "k"}},
	}
}

// assertStatusCode checks if response status matches expected value.
func assertStatusCode(t *testing.T, got, want int) {
	t.Helper()
	if got != want {
		t.Errorf("status = %d, want %d", got, want)
	}
}

// assertJSONResponse decodes and validates a JSON response.
func assertJSONResponse(t *testing.T, body io.Reader, want interface{}) error {
	t.Helper()
	return json.NewDecoder(body).Decode(want)
}

// doSignedRequest executes a signed request and returns the response.
// Caller is responsible for closing resp.Body.
func doSignedRequest(t *testing.T, ts *httptest.Server, path, method, secret, origin string, body []byte) *http.Response {
	t.Helper()
	req := newSignedRequest(t, ts, path, method, secret, origin, body)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	return resp
}

