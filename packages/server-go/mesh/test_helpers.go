package mesh

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Test fixture constants to reduce duplication.
const (
	testServerName   = "host"
	testClientName   = "mac"
	testSecret       = "k"
	testOllamaURL    = "http://localhost:11434"
	testOllamaURL404 = "http://127.0.0.1:19999"
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

// newTestConfigWithOllamaURL creates a test config with an Ollama URL.
func newTestConfigWithOllamaURL(url string) *Config {
	return &Config{
		SelfName:      testServerName,
		SelfOllamaURL: url,
		Peers:         []Peer{{Name: testClientName, Secret: testSecret}},
	}
}

// marshalExecRequest encodes an ExecRequest to JSON.
func marshalExecRequest(cmd string, args ...string) []byte {
	req := ExecRequest{Command: cmd}
	if len(args) > 0 {
		req.Args = args
	}
	b, _ := json.Marshal(req)
	return b
}

// marshalExecRequestFull encodes an ExecRequest with all fields set.
func marshalExecRequestFull(cmd string, cwd string, env []string, args []string) []byte {
	req := ExecRequest{
		Command: cmd,
		Cwd:     cwd,
		Env:     env,
		Args:    args,
	}
	b, _ := json.Marshal(req)
	return b
}

// marshalInferenceRequest encodes an InferenceRequest to JSON.
func marshalInferenceRequest(path string, method string, body interface{}) []byte {
	var bodyJSON json.RawMessage
	if body != nil {
		bodyJSON, _ = json.Marshal(body)
	} else {
		bodyJSON = json.RawMessage(`{}`)
	}
	req := InferenceRequest{
		Path:   path,
		Method: method,
		Body:   bodyJSON,
	}
	b, _ := json.Marshal(req)
	return b
}

// decodeExecResponse unmarshals an ExecResponse from resp.Body.
func decodeExecResponse(t *testing.T, body io.Reader) *ExecResponse {
	t.Helper()
	var result ExecResponse
	if err := json.NewDecoder(body).Decode(&result); err != nil {
		t.Fatalf("decode ExecResponse: %v", err)
	}
	return &result
}

// decodeInferenceResponse unmarshals an InferenceResponse from resp.Body.
func decodeInferenceResponse(t *testing.T, body io.Reader) *InferenceResponse {
	t.Helper()
	var result InferenceResponse
	if err := json.NewDecoder(body).Decode(&result); err != nil {
		t.Fatalf("decode InferenceResponse: %v", err)
	}
	return &result
}

// decodeHealthResponse unmarshals a HealthResponse from resp.Body.
func decodeHealthResponse(t *testing.T, body io.Reader) *HealthResponse {
	t.Helper()
	var result HealthResponse
	if err := json.NewDecoder(body).Decode(&result); err != nil {
		t.Fatalf("decode HealthResponse: %v", err)
	}
	return &result
}

// doSignedExecRequestAndDecode executes a signed exec request and decodes response.
func doSignedExecRequestAndDecode(t *testing.T, ts *httptest.Server, cmd string, args ...string) *ExecResponse {
	t.Helper()
	body := marshalExecRequest(cmd, args...)
	resp := doSignedRequest(t, ts, "/mesh/exec", http.MethodPost, testSecret, testClientName, body)
	defer resp.Body.Close()
	assertStatusCode(t, resp.StatusCode, http.StatusOK)
	return decodeExecResponse(t, resp.Body)
}

// doSignedRequestAndExpectStatus executes a signed request and asserts the response status code.
// Caller is responsible for reading/closing the response body.
func doSignedRequestAndExpectStatus(t *testing.T, ts *httptest.Server, path, method, secret, origin string, body []byte, expectedStatus int) *http.Response {
	t.Helper()
	resp := doSignedRequest(t, ts, path, method, secret, origin, body)
	assertStatusCode(t, resp.StatusCode, expectedStatus)
	return resp
}

// doSignedRequestAndClose executes a signed request, closes the response body, and asserts status.
func doSignedRequestAndClose(t *testing.T, ts *httptest.Server, path, method, secret, origin string, body []byte, expectedStatus int) {
	t.Helper()
	resp := doSignedRequest(t, ts, path, method, secret, origin, body)
	defer resp.Body.Close()
	assertStatusCode(t, resp.StatusCode, expectedStatus)
}

// doSignedInferenceAndDecode executes a signed inference request and decodes the response.
func doSignedInferenceAndDecode(t *testing.T, ts *httptest.Server, path, method string, body []byte) *InferenceResponse {
	t.Helper()
	resp := doSignedRequestAndExpectStatus(t, ts, "/mesh/inference", http.MethodPost, testSecret, testClientName, body, http.StatusOK)
	defer resp.Body.Close()
	return decodeInferenceResponse(t, resp.Body)
}

// doSignedHealthAndDecode executes a signed health request and decodes the response.
func doSignedHealthAndDecode(t *testing.T, ts *httptest.Server) *HealthResponse {
	t.Helper()
	resp := doSignedRequestAndExpectStatus(t, ts, "/mesh/health", http.MethodPost, testSecret, testClientName, []byte{}, http.StatusOK)
	defer resp.Body.Close()
	return decodeHealthResponse(t, resp.Body)
}

