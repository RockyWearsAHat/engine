// Package github — GitHub webhook receiver and event dispatcher.
// Validates HMAC-SHA256 signatures on incoming payloads, parses the event type,
// and dispatches to registered handlers.
package github

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
)

// WebhookEvent is a parsed GitHub webhook event.
type WebhookEvent struct {
	// Type is the X-GitHub-Event header value (push, issues, issue_comment, etc.).
	Type string
	// Delivery is the X-GitHub-Delivery header (unique per delivery attempt).
	Delivery string
	// Payload is the raw JSON body.
	Payload json.RawMessage
}

// WebhookHandler is a function that processes a single GitHub webhook event.
type WebhookHandler func(event *WebhookEvent)

// WebhookReceiver receives and validates GitHub webhook deliveries.
type WebhookReceiver struct {
	mu       sync.RWMutex
	secret   []byte
	handlers []WebhookHandler
}

// NewWebhookReceiver creates a WebhookReceiver that validates signatures using secret.
// Pass an empty secret to skip signature validation (development only).
func NewWebhookReceiver(secret string) *WebhookReceiver {
	return &WebhookReceiver{secret: []byte(secret)}
}

// SetSecret updates the webhook signature secret without recreating the receiver.
func (wr *WebhookReceiver) SetSecret(secret string) {
	wr.mu.Lock()
	wr.secret = []byte(strings.TrimSpace(secret))
	wr.mu.Unlock()
}

// AddHandler registers a handler to be called for every validated event.
func (wr *WebhookReceiver) AddHandler(h WebhookHandler) {
	wr.mu.Lock()
	defer wr.mu.Unlock()
	wr.handlers = append(wr.handlers, h)
}

// ServeHTTP implements http.Handler. Validates the HMAC-SHA256 signature,
// parses the event, and dispatches to all registered handlers.
func (wr *WebhookReceiver) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 10<<20)) // 10 MB limit
	if err != nil {
		http.Error(w, "read body: "+err.Error(), http.StatusBadRequest)
		return
	}

	sig := r.Header.Get("X-Hub-Signature-256")
	if !wr.validSignature(body, sig) {
		http.Error(w, "invalid signature", http.StatusUnauthorized)
		return
	}

	event := &WebhookEvent{
		Type:     r.Header.Get("X-GitHub-Event"),
		Delivery: r.Header.Get("X-GitHub-Delivery"),
		Payload:  json.RawMessage(body),
	}

	wr.mu.RLock()
	handlers := append([]WebhookHandler(nil), wr.handlers...)
	wr.mu.RUnlock()
	for _, h := range handlers {
		h(event)
	}

	w.WriteHeader(http.StatusNoContent)
}

// validSignature returns true if sig matches the HMAC-SHA256 of body under wr.secret.
// sig must be in the format "sha256=<hex>".
func (wr *WebhookReceiver) validSignature(body []byte, sig string) bool {
	wr.mu.RLock()
	secret := append([]byte(nil), wr.secret...)
	wr.mu.RUnlock()

	if len(secret) == 0 {
		return true
	}

	if !strings.HasPrefix(sig, "sha256=") {
		return false
	}
	expectedHex := strings.TrimPrefix(sig, "sha256=")
	expected, err := hex.DecodeString(expectedHex)
	if err != nil {
		return false
	}

	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	computed := mac.Sum(nil)
	return hmac.Equal(computed, expected)
}

// ── Payload types ──────────────────────────────────────────────────────────────

// PushPayload is the payload for "push" events.
type PushPayload struct {
	Ref        string `json:"ref"`        // e.g. "refs/heads/main"
	Repository struct {
		FullName      string `json:"full_name"`
		DefaultBranch string `json:"default_branch"`
	} `json:"repository"`
	Commits []struct {
		ID       string   `json:"id"`
		Message  string   `json:"message"`
		Added    []string `json:"added"`
		Modified []string `json:"modified"`
		Removed  []string `json:"removed"`
	} `json:"commits"`
}

// IssueCommentPayload is the payload for "issue_comment" events.
type IssueCommentPayload struct {
	Action  string `json:"action"`
	Comment struct {
		Body string `json:"body"`
		User struct {
			Login string `json:"login"`
		} `json:"user"`
	} `json:"comment"`
	Issue struct {
		Number int    `json:"number"`
		Title  string `json:"title"`
	} `json:"issue"`
	Repository struct {
		FullName string `json:"full_name"`
	} `json:"repository"`
}

// IssuePayload is the payload for "issues" events.
type IssuePayload struct {
	Action string `json:"action"`
	Issue  struct {
		Number int    `json:"number"`
		Title  string `json:"title"`
		Body   string `json:"body"`
	} `json:"issue"`
	Repository struct {
		FullName string `json:"full_name"`
	} `json:"repository"`
	Sender struct {
		Login string `json:"login"`
	} `json:"sender"`
}

// WorkflowRunPayload is the payload for "workflow_run" events.
type WorkflowRunPayload struct {
	Action      string `json:"action"`       // "requested", "completed", etc.
	Conclusion  string `json:"conclusion"`   // "success", "failure", "skipped", etc.
	WorkflowRun struct {
		Name    string `json:"name"`
		Status  string `json:"status"`
		HTMLURL string `json:"html_url"`
	} `json:"workflow_run"`
}

// ParsePush parses a PushPayload from a WebhookEvent with Type=="push".
func ParsePush(e *WebhookEvent) (*PushPayload, error) {
	var p PushPayload
	if err := json.Unmarshal(e.Payload, &p); err != nil {
		return nil, fmt.Errorf("parse push payload: %w", err)
	}
	return &p, nil
}

// ParseIssueComment parses an IssueCommentPayload.
func ParseIssueComment(e *WebhookEvent) (*IssueCommentPayload, error) {
	var p IssueCommentPayload
	if err := json.Unmarshal(e.Payload, &p); err != nil {
		return nil, fmt.Errorf("parse issue_comment payload: %w", err)
	}
	return &p, nil
}

// ParseIssue parses an IssuePayload from a WebhookEvent with Type=="issues".
func ParseIssue(e *WebhookEvent) (*IssuePayload, error) {
	var p IssuePayload
	if err := json.Unmarshal(e.Payload, &p); err != nil {
		return nil, fmt.Errorf("parse issues payload: %w", err)
	}
	return &p, nil
}

// ParseWorkflowRun parses a WorkflowRunPayload.
func ParseWorkflowRun(e *WebhookEvent) (*WorkflowRunPayload, error) {
	var p WorkflowRunPayload
	if err := json.Unmarshal(e.Payload, &p); err != nil {
		return nil, fmt.Errorf("parse workflow_run payload: %w", err)
	}
	return &p, nil
}

// TouchesReadme returns true if the push modifies README.md at the repo root.
func (p *PushPayload) TouchesReadme() bool {
	check := func(files []string) bool {
		for _, f := range files {
			lower := strings.ToLower(f)
			if lower == "readme.md" || strings.HasSuffix(lower, "/readme.md") {
				return true
			}
		}
		return false
	}
	for _, c := range p.Commits {
		if check(c.Added) || check(c.Modified) {
			return true
		}
	}
	return false
}
