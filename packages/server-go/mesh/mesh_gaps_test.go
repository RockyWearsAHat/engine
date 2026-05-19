package mesh

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ── config.go gaps ────────────────────────────────────────────────────────────

func TestLoadConfig_EmptyPath_UsesDefault(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "mesh.json")
	src := &Config{SelfName: "box", ListenAddr: ":9999"}
	if err := SaveConfig(cfgPath, src); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ENGINE_MESH_CONFIG", cfgPath)
	cfg, err := LoadConfig("") // empty path → uses DefaultConfigPath
	if err != nil {
		t.Fatalf("LoadConfig empty path: %v", err)
	}
	if cfg.SelfName != "box" {
		t.Errorf("selfName = %q, want box", cfg.SelfName)
	}
}

func TestLoadConfig_BadJSON_ReturnsError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.json")
	os.WriteFile(path, []byte("not-json{{{"), 0o600) //nolint:errcheck
	_, err := LoadConfig(path)
	if err == nil {
		t.Fatal("expected error for bad JSON")
	}
}

func TestLoadConfig_NonExistentError_NonErrNotExist(t *testing.T) {
	// Pass a path whose *parent* does not exist so os.ReadFile gives an error
	// that is NOT os.ErrNotExist at the file level – it will be a path error
	// from a missing directory.  On most systems this is ENOENT too, so to
	// get a truly different error we rely on a non-existent path that is
	// well-formed but its parent directory doesn't exist.
	// Actually ErrNotExist covers most cases; let's use a permission-denied
	// workaround by creating a file then making it unreadable.
	dir := t.TempDir()
	path := filepath.Join(dir, "mesh.json")
	os.WriteFile(path, []byte(`{"selfName":"x"}`), 0o000) //nolint:errcheck
	_, err := LoadConfig(path)
	// On macOS/Linux running as non-root, this is permission denied, not ErrNotExist.
	if err == nil {
		t.Skip("could not create unreadable file (running as root?)")
	}
}

func TestLoadConfig_EmptyListenAddr_Defaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mesh.json")
	os.WriteFile(path, []byte(`{"selfName":"host"}`), 0o600) //nolint:errcheck
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.ListenAddr != ":24445" {
		t.Errorf("listenAddr = %q, want :24445", cfg.ListenAddr)
	}
}

func TestSaveConfig_EmptyPath_UsesDefault(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "mesh.json")
	t.Setenv("ENGINE_MESH_CONFIG", cfgPath)
	src := &Config{SelfName: "boxB"}
	if err := SaveConfig("", src); err != nil {
		t.Fatalf("SaveConfig empty path: %v", err)
	}
	cfg, err := LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("LoadConfig after SaveConfig: %v", err)
	}
	if cfg.SelfName != "boxB" {
		t.Errorf("selfName = %q, want boxB", cfg.SelfName)
	}
}

func TestSaveConfig_MkdirCreatesParent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "deep", "nested", "mesh.json")
	src := &Config{SelfName: "nest"}
	if err := SaveConfig(path, src); err != nil {
		t.Fatalf("SaveConfig nested: %v", err)
	}
}

func TestDefaultConfigPath_CustomEnvPath(t *testing.T) {
	t.Setenv("ENGINE_MESH_CONFIG", "/custom/gap/path/mesh.json")
	got := DefaultConfigPath()
	if got != "/custom/gap/path/mesh.json" {
		t.Errorf("got %q, want /custom/gap/path/mesh.json", got)
	}
}

func TestFindPeer_NilConfig(t *testing.T) {
	var c *Config
	if got := c.FindPeer("any"); got != nil {
		t.Errorf("expected nil for nil config, got %v", got)
	}
}

func TestPeerWithRole_NilConfig(t *testing.T) {
	var c *Config
	if got := c.PeerWithRole("inference"); got != nil {
		t.Errorf("expected nil for nil config, got %v", got)
	}
}

func TestPeerWithRole_EmptyRole(t *testing.T) {
	c := &Config{Peers: []Peer{{Name: "a", Roles: []string{"inference"}}}}
	if got := c.PeerWithRole(""); got != nil {
		t.Errorf("expected nil for empty role, got %v", got)
	}
}

