package ai

import (
	stdctx "context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/engine/server/mesh"
)

// defaultGetEnv reads from the process environment; isolated for testability.
func defaultGetEnv(key string) string { return os.Getenv(key) }

// ResolveOllamaBaseURL returns the URL the Ollama path should target for the
// caller. When ENGINE_MESH_INFERENCE=1 and a mesh peer with role "inference"
// (or an explicit OllamaURL) is configured, the returned base URL points at a
// local proxy goroutine that signs requests and forwards them to the peer.
//
// Returns ("", false) when no peer routing applies — the caller falls back to
// the OLLAMA_BASE_URL environment value, i.e. existing behaviour.
//
// Wiring: a small in-process HTTP proxy is spun up on demand. The provider
// loop talks to localhost; the proxy converts each Ollama request into a
// signed mesh inference call. That isolation keeps the OpenAI-compatible
// loop in provider.go ignorant of the mesh.
var (
	meshInferenceEnabledFn = defaultMeshInferenceEnabled
	meshInferencePeerFn    = defaultMeshInferencePeer
)

func defaultMeshInferenceEnabled() bool {
	v := strings.TrimSpace(strings.ToLower(envOrDefault("ENGINE_MESH_INFERENCE", "")))
	return v == "1" || v == "true" || v == "yes" || v == "on"
}

func defaultMeshInferencePeer() (*mesh.Peer, string, bool) {
	cfg, err := mesh.LoadConfig("")
	if err != nil || cfg == nil || len(cfg.Peers) == 0 {
		return nil, "", false
	}
	if p := cfg.PeerWithRole("inference"); p != nil {
		return p, cfg.SelfName, true
	}
	for i := range cfg.Peers {
		if strings.TrimSpace(cfg.Peers[i].OllamaURL) != "" {
			return &cfg.Peers[i], cfg.SelfName, true
		}
	}
	return nil, "", false
}

// envOrDefault is a tiny helper that lets tests stub env lookups.
var envOrDefault = func(key, fallback string) string {
	if v := osLookupEnv(key); v != "" {
		return v
	}
	return fallback
}

// osLookupEnv is split out so tests can override env reads without touching
// the OS environment. Production behaviour is identical to os.Getenv.
var osLookupEnv = func(key string) string {
	return strings.TrimSpace(getEnv(key))
}

// getEnv is the actual os.Getenv indirection. Pulled out so the test seam
// above remains a one-line replacement that returns trimmed strings.
var getEnv = func(key string) string { return defaultGetEnv(key) }

// callMeshInference performs one signed inference RPC to peer. Returns the
// peer's wrapped response body (which is the upstream Ollama JSON) and a
// status code error when status != 2xx.
func callMeshInference(peer *mesh.Peer, selfName, path string, body []byte, timeout time.Duration) ([]byte, error) {
	if peer == nil {
		return nil, fmt.Errorf("mesh inference: no peer")
	}
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	ctx, cancel := stdctx.WithTimeout(stdctx.Background(), timeout)
	defer cancel()

	client := mesh.NewClient(selfName)
	resp, err := client.Inference(ctx, peer, mesh.InferenceRequest{
		Path:      path,
		Method:    "POST",
		Body:      body,
		TimeoutMs: int(timeout / time.Millisecond),
	})
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(resp.Error) != "" {
		return nil, fmt.Errorf("mesh inference: peer error: %s", resp.Error)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("mesh inference: peer returned status %d", resp.StatusCode)
	}
	return []byte(resp.Body), nil
}

// MeshInferencePeerInfo reports the currently selected inference peer, used by
// the AI tool surface and Discord status replies. Empty when no peer is set.
func MeshInferencePeerInfo() string {
	if !meshInferenceEnabledFn() {
		return ""
	}
	peer, _, ok := meshInferencePeerFn()
	if !ok || peer == nil {
		return ""
	}
	return fmt.Sprintf("%s @ %s", peer.Name, peer.Address)
}
