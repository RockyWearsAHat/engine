package mesh

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestInferenceProxy_RoundTrips verifies that a signed /mesh/inference call
// on one server is forwarded to the configured SelfOllamaURL and returned to
// the caller. Two test servers are involved: a fake Ollama and a real mesh
// server that points at the fake Ollama as its upstream.
func TestInferenceProxy_RoundTrips(t *testing.T) {
	var upstreamCalled bool
	fakeOllama := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalled = true
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), "hello") {
			t.Errorf("upstream did not receive original body: %s", body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"reply":"world"}`))
	}))
	defer fakeOllama.Close()

	cfg := &Config{
		SelfName:      "host",
		SelfOllamaURL: fakeOllama.URL,
		Peers:         []Peer{{Name: "mac", Secret: "k"}},
	}
	meshServer := newMeshServer(t, cfg)

	peer := &Peer{Name: "host", Address: strings.TrimPrefix(meshServer.URL, "http://"), Secret: "k"}
	client := NewClient("mac")

	resp, err := client.Inference(context.Background(), peer, InferenceRequest{
		Path: "/v1/chat/completions",
		Body: json.RawMessage(`{"prompt":"hello"}`),
	})
	if err != nil {
		t.Fatalf("inference: %v", err)
	}
	if !upstreamCalled {
		t.Fatal("upstream Ollama was never called")
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("statusCode = %d", resp.StatusCode)
	}
	if !strings.Contains(string(resp.Body), "world") {
		t.Errorf("body = %s; want world", resp.Body)
	}
}

// TestInferenceProxy_NoSelfOllamaURL tests that inference requests fail without an upstream Ollama URL configured.
func TestInferenceProxy_NoSelfOllamaURL(t *testing.T) {
	cfg := &Config{
		SelfName: "host",
		Peers:    []Peer{{Name: "mac", Secret: "k"}},
	}
	meshServer := newMeshServer(t, cfg)

	peer := &Peer{Name: "host", Address: strings.TrimPrefix(meshServer.URL, "http://"), Secret: "k"}
	client := NewClient("mac")

	resp, err := client.Inference(context.Background(), peer, InferenceRequest{
		Path: "/v1/chat/completions",
		Body: json.RawMessage(`{}`),
	})
	if err == nil {
		t.Fatalf("expected error from peer without upstream, got %+v", resp)
	}
}

// TestInferenceProxy_RequiresAuth tests that /mesh/inference requires valid authentication.
func TestInferenceProxy_RequiresAuth(t *testing.T) {
	cfg := &Config{SelfName: "host", Peers: []Peer{{Name: "mac", Secret: "k"}}}
	meshServer := newMeshServer(t, cfg)

	resp, err := http.Post(meshServer.URL+"/mesh/inference", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", resp.StatusCode)
	}
}

// TestInferenceProxy_RejectsBadPath tests that inference requests with empty paths are rejected.
func TestInferenceProxy_RejectsBadPath(t *testing.T) {
	cfg := &Config{SelfName: "host", SelfOllamaURL: "http://nowhere", Peers: []Peer{{Name: "mac", Secret: "k"}}}
	meshServer := newMeshServer(t, cfg)

	peer := &Peer{Name: "host", Address: strings.TrimPrefix(meshServer.URL, "http://"), Secret: "k"}
	client := NewClient("mac")

	_, err := client.Inference(context.Background(), peer, InferenceRequest{Path: "", Body: json.RawMessage(`{}`)})
	if err == nil {
		t.Fatal("expected error on empty path")
	}
}