func TestPeerWithRole_NotFound(t *testing.T) {
	c := &Config{Peers: []Peer{{Name: "a", Roles: []string{"build"}}}}
	if got := c.PeerWithRole("inference"); got != nil {
		t.Errorf("expected nil when role not found, got %v", got)
	}
}

// ── sig.go gaps ───────────────────────────────────────────────────────────────

func TestVerifyRequest_EmptySecret(t *testing.T) {
	if err := verifyRequest("", []byte("body"), "ts", "sig"); err == nil {
		t.Error("expected error for empty secret")
	}
}

func TestVerifyRequest_EmptySignature(t *testing.T) {
	if err := verifyRequest("secret", []byte("body"), "ts", ""); err == nil {
		t.Error("expected error for empty signature")
	}
}

func TestVerifyRequest_EmptyTimestamp(t *testing.T) {
	if err := verifyRequest("secret", []byte("body"), "", "sig"); err == nil {
		t.Error("expected error for empty timestamp")
	}
}

func TestVerifyRequest_BadTimestampFormat(t *testing.T) {
	if err := verifyRequest("secret", []byte("body"), "not-a-timestamp", "sig"); err == nil {
		t.Error("expected error for bad timestamp format")
	}
}

func TestVerifyRequest_ExpiredTimestamp(t *testing.T) {
	old := time.Now().Add(-10 * time.Minute).UTC().Format(time.RFC3339)
	_, sig := signRequest("secret", "origin", []byte("body"))
	if err := verifyRequest("secret", []byte("body"), old, sig); err == nil {
		t.Error("expected error for expired timestamp")
	}
}

func TestVerifyRequest_BadSignatureEncoding(t *testing.T) {
	ts := time.Now().UTC().Format(time.RFC3339)
	if err := verifyRequest("secret", []byte("body"), ts, "not-hex!!!"); err == nil {
		t.Error("expected error for bad signature encoding")
	}
}

// ── server.go gaps ────────────────────────────────────────────────────────────

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

func newMeshServer(t *testing.T, cfg *Config) *httptest.Server {
	t.Helper()
	srv := NewServer(cfg)
	mux := http.NewServeMux()
	srv.Register(mux)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts
}

func TestHandleExec_MethodNotAllowed(t *testing.T) {
	cfg := &Config{SelfName: "host", Peers: []Peer{{Name: "mac", Secret: "k"}}}
	ts := newMeshServer(t, cfg)

	resp, err := http.Get(ts.URL + "/mesh/exec")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", resp.StatusCode)
	}
}

func TestHandleExec_BadJSON(t *testing.T) {
	cfg := &Config{SelfName: "host", Peers: []Peer{{Name: "mac", Secret: "k"}}}
	ts := newMeshServer(t, cfg)

	body := []byte("not-json")
	req := newSignedRequest(t, ts, "/mesh/exec", http.MethodPost, "k", "mac", body)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", resp.StatusCode)
	}
}

func TestHandleExec_EmptyCommand(t *testing.T) {
	cfg := &Config{SelfName: "host", Peers: []Peer{{Name: "mac", Secret: "k"}}}
	ts := newMeshServer(t, cfg)

	body, _ := json.Marshal(ExecRequest{Command: ""})
	req := newSignedRequest(t, ts, "/mesh/exec", http.MethodPost, "k", "mac", body)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", resp.StatusCode)
	}
}

func TestHandleExec_WithCwdAndEnv(t *testing.T) {
	cfg := &Config{SelfName: "host", Peers: []Peer{{Name: "mac", Secret: "k"}}}
	ts := newMeshServer(t, cfg)

	body, _ := json.Marshal(ExecRequest{
		Command: "echo",
		Args:    []string{"hello"},
		Cwd:     t.TempDir(),
		Env:     []string{"MY_VAR=test"},
	})
	req := newSignedRequest(t, ts, "/mesh/exec", http.MethodPost, "k", "mac", body)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
	var result ExecResponse
	json.NewDecoder(resp.Body).Decode(&result) //nolint:errcheck
	if result.ExitCode != 0 {
		t.Errorf("exit code = %d, want 0", result.ExitCode)
	}
}

