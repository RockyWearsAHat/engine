package mesh

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestClientHealth_SuccessAndParseError tests that Health succeeds on valid responses and reports parse errors.
func TestClientHealth_SuccessAndParseError(t *testing.T) {
	cfg := &Config{SelfName: "host", Peers: []Peer{{Name: "mac", Secret: "k"}}}
	srv := NewServer(cfg)
	mux := http.NewServeMux()
	srv.Register(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	peer := &Peer{Name: "host", Address: strings.TrimPrefix(ts.URL, "http://"), Secret: "k"}
	c := NewClient("mac")

	h, err := c.Health(context.Background(), peer)
	if err != nil {
		t.Fatalf("Health: %v", err)
	}
	if h.Name != "host" {
		t.Fatalf("health name = %q, want host", h.Name)
	}

	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("not-json"))
	}))
	defer bad.Close()
	peerBad := &Peer{Name: "host", Address: strings.TrimPrefix(bad.URL, "http://"), Secret: "k"}

	if _, err := c.Health(context.Background(), peerBad); err == nil {
		t.Fatal("expected parse error")
	}
}

// TestClientInferenceAndExec_ParseErrors tests that Inference and Exec report JSON parsing errors.
func TestClientInferenceAndExec_ParseErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("not-json"))
	}))
	defer srv.Close()

	peer := &Peer{Name: "host", Address: strings.TrimPrefix(srv.URL, "http://"), Secret: "k"}
	c := NewClient("mac")

	if _, err := c.Inference(context.Background(), peer, InferenceRequest{Path: "/v1", Body: json.RawMessage(`{}`)}); err == nil {
		t.Fatal("expected inference parse error")
	}
	if _, err := c.Exec(context.Background(), peer, ExecRequest{Command: "echo"}); err == nil {
		t.Fatal("expected exec parse error")
	}
}

// TestClientSignedRequest_ValidationErrors tests that signedRequest validates peer and secret parameters.
func TestClientSignedRequest_ValidationErrors(t *testing.T) {
	c := NewClient("mac")	
	if _, err := c.signedRequest(context.Background(), nil, http.MethodPost, "/mesh/health", nil); err == nil {
		t.Fatal("expected nil peer error")
	}
	if _, err := c.signedRequest(context.Background(), &Peer{Name: "p", Address: "", Secret: "s"}, http.MethodPost, "/mesh/health", nil); err == nil {
		t.Fatal("expected empty address error")
	}
	if _, err := c.signedRequest(context.Background(), &Peer{Name: "p", Address: "127.0.0.1:1", Secret: ""}, http.MethodPost, "/mesh/health", nil); err == nil {
		t.Fatal("expected empty secret error")
	}
}

// TestPeerBaseURL_Paths tests that peerBaseURL correctly formats peer addresses.
func TestPeerBaseURL_Paths(t *testing.T) {
	if got := peerBaseURL(""); got != "" {
		t.Fatalf("peerBaseURL empty = %q, want empty", got)
	}
	if got := peerBaseURL("http://localhost:1"); got != "http://localhost:1" {
		t.Fatalf("unexpected http base: %q", got)
	}
	if got := peerBaseURL("localhost:1"); got != "http://localhost:1" {
		t.Fatalf("unexpected inferred base: %q", got)
	}
}

// TestNewServer_NilConfigCreatesDefault tests that NewServer creates a default config when nil is passed.
func TestNewServer_NilConfigCreatesDefault(t *testing.T) {
	s := NewServer(nil)
	if s.cfg == nil {
		t.Fatal("expected default config")
	}
}

// TestListenAndServe_ContextCancel tests that ListenAndServe respects context cancellation.
func TestListenAndServe_ContextCancel(t *testing.T) {
	s := NewServer(&Config{ListenAddr: "127.0.0.1:0"})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := s.ListenAndServe(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("ListenAndServe err = %v, want context.Canceled", err)
	}
}

// TestListenAndServe_ServerErrorPath tests that ListenAndServe reports errors from the listener.
func TestListenAndServe_ServerErrorPath(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	defer ln.Close()

	s := NewServer(&Config{ListenAddr: addr})
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	err = s.ListenAndServe(ctx)
	if err == nil {
		t.Fatal("expected listen error due to occupied port")
	}
}
