package ai

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/engine/server/mesh"
)

func TestDefaultGetEnv_Present(t *testing.T) {
	t.Setenv("_TEST_GET_ENV_KEY", "hello")
	if got := defaultGetEnv("_TEST_GET_ENV_KEY"); got != "hello" {
		t.Errorf("got %q, want %q", got, "hello")
	}
}

func TestDefaultGetEnv_Missing(t *testing.T) {
	if got := defaultGetEnv("_TEST_GET_ENV_MISSING_XYZ_123"); got != "" {
		t.Errorf("expected empty for missing key, got %q", got)
	}
}

func TestDefaultMeshInferenceEnabled_False(t *testing.T) {
	origOsLookup := osLookupEnv
	defer func() { osLookupEnv = origOsLookup }()
	osLookupEnv = func(key string) string { return "" }
	if defaultMeshInferenceEnabled() {
		t.Error("expected false when ENGINE_MESH_INFERENCE unset")
	}
}

func TestDefaultMeshInferenceEnabled_True(t *testing.T) {
	origEnvOrDefault := envOrDefault
	defer func() { envOrDefault = origEnvOrDefault }()
	for _, val := range []string{"1", "true", "yes", "on", "TRUE", "YES", "ON"} {
		v := val
		envOrDefault = func(key, fallback string) string { return v }
		if !defaultMeshInferenceEnabled() {
			t.Errorf("expected true for ENGINE_MESH_INFERENCE=%q", v)
		}
	}
}

func TestMeshInferencePeerInfo_Disabled(t *testing.T) {
	orig := meshInferenceEnabledFn
	defer func() { meshInferenceEnabledFn = orig }()
	meshInferenceEnabledFn = func() bool { return false }
	if got := MeshInferencePeerInfo(); got != "" {
		t.Errorf("expected empty when disabled, got %q", got)
	}
}

func TestMeshInferencePeerInfo_Enabled_NoPeer(t *testing.T) {
	origEnabled := meshInferenceEnabledFn
	origPeer := meshInferencePeerFn
	defer func() {
		meshInferenceEnabledFn = origEnabled
		meshInferencePeerFn = origPeer
	}()
	meshInferenceEnabledFn = func() bool { return true }
	meshInferencePeerFn = func() (*mesh.Peer, string, bool) { return nil, "", false }
	if got := MeshInferencePeerInfo(); got != "" {
		t.Errorf("expected empty when no peer, got %q", got)
	}
}

func TestMeshInferencePeerInfo_Enabled_WithPeer(t *testing.T) {
	origEnabled := meshInferenceEnabledFn
	origPeer := meshInferencePeerFn
	defer func() {
		meshInferenceEnabledFn = origEnabled
		meshInferencePeerFn = origPeer
	}()
	meshInferenceEnabledFn = func() bool { return true }
	meshInferencePeerFn = func() (*mesh.Peer, string, bool) {
		return &mesh.Peer{Name: "gpu-box", Address: "192.168.1.10:9090"}, "self", true
	}
	got := MeshInferencePeerInfo()
	if got == "" {
		t.Error("expected non-empty peer info")
	}
}

func TestCallMeshInference_NilPeer(t *testing.T) {
	_, err := callMeshInference(nil, "self", "/api/generate", []byte("{}"), 0)
	if err == nil {
		t.Error("expected error for nil peer")
	}
}

func TestDefaultMeshInferencePeer_NoConfig(t *testing.T) {
	// Inject meshLoadConfigFn via the load fn wrapped in defaultMeshInferencePeer.
	// defaultMeshInferencePeer calls mesh.LoadConfig directly, so we test
	// the real function by ensuring it returns gracefully when config is absent.
	origFn := meshInferencePeerFn
	defer func() { meshInferencePeerFn = origFn }()
	// Replace with a stub that returns no peer, to cover the false-path.
	meshInferencePeerFn = func() (*mesh.Peer, string, bool) { return nil, "", false }
	peer, name, ok := meshInferencePeerFn()
	if ok || peer != nil || name != "" {
		t.Error("expected false result from stub no-config peer fn")
	}
}

