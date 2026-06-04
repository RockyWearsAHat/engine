package mesh

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// InferenceRequest tells a peer "please run this Ollama-compatible request
// against your local model and return the response." The peer-side handler
// proxies the body verbatim to its configured Ollama and streams the response
// back. Keeps Engine's existing OpenAI-compatible loop unchanged on the caller
// side: from the caller's perspective it just looks like a slower Ollama.
type InferenceRequest struct {
	// Path is the upstream Ollama path, e.g. "/v1/chat/completions" or
	// "/api/chat". Leading slash optional.
	Path string `json:"path"`
	// Method, defaults to POST when empty.
	Method string `json:"method,omitempty"`
	// Body is the raw JSON to forward upstream.
	Body json.RawMessage `json:"body"`
	// TimeoutMs caps the proxied call. 0 → 5 minutes.
	TimeoutMs int `json:"timeoutMs,omitempty"`
}

// InferenceResponse mirrors the upstream Ollama response with the body kept
// as raw JSON so streaming chat-completion shapes survive without translation.
type InferenceResponse struct {
	StatusCode int             `json:"statusCode"`
	Body       json.RawMessage `json:"body,omitempty"`
	Error      string          `json:"error,omitempty"`
}

// handleInferenceProxy authenticates the caller, then forwards the request
// body to the local Ollama at cfg.SelfOllamaURL. The reply is wrapped in
// InferenceResponse so the wire shape is stable even when upstream returns
// non-JSON error text.
//
// Security model: same as /mesh/exec — peers are explicitly paired. The HMAC
// proves the request came from a configured peer; the peer is trusted to
// behave. If your network has untrusted machines, don't run mesh.
func (s *Server) handleInferenceProxy(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "read body", http.StatusBadRequest)
		return
	}
	if err := s.authenticate(r, body); err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}

	var req InferenceRequest
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, "bad json: "+err.Error(), http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.Path) == "" {
		http.Error(w, "path required", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(s.cfg.SelfOllamaURL) == "" {
		writeJSON(w, http.StatusServiceUnavailable, InferenceResponse{Error: "self has no local Ollama configured"})
		return
	}

	method := strings.ToUpper(strings.TrimSpace(req.Method))
	if method == "" {
		method = http.MethodPost
	}
	timeout := time.Duration(req.TimeoutMs) * time.Millisecond
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()

	upstream, err := buildUpstreamRequest(ctx, method, s.cfg.SelfOllamaURL, req.Path, req.Body)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, InferenceResponse{Error: err.Error()})
		return
	}
	resp, err := http.DefaultClient.Do(upstream)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, InferenceResponse{Error: err.Error()})
		return
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	writeJSON(w, http.StatusOK, InferenceResponse{
		StatusCode: resp.StatusCode,
		Body:       respBody,
	})
}

func buildUpstreamRequest(ctx context.Context, method, base, path string, body []byte) (*http.Request, error) {
	target := strings.TrimRight(base, "/") + "/" + strings.TrimLeft(path, "/")
	req, err := http.NewRequestWithContext(ctx, method, target, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("mesh inference: build upstream request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	return req, nil
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		fmt.Printf("mesh inference encode response failed: %v\n", err)
	}
}