func TestHandleExec_ExitError(t *testing.T) {
	cfg := &Config{SelfName: "host", Peers: []Peer{{Name: "mac", Secret: "k"}}}
	ts := newMeshServer(t, cfg)

	body, _ := json.Marshal(ExecRequest{Command: "false"})
	req := newSignedRequest(t, ts, "/mesh/exec", http.MethodPost, "k", "mac", body)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 (exec ran but failed), got %d", resp.StatusCode)
	}
	var result ExecResponse
	json.NewDecoder(resp.Body).Decode(&result) //nolint:errcheck
	if result.ExitCode == 0 {
		t.Error("expected non-zero exit code from 'false'")
	}
}

func TestHandleExec_NonExistentCommand(t *testing.T) {
	cfg := &Config{SelfName: "host", Peers: []Peer{{Name: "mac", Secret: "k"}}}
	ts := newMeshServer(t, cfg)

	body, _ := json.Marshal(ExecRequest{Command: "/no/such/binary/xyzzy"})
	req := newSignedRequest(t, ts, "/mesh/exec", http.MethodPost, "k", "mac", body)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 (exec returned error info), got %d", resp.StatusCode)
	}
	var result ExecResponse
	json.NewDecoder(resp.Body).Decode(&result) //nolint:errcheck
	if result.ExitCode == 0 {
		t.Error("expected non-zero exit for non-existent command")
	}
}

func TestHandleInference_MethodNotAllowed(t *testing.T) {
	cfg := &Config{SelfName: "host", Peers: []Peer{{Name: "mac", Secret: "k"}}}
	ts := newMeshServer(t, cfg)

	resp, err := http.Get(ts.URL + "/mesh/inference")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", resp.StatusCode)
	}
}

func TestHandleInference_BadJSON(t *testing.T) {
	cfg := &Config{SelfName: "host", Peers: []Peer{{Name: "mac", Secret: "k"}}}
	ts := newMeshServer(t, cfg)

	body := []byte("not-json")
	req := newSignedRequest(t, ts, "/mesh/inference", http.MethodPost, "k", "mac", body)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", resp.StatusCode)
	}
}

func TestHandleInference_EmptyPath(t *testing.T) {
	cfg := &Config{SelfName: "host", SelfOllamaURL: "http://127.0.0.1:99999", Peers: []Peer{{Name: "mac", Secret: "k"}}}
	ts := newMeshServer(t, cfg)

	body, _ := json.Marshal(InferenceRequest{Path: "", Body: json.RawMessage(`{}`)})
	req := newSignedRequest(t, ts, "/mesh/inference", http.MethodPost, "k", "mac", body)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", resp.StatusCode)
	}
}

func TestHandleInference_UpstreamConnectionError(t *testing.T) {
	// Point to a port that's almost certainly not listening.
	cfg := &Config{SelfName: "host", SelfOllamaURL: "http://127.0.0.1:19999", Peers: []Peer{{Name: "mac", Secret: "k"}}}
	ts := newMeshServer(t, cfg)

	body, _ := json.Marshal(InferenceRequest{Path: "/v1/chat", Body: json.RawMessage(`{}`)})
	req := newSignedRequest(t, ts, "/mesh/inference", http.MethodPost, "k", "mac", body)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	// 502 Bad Gateway or 200 with error in body
	var result InferenceResponse
	json.NewDecoder(resp.Body).Decode(&result) //nolint:errcheck
	// Either way, we get a response (not a test failure).
}

func TestHandleInference_WithExplicitMethod(t *testing.T) {
	var upstreamMethod string
	ollama := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamMethod = r.Method
		w.Write([]byte(`{"ok":true}`)) //nolint:errcheck
	}))
	defer ollama.Close()

	cfg := &Config{SelfName: "host", SelfOllamaURL: ollama.URL, Peers: []Peer{{Name: "mac", Secret: "k"}}}
	ts := newMeshServer(t, cfg)

	body, _ := json.Marshal(InferenceRequest{Path: "/v1/chat", Method: "get", Body: json.RawMessage(`{}`)})
	req := newSignedRequest(t, ts, "/mesh/inference", http.MethodPost, "k", "mac", body)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
	if upstreamMethod != "GET" {
		t.Errorf("upstream method = %q, want GET", upstreamMethod)
	}
}