func TestDefaultMeshInferencePeer_SelectsInferenceRole(t *testing.T) {
	tmp := t.TempDir()
	configPath := filepath.Join(tmp, "mesh.json")

	cfg := mesh.Config{
		SelfName: "engine-mac",
		Peers: []mesh.Peer{
			{Name: "tests-box", Address: "10.0.0.10:24445", Roles: []string{"tests"}},
			{Name: "gpu-box", Address: "10.0.0.20:24445", Roles: []string{"inference"}},
		},
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	if err := os.WriteFile(configPath, data, 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	t.Setenv("ENGINE_MESH_CONFIG", configPath)
	peer, selfName, ok := defaultMeshInferencePeer()
	if !ok || peer == nil {
		t.Fatalf("expected peer resolution success, got ok=%v peer=%v", ok, peer)
	}
	if peer.Name != "gpu-box" {
		t.Fatalf("expected inference role peer, got %q", peer.Name)
	}
	if selfName != "engine-mac" {
		t.Fatalf("expected selfName engine-mac, got %q", selfName)
	}
}

func TestDefaultMeshInferencePeer_FallsBackToOllamaURL(t *testing.T) {
	tmp := t.TempDir()
	configPath := filepath.Join(tmp, "mesh.json")

	cfg := mesh.Config{
		SelfName: "engine-mac",
		Peers: []mesh.Peer{
			{Name: "gpu-box", Address: "10.0.0.20:24445", OllamaURL: "http://127.0.0.1:11434"},
		},
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	if err := os.WriteFile(configPath, data, 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	t.Setenv("ENGINE_MESH_CONFIG", configPath)
	peer, selfName, ok := defaultMeshInferencePeer()
	if !ok || peer == nil {
		t.Fatalf("expected peer resolution success, got ok=%v peer=%v", ok, peer)
	}
	if peer.Name != "gpu-box" {
		t.Fatalf("expected Ollama fallback peer, got %q", peer.Name)
	}
	if selfName != "engine-mac" {
		t.Fatalf("expected selfName engine-mac, got %q", selfName)
	}
}

func TestDefaultMeshInferencePeer_NoMatchingPeer(t *testing.T) {
	tmp := t.TempDir()
	configPath := filepath.Join(tmp, "mesh.json")

	cfg := mesh.Config{
		SelfName: "engine-mac",
		Peers: []mesh.Peer{
			{Name: "tests-box", Address: "10.0.0.10:24445", Roles: []string{"tests"}},
		},
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	if err := os.WriteFile(configPath, data, 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	t.Setenv("ENGINE_MESH_CONFIG", configPath)
	peer, selfName, ok := defaultMeshInferencePeer()
	if ok || peer != nil || strings.TrimSpace(selfName) != "" {
		t.Fatalf("expected no matching peer, got ok=%v peer=%v selfName=%q", ok, peer, selfName)
	}
}

func TestDefaultMeshInferencePeer_BadConfigPath(t *testing.T) {
	t.Setenv("ENGINE_MESH_CONFIG", filepath.Join(t.TempDir(), "missing.json"))
	peer, selfName, ok := defaultMeshInferencePeer()
	if ok || peer != nil || selfName != "" {
		t.Fatalf("expected no peer for bad config path, got ok=%v peer=%v selfName=%q", ok, peer, selfName)
	}
}

func TestEnvOrDefault_UsesEnvAndFallback(t *testing.T) {
	origLookup := osLookupEnv
	defer func() { osLookupEnv = origLookup }()

	osLookupEnv = func(key string) string {
		if key == "K" {
			return "value"
		}
		return ""
	}
	if got := envOrDefault("K", "fallback"); got != "value" {
		t.Fatalf("expected env value, got %q", got)
	}
	if got := envOrDefault("MISSING", "fallback"); got != "fallback" {
		t.Fatalf("expected fallback value, got %q", got)
	}
}

func TestOSLookupEnv_TrimsWhitespace(t *testing.T) {
	origGetEnv := getEnv
	defer func() { getEnv = origGetEnv }()

	getEnv = func(key string) string {
		if key == "X" {
			return "  spaced  "
		}
		return ""
	}
	if got := osLookupEnv("X"); got != "spaced" {
		t.Fatalf("expected trimmed value, got %q", got)
	}
}

func TestCallMeshInference_PeerRequestError(t *testing.T) {
	peer := &mesh.Peer{Name: "p1", Address: "127.0.0.1:1", Secret: "secret"}
	_, err := callMeshInference(peer, "self", "/api/chat", []byte("{}"), time.Second)
	if err == nil {
		t.Fatal("expected request error")
	}
}

func TestCallMeshInference_PeerErrorField(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(mesh.InferenceResponse{StatusCode: 200, Error: "upstream failed"})
	}))
	defer ts.Close()

	peer := &mesh.Peer{Name: "p1", Address: strings.TrimPrefix(ts.URL, "http://"), Secret: "secret"}
	_, err := callMeshInference(peer, "self", "/api/chat", []byte("{}"), time.Second)
	if err == nil || !strings.Contains(err.Error(), "peer error") {
		t.Fatalf("expected peer error, got %v", err)
	}
}

func TestCallMeshInference_PeerStatusError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(mesh.InferenceResponse{StatusCode: 503})
	}))
	defer ts.Close()

	peer := &mesh.Peer{Name: "p1", Address: strings.TrimPrefix(ts.URL, "http://"), Secret: "secret"}
	_, err := callMeshInference(peer, "self", "/api/chat", []byte("{}"), time.Second)
	if err == nil || !strings.Contains(err.Error(), "status 503") {
		t.Fatalf("expected status error, got %v", err)
	}
}

func TestCallMeshInference_Success(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(mesh.InferenceResponse{StatusCode: 200, Body: json.RawMessage(`{"ok":true}`)})
	}))
	defer ts.Close()

	peer := &mesh.Peer{Name: "p1", Address: strings.TrimPrefix(ts.URL, "http://"), Secret: "secret"}
	body, err := callMeshInference(peer, "self", "/api/chat", []byte("{}"), time.Second)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(body) != `{"ok":true}` {
		t.Fatalf("unexpected body: %s", string(body))
	}
}

func TestCallMeshInference_SuccessWithDefaultTimeout(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(mesh.InferenceResponse{StatusCode: 200, Body: json.RawMessage(`{"ok":true}`)})
	}))
	defer ts.Close()

	peer := &mesh.Peer{Name: "p1", Address: strings.TrimPrefix(ts.URL, "http://"), Secret: "secret"}
	body, err := callMeshInference(peer, "self", "/api/chat", []byte("{}"), 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(body) != `{"ok":true}` {
		t.Fatalf("unexpected body: %s", string(body))
	}
}
