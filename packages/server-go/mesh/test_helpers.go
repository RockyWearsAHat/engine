package mesh

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
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

// doSignedExecFullAndDecode executes a full exec request with all fields and decodes the response.
func doSignedExecFullAndDecode(t *testing.T, ts *httptest.Server, cmd, cwd string, env []string, args []string) *ExecResponse {
	t.Helper()
	body := marshalExecRequestFull(cmd, cwd, env, args)
	resp := doSignedRequestAndExpectStatus(t, ts, "/mesh/exec", http.MethodPost, testSecret, testClientName, body, http.StatusOK)
	defer resp.Body.Close()
	return decodeExecResponse(t, resp.Body)
}

// doSignedInferenceFullAndDecode executes an inference request with all fields and decodes the response.
func doSignedInferenceFullAndDecode(t *testing.T, ts *httptest.Server, path, method string, bodyObj interface{}) *InferenceResponse {
	t.Helper()
	body := marshalInferenceRequest(path, method, bodyObj)
	return doSignedInferenceAndDecode(t, ts, path, method, body)
}

// testMethodNotAllowed verifies that a given path rejects non-POST requests.
func testMethodNotAllowed(t *testing.T, ts *httptest.Server, path string) {
	t.Helper()
	doSignedRequestAndClose(t, ts, path, http.MethodGet, testSecret, testClientName, []byte{}, http.StatusMethodNotAllowed)
}

// testEndpointMethodNotAllowed creates a server and verifies any endpoint rejects non-POST requests.
func testEndpointMethodNotAllowed(t *testing.T, path string) {
	t.Helper()
	ts := newMeshServer(t, defaultMeshTestConfig())
	testMethodNotAllowed(t, ts, path)
}

// testEndpointBadJSON creates a server and verifies any endpoint rejects malformed JSON.
func testEndpointBadJSON(t *testing.T, path string) {
	t.Helper()
	ts := newMeshServer(t, defaultMeshTestConfig())
	testSignedBadJSON(t, ts, path)
}

// testSignedBadJSON verifies that a given path rejects malformed JSON.
func testSignedBadJSON(t *testing.T, ts *httptest.Server, path string) {
	t.Helper()
	doSignedRequestAndClose(t, ts, path, http.MethodPost, testSecret, testClientName, []byte("not-json{{{"), http.StatusBadRequest)
}

// testSignedInferenceValidation verifies that /mesh/inference rejects empty path requests.
func testSignedInferenceValidation(t *testing.T, ts *httptest.Server) {
	t.Helper()
	body := marshalInferenceRequest("", "", nil)
	doSignedRequestAndClose(t, ts, "/mesh/inference", http.MethodPost, testSecret, testClientName, body, http.StatusBadRequest)
}

// testExecEndpointMethodNotAllowed creates an exec server and verifies it rejects non-POST requests.
func testExecEndpointMethodNotAllowed(t *testing.T) {
	t.Helper()
	testEndpointMethodNotAllowed(t, "/mesh/exec")
}

// testExecEndpointBadJSON creates an exec server and verifies it rejects malformed JSON.
func testExecEndpointBadJSON(t *testing.T) {
	t.Helper()
	testEndpointBadJSON(t, "/mesh/exec")
}

// testInferenceEndpointMethodNotAllowed creates an inference server and verifies it rejects non-POST requests.
func testInferenceEndpointMethodNotAllowed(t *testing.T) {
	t.Helper()
	testEndpointMethodNotAllowed(t, "/mesh/inference")
}

// testInferenceEndpointBadJSON creates an inference server and verifies it rejects malformed JSON.
func testInferenceEndpointBadJSON(t *testing.T) {
	t.Helper()
	testEndpointBadJSON(t, "/mesh/inference")
}

// newPeerFromTestServer constructs a Peer from a test server URL using test constants.
func newPeerFromTestServer(t *testing.T, ts *httptest.Server) *Peer {
	t.Helper()
	return &Peer{
		Name:    testServerName,
		Address: strings.TrimPrefix(ts.URL, "http://"),
		Secret:  testSecret,
	}
}

// newServerAndClientPair creates a mesh server with default config, constructs a peer, and returns both with a client.
func newServerAndClientPair(t *testing.T) (*httptest.Server, *Peer, *Client) {
	t.Helper()
	ts := newMeshServer(t, defaultMeshTestConfig())
	peer := newPeerFromTestServer(t, ts)
	c := NewClient(testClientName)
	return ts, peer, c
}

// newDefaultMeshServerWithTestConfig creates a standard mesh server for testing.
func newDefaultMeshServerWithTestConfig(t *testing.T) *httptest.Server {
	t.Helper()
	return newMeshServer(t, defaultMeshTestConfig())
}

// doExecRequestAndAssertStatus executes an exec request with standard parameters and asserts the status code.
func doExecRequestAndAssertStatus(t *testing.T, ts *httptest.Server, cmd string, expectedStatus int, args ...string) *ExecResponse {
	t.Helper()
	body := marshalExecRequest(cmd, args...)
	resp := doSignedRequestAndExpectStatus(t, ts, "/mesh/exec", http.MethodPost, testSecret, testClientName, body, expectedStatus)
	defer resp.Body.Close()
	if expectedStatus != http.StatusOK {
		return nil
	}
	return decodeExecResponse(t, resp.Body)
}

// doInferenceRequestAndAssertStatus executes an inference request with standard parameters and asserts the status code.
func doInferenceRequestAndAssertStatus(t *testing.T, ts *httptest.Server, path, method string, expectedStatus int, body []byte) *InferenceResponse {
	t.Helper()
	resp := doSignedRequestAndExpectStatus(t, ts, "/mesh/inference", http.MethodPost, testSecret, testClientName, body, expectedStatus)
	defer resp.Body.Close()
	return decodeInferenceResponse(t, resp.Body)
}

// execRequestWithStatusCheck is a consolidated helper for tests that create a server, exec, and verify status.
func execRequestWithStatusCheck(t *testing.T, cfg *Config, cmd string, expectedStatus int, args ...string) *ExecResponse {
	t.Helper()
	ts := newMeshServer(t, cfg)
	body := marshalExecRequest(cmd, args...)
	resp := doSignedRequestAndExpectStatus(t, ts, "/mesh/exec", http.MethodPost, testSecret, testClientName, body, expectedStatus)
	defer resp.Body.Close()
	return decodeExecResponse(t, resp.Body)
}