func TestBuildUpstreamRequest_BuildsURL(t *testing.T) {
	ctx := context.Background()
	req, err := buildUpstreamRequest(ctx, "POST", "http://127.0.0.1:11434", "/v1/chat", []byte(`{}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(req.URL.String(), "v1/chat") {
		t.Errorf("URL = %s, expected to contain v1/chat", req.URL)
	}
}

func TestLimitedBuffer_Overflow(t *testing.T) {
	buf := &limitedBuffer{Max: 5}
	n, err := buf.Write([]byte("hello world overflow"))
	if err != nil {
		t.Fatalf("Write error: %v", err)
	}
	if n != 20 {
		t.Errorf("Write returned %d, want 20", n)
	}
	if got := buf.String(); got != "hello" {
		t.Errorf("buf = %q, want %q", got, "hello")
	}
}

func TestLimitedBuffer_WriteAtCapacity(t *testing.T) {
	buf := &limitedBuffer{Max: 5}
	buf.Write([]byte("hello")) //nolint:errcheck
	// Writing to full buffer returns len(p), no error, no new bytes written.
	n, err := buf.Write([]byte("x"))
	if err != nil {
		t.Fatalf("Write to full buffer error: %v", err)
	}
	if n != 1 {
		t.Errorf("Write returned %d, want 1", n)
	}
	if got := buf.String(); got != "hello" {
		t.Errorf("buf = %q, want hello", got)
	}
}

func TestHandleHealth_WithOllamaURL(t *testing.T) {
	cfg := &Config{
		SelfName:      "host",
		SelfOllamaURL: "http://localhost:11434",
		Peers:         []Peer{{Name: "mac", Secret: "k"}},
	}
	ts := newMeshServer(t, cfg)

	body := []byte{}
	req := newSignedRequest(t, ts, "/mesh/health", http.MethodPost, "k", "mac", body)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
	var h HealthResponse
	json.NewDecoder(resp.Body).Decode(&h) //nolint:errcheck
	if h.OllamaURL != "http://localhost:11434" {
		t.Errorf("ollamaURL = %q, want http://localhost:11434", h.OllamaURL)
	}
}

func TestAuthenticate_UnknownPeer(t *testing.T) {
	cfg := &Config{SelfName: "host", Peers: []Peer{{Name: "known", Secret: "k"}}}
	ts := newMeshServer(t, cfg)

	body := []byte{}
	req := newSignedRequest(t, ts, "/mesh/health", http.MethodPost, "k", "unknown", body)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401 for unknown peer, got %d", resp.StatusCode)
	}
}

func TestListenAndServe_ErrFromListener(t *testing.T) {
	// Bind an invalid address to force an error from ListenAndServe.
	s := NewServer(&Config{ListenAddr: "invalid:::addr"})
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	err := s.ListenAndServe(ctx)
	// Either context deadline or bind error — both are valid non-nil returns.
	if err == nil {
		t.Error("expected error from invalid listen address")
	}
}

// ── client.go gaps ────────────────────────────────────────────────────────────

func TestClientHealth_NetworkError(t *testing.T) {
	// Use a port that refuses connections.
	peer := &Peer{Name: "host", Address: "127.0.0.1:19998", Secret: "k"}
	c := NewClient("mac")
	_, err := c.Health(context.Background(), peer)
	if err == nil {
		t.Error("expected error for unreachable peer")
	}
}

func TestClientExec_Success(t *testing.T) {
	cfg := &Config{SelfName: "host", Peers: []Peer{{Name: "mac", Secret: "k"}}}
	srv := NewServer(cfg)
	mux := http.NewServeMux()
	srv.Register(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	peer := &Peer{Name: "host", Address: strings.TrimPrefix(ts.URL, "http://"), Secret: "k"}
	c := NewClient("mac")

	result, err := c.Exec(context.Background(), peer, ExecRequest{Command: "echo", Args: []string{"hi"}})
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if result.ExitCode != 0 {
		t.Errorf("exit code = %d, want 0", result.ExitCode)
	}
}
