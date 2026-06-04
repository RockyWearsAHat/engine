package mesh

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// failingReadCloser is a test helper that simulates I/O errors.
type failingReadCloser struct{}

func (failingReadCloser) Read(p []byte) (int, error) { return 0, errors.New("read failed") }
func (failingReadCloser) Close() error               { return nil }

// TestClientInference_MarshalError_InvalidRawJSON tests that Inference rejects invalid JSON payloads.
func TestClientInference_MarshalError_InvalidRawJSON(t *testing.T) {
	c := NewClient("self")
	peer := &Peer{Name: "p1", Address: "localhost:9999", Secret: "s"}
	_, err := c.Inference(context.Background(), peer, InferenceRequest{Path: "/api/chat", Body: json.RawMessage("{" )})
	if err == nil {
		t.Fatal("expected marshal error for invalid raw JSON body")
	}
}

// TestClientExec_NilPeer_Error tests that Exec returns an error for nil peers.
func TestClientExec_NilPeer_Error(t *testing.T) {
	c := NewClient("self")
	_, err := c.Exec(context.Background(), nil, ExecRequest{Command: "echo"})
	if err == nil {
		t.Fatal("expected error for nil peer")
	}
}

// TestSignedRequest_NewRequestError tests that signedRequest reports malformed URL errors.
func TestSignedRequest_NewRequestError(t *testing.T) {
	c := NewClient("self")
	peer := &Peer{Name: "p1", Address: "http://[::1", Secret: "s"}
	_, err := c.signedRequest(context.Background(), peer, http.MethodPost, "/mesh/exec", []byte(`{}`))
	if err == nil {
		t.Fatal("expected request construction error")
	}
}

// TestSaveConfig_MkdirError tests that SaveConfig reports directory creation failures.
func TestSaveConfig_MkdirError(t *testing.T) {
	base := t.TempDir()
	fileParent := filepath.Join(base, "as-file")
	if err := os.WriteFile(fileParent, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(fileParent, "mesh.json")
	err := SaveConfig(path, &Config{SelfName: "x"})
	if err == nil {
		t.Fatal("expected mkdir error")
	}
}

// TestHandleInferenceProxy_ReadBodyError tests that /mesh/inference handles request body read errors gracefully.
func TestHandleInferenceProxy_ReadBodyError(t *testing.T) {
	s := NewServer(&Config{SelfName: "host", Peers: []Peer{{Name: "peer", Secret: "k"}}})
	req := httptest.NewRequest(http.MethodPost, "/mesh/inference", nil)
	req.Body = failingReadCloser{}
	rr := httptest.NewRecorder()
	s.handleInferenceProxy(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}

// TestHandleInferenceProxy_BuildUpstreamRequestError tests that /mesh/inference reports errors building upstream requests.
func TestHandleInferenceProxy_BuildUpstreamRequestError(t *testing.T) {
	cfg := &Config{SelfName: "host", SelfOllamaURL: "://bad", Peers: []Peer{{Name: "peer", Secret: "k"}}}
	ts := newMeshServer(t, cfg)
	body, _ := json.Marshal(InferenceRequest{Path: "/api/chat", Body: json.RawMessage(`{"model":"x"}`)})
	req := newSignedRequest(t, ts, "/mesh/inference", http.MethodPost, "k", "peer", body)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d", resp.StatusCode)
	}
}

// TestBuildUpstreamRequest_InvalidMethod tests that buildUpstreamRequest rejects invalid HTTP methods.
func TestBuildUpstreamRequest_InvalidMethod(t *testing.T) {
	_, err := buildUpstreamRequest(context.Background(), "BAD METHOD", "http://localhost:11434", "/api/chat", nil)
	if err == nil {
		t.Fatal("expected build upstream request error")
	}
}

// TestHandleHealth_ReadBodyError tests that /mesh/health handles request body read errors gracefully.
func TestHandleHealth_ReadBodyError(t *testing.T) {
	s := NewServer(&Config{SelfName: "host", Peers: []Peer{{Name: "peer", Secret: "k"}}})
	req := httptest.NewRequest(http.MethodGet, "/mesh/health", nil)
	req.Body = failingReadCloser{}
	rr := httptest.NewRecorder()
	s.handleHealth(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}

// TestHandleExec_ReadBodyError tests that /mesh/exec handles request body read errors gracefully.
func TestHandleExec_ReadBodyError(t *testing.T) {
	s := NewServer(&Config{SelfName: "host", Peers: []Peer{{Name: "peer", Secret: "k"}}})
	req := httptest.NewRequest(http.MethodPost, "/mesh/exec", nil)
	req.Body = failingReadCloser{}
	rr := httptest.NewRecorder()
	s.handleExec(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}

// TestAuthenticate_VerifyError tests that authenticate rejects requests with invalid signatures.
func TestAuthenticate_VerifyError(t *testing.T) {
	s := NewServer(&Config{SelfName: "host", Peers: []Peer{{Name: "peer", Secret: "k"}}})
	req := httptest.NewRequest(http.MethodPost, "/mesh/exec", strings.NewReader(`{}`))
	req.Header.Set(originHeader, "peer")
	req.Header.Set(timestampHeader, "bad-ts")
	req.Header.Set(signatureHeader, "bad-sig")
	if err := s.authenticate(req, []byte(`{}`)); err == nil {
		t.Fatal("expected verifyRequest error")
	}
}

// TestListenAndServe_EmptyListenAddr_DefaultsThenCancels tests that ListenAndServe uses default address and respects context.
func TestListenAndServe_EmptyListenAddr_DefaultsThenCancels(t *testing.T) {
	s := NewServer(&Config{SelfName: "host", ListenAddr: ""})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := s.ListenAndServe(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context canceled, got %v", err)
	}
}

// TestDefaultConfigPath_FallbackWhenHomeUnavailable tests that DefaultConfigPath handles home directory lookup failures.
func TestDefaultConfigPath_FallbackWhenHomeUnavailable(t *testing.T) {
	old := userHomeDirFn
	defer func() { userHomeDirFn = old }()
	t.Setenv("ENGINE_MESH_CONFIG", "")

	t.Run("home lookup error", func(t *testing.T) {
		userHomeDirFn = func() (string, error) { return "", errors.New("no home") }
		if got := DefaultConfigPath(); got != ".engine/mesh.json" {
			t.Fatalf("got %q, want .engine/mesh.json", got)
		}
	})

	t.Run("empty home", func(t *testing.T) {
		userHomeDirFn = func() (string, error) { return "   ", nil }
		if got := DefaultConfigPath(); got != ".engine/mesh.json" {
			t.Fatalf("got %q, want .engine/mesh.json", got)
		}
	})
}
