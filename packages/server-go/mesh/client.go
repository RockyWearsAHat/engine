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

// Client dispatches mesh-signed requests to peers. SelfName is sent in the
// Origin header so the receiving peer knows which configured secret to verify
// against.
type Client struct {
	HTTP     *http.Client
	SelfName string
}

// NewClient builds a client with a sensible HTTP timeout.
func NewClient(selfName string) *Client {
	return &Client{
		HTTP:     &http.Client{Timeout: 5 * time.Minute},
		SelfName: selfName,
	}
}

// Health calls the peer's /mesh/health endpoint.
func (c *Client) Health(ctx context.Context, peer *Peer) (*HealthResponse, error) {
	body, err := c.signedRequest(ctx, peer, http.MethodPost, "/mesh/health", nil)
	if err != nil {
		return nil, err
	}
	var resp HealthResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("mesh client: parse health: %w", err)
	}
	return &resp, nil
}

// Inference proxies an Ollama-compatible request to the peer's local model.
// Returns the peer's wrapped response (status + raw body) so the caller can
// treat it as if it were a direct upstream Ollama call.
func (c *Client) Inference(ctx context.Context, peer *Peer, req InferenceRequest) (*InferenceResponse, error) {
	payload, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("mesh client: marshal inference: %w", err)
	}
	body, err := c.signedRequest(ctx, peer, http.MethodPost, "/mesh/inference", payload)
	if err != nil {
		return nil, err
	}
	var resp InferenceResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("mesh client: parse inference response: %w", err)
	}
	return &resp, nil
}

// Exec runs a shell command on the peer and returns the captured output.
func (c *Client) Exec(ctx context.Context, peer *Peer, req ExecRequest) (*ExecResponse, error) {
	payload, _ := json.Marshal(req)
	body, err := c.signedRequest(ctx, peer, http.MethodPost, "/mesh/exec", payload)
	if err != nil {
		return nil, err
	}
	var resp ExecResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("mesh client: parse exec response: %w", err)
	}
	return &resp, nil
}

// signedRequest is the common path: attach HMAC signature + timestamp + origin,
// POST to the peer, return the response body.
func (c *Client) signedRequest(ctx context.Context, peer *Peer, method, path string, payload []byte) ([]byte, error) {
	if peer == nil || strings.TrimSpace(peer.Address) == "" {
		return nil, fmt.Errorf("mesh client: peer address is required")
	}
	if strings.TrimSpace(peer.Secret) == "" {
		return nil, fmt.Errorf("mesh client: peer secret is required")
	}
	target := strings.TrimRight(peerBaseURL(peer.Address), "/") + path

	if payload == nil {
		payload = []byte{}
	}
	timestamp, signature := signRequest(peer.Secret, c.SelfName, payload)

	req, err := http.NewRequestWithContext(ctx, method, target, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(signatureHeader, signature)
	req.Header.Set(timestampHeader, timestamp)
	req.Header.Set(originHeader, c.SelfName)

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("mesh client: %s %s: %w", method, target, err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("mesh client: %s %s: status %d: %s", method, target, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return body, nil
}

// peerBaseURL normalizes a peer address into an HTTP URL, adding http:// if not present.
func peerBaseURL(address string) string {
	addr := strings.TrimSpace(address)
	if addr == "" {
		return ""
	}
	if strings.HasPrefix(addr, "http://") || strings.HasPrefix(addr, "https://") {
		return addr
	}
	return "http://" + addr
}
