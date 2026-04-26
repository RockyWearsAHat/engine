package github

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// ── WebhookReceiver ───────────────────────────────────────────────────────────

func TestWebhookReceiver_WrongMethod(t *testing.T) {
	wr := NewWebhookReceiver("")
	req := httptest.NewRequest("GET", "/webhook", nil)
	rr := httptest.NewRecorder()
	wr.ServeHTTP(rr, req)
	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", rr.Code)
	}
}

func TestWebhookReceiver_NoSecret_Dispatches(t *testing.T) {
	wr := NewWebhookReceiver("")
	dispatched := false
	wr.AddHandler(func(_ *WebhookEvent) { dispatched = true })

	body := []byte(`{"action":"opened"}`)
	req := httptest.NewRequest("POST", "/webhook", bytes.NewReader(body))
	req.Header.Set("X-GitHub-Event", "issues")
	req.Header.Set("X-GitHub-Delivery", "abc-123")
	rr := httptest.NewRecorder()
	wr.ServeHTTP(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Errorf("status = %d, want 204", rr.Code)
	}
	if !dispatched {
		t.Error("expected handler to be called")
	}
}

func TestWebhookReceiver_ValidSignature(t *testing.T) {
	secret := "my-secret"
	wr := NewWebhookReceiver(secret)
	dispatched := false
	wr.AddHandler(func(_ *WebhookEvent) { dispatched = true })

	body := []byte(`{"action":"opened"}`)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	sig := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	req := httptest.NewRequest("POST", "/webhook", bytes.NewReader(body))
	req.Header.Set("X-Hub-Signature-256", sig)
	req.Header.Set("X-GitHub-Event", "push")
	rr := httptest.NewRecorder()
	wr.ServeHTTP(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Errorf("status = %d, want 204", rr.Code)
	}
	if !dispatched {
		t.Error("expected handler called on valid signature")
	}
}

func TestWebhookReceiver_InvalidSignature(t *testing.T) {
	wr := NewWebhookReceiver("secret")
	wr.AddHandler(func(_ *WebhookEvent) { t.Error("handler should not be called") })

	body := []byte(`{"action":"opened"}`)
	req := httptest.NewRequest("POST", "/webhook", bytes.NewReader(body))
	req.Header.Set("X-Hub-Signature-256", "sha256=deadbeef")
	rr := httptest.NewRecorder()
	wr.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rr.Code)
	}
}

func TestWebhookReceiver_MissingSigPrefix(t *testing.T) {
	wr := NewWebhookReceiver("secret")
	body := []byte(`{}`)
	req := httptest.NewRequest("POST", "/webhook", bytes.NewReader(body))
	req.Header.Set("X-Hub-Signature-256", "notsha256=abc")
	rr := httptest.NewRecorder()
	wr.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rr.Code)
	}
}

func TestWebhookReceiver_BadHexSig(t *testing.T) {
	wr := NewWebhookReceiver("secret")
	body := []byte(`{}`)
	req := httptest.NewRequest("POST", "/webhook", bytes.NewReader(body))
	req.Header.Set("X-Hub-Signature-256", "sha256=ZZZZZZ")
	rr := httptest.NewRecorder()
	wr.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rr.Code)
	}
}

// ── ParsePush / TouchesReadme ─────────────────────────────────────────────────

func TestParsePush_TouchesReadme(t *testing.T) {
	payload := `{"ref":"refs/heads/main","commits":[{"id":"abc","message":"docs","added":["README.md"],"modified":[],"removed":[]}]}`
	ev := &WebhookEvent{Type: "push", Payload: json.RawMessage(payload)}
	p, err := ParsePush(ev)
	if err != nil {
		t.Fatalf("ParsePush: %v", err)
	}
	if !p.TouchesReadme() {
		t.Error("expected TouchesReadme true")
	}
}

func TestParsePush_ModifiedReadme(t *testing.T) {
	payload := `{"ref":"refs/heads/main","commits":[{"id":"abc","message":"docs","added":[],"modified":["docs/README.md"],"removed":[]}]}`
	ev := &WebhookEvent{Type: "push", Payload: json.RawMessage(payload)}
	p, _ := ParsePush(ev)
	if !p.TouchesReadme() {
		t.Error("expected TouchesReadme true for modified")
	}
}

func TestParsePush_NoReadme(t *testing.T) {
	payload := `{"ref":"refs/heads/main","commits":[{"id":"abc","message":"feat","added":["src/main.go"],"modified":[],"removed":[]}]}`
	ev := &WebhookEvent{Type: "push", Payload: json.RawMessage(payload)}
	p, _ := ParsePush(ev)
	if p.TouchesReadme() {
		t.Error("expected TouchesReadme false")
	}
}

func TestParsePush_BadJSON(t *testing.T) {
	ev := &WebhookEvent{Type: "push", Payload: json.RawMessage(`{bad`)}
	_, err := ParsePush(ev)
	if err == nil {
		t.Error("expected error on bad JSON")
	}
}

func TestParseIssueComment(t *testing.T) {
	payload := `{"action":"created","comment":{"body":"lgtm","user":{"login":"alice"}},"issue":{"number":1,"title":"Bug"}}`
	ev := &WebhookEvent{Type: "issue_comment", Payload: json.RawMessage(payload)}
	p, err := ParseIssueComment(ev)
	if err != nil {
		t.Fatalf("ParseIssueComment: %v", err)
	}
	if p.Comment.Body != "lgtm" {
		t.Errorf("Body = %q, want lgtm", p.Comment.Body)
	}
}

func TestParseIssueComment_BadJSON(t *testing.T) {
	ev := &WebhookEvent{Type: "issue_comment", Payload: json.RawMessage(`bad`)}
	_, err := ParseIssueComment(ev)
	if err == nil {
		t.Error("expected error")
	}
}

func TestParseIssue(t *testing.T) {
	payload := `{"action":"opened","issue":{"number":42,"title":"Feature","body":"Add X"},"repository":{"full_name":"org/repo"}}`
	ev := &WebhookEvent{Type: "issues", Payload: json.RawMessage(payload)}
	p, err := ParseIssue(ev)
	if err != nil {
		t.Fatalf("ParseIssue: %v", err)
	}
	if p.Action != "opened" {
		t.Errorf("Action = %q, want opened", p.Action)
	}
	if p.Issue.Number != 42 {
		t.Errorf("Issue.Number = %d, want 42", p.Issue.Number)
	}
}

func TestParseIssue_BadJSON(t *testing.T) {
	ev := &WebhookEvent{Type: "issues", Payload: json.RawMessage(`invalid`)}
	_, err := ParseIssue(ev)
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestParseWorkflowRun(t *testing.T) {
	payload := `{"action":"completed","conclusion":"failure","workflow_run":{"name":"CI","status":"completed","html_url":"https://github.com"}}`
	ev := &WebhookEvent{Type: "workflow_run", Payload: json.RawMessage(payload)}
	p, err := ParseWorkflowRun(ev)
	if err != nil {
		t.Fatalf("ParseWorkflowRun: %v", err)
	}
	if p.Action != "completed" {
		t.Errorf("Action = %q, want completed", p.Action)
	}
}

func TestParseWorkflowRun_BadJSON(t *testing.T) {
	ev := &WebhookEvent{Type: "workflow_run", Payload: json.RawMessage(`bad`)}
	_, err := ParseWorkflowRun(ev)
	if err == nil {
		t.Error("expected error")
	}
}

// ── GitHub client (httptest) ──────────────────────────────────────────────────

func TestNewClient_NoToken(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "")
	_, err := NewClient("owner", "repo")
	if err == nil {
		t.Error("expected error when GITHUB_TOKEN missing")
	}
}

func TestNewClient_WithToken(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "tok")
	c, err := NewClient("owner", "repo")
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if c == nil {
		t.Fatal("expected non-nil client")
	}
}

func TestNewClientWithToken(t *testing.T) {
	c := NewClientWithToken("owner", "repo", "explicit-tok")
	if c == nil {
		t.Fatal("expected non-nil client")
	}
}

func TestListIssues(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		issues := []Issue{{Number: 1, Title: "Bug", State: "open"}}
		json.NewEncoder(w).Encode(issues) //nolint:errcheck
	}))
	defer srv.Close()

	c := newClientWithBase(srv.URL)
	issues, err := c.ListIssues("open", nil)
	if err != nil {
		t.Fatalf("ListIssues: %v", err)
	}
	if len(issues) != 1 || issues[0].Number != 1 {
		t.Errorf("unexpected issues: %v", issues)
	}
}

func TestListIssues_WithLabels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("labels") == "" {
			t.Error("expected labels param")
		}
		json.NewEncoder(w).Encode([]Issue{}) //nolint:errcheck
	}))
	defer srv.Close()

	c := newClientWithBase(srv.URL)
	_, err := c.ListIssues("open", []string{"bug", "enhancement"})
	if err != nil {
		t.Fatalf("ListIssues with labels: %v", err)
	}
}

func TestGetIssue(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(Issue{Number: 42, Title: "Feature"}) //nolint:errcheck
	}))
	defer srv.Close()

	c := newClientWithBase(srv.URL)
	issue, err := c.GetIssue(42)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if issue.Number != 42 {
		t.Errorf("Number = %d, want 42", issue.Number)
	}
}

func TestCreateIssue(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(Issue{Number: 7, Title: "New Bug"}) //nolint:errcheck
	}))
	defer srv.Close()

	c := newClientWithBase(srv.URL)
	issue, err := c.CreateIssue("New Bug", "desc", nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if issue.Number != 7 {
		t.Errorf("Number = %d, want 7", issue.Number)
	}
}

func TestCreateIssue_WithLabels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(Issue{Number: 8}) //nolint:errcheck
	}))
	defer srv.Close()

	c := newClientWithBase(srv.URL)
	_, err := c.CreateIssue("Titled", "body", []string{"bug"})
	if err != nil {
		t.Fatalf("CreateIssue with labels: %v", err)
	}
}

func TestAddComment(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(Comment{ID: 5, Body: "LGTM"}) //nolint:errcheck
	}))
	defer srv.Close()

	c := newClientWithBase(srv.URL)
	comment, err := c.AddComment(1, "LGTM")
	if err != nil {
		t.Fatalf("AddComment: %v", err)
	}
	if comment.ID != 5 {
		t.Errorf("ID = %d, want 5", comment.ID)
	}
}

func TestCloseIssue_WithComment(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(Comment{ID: 1}) //nolint:errcheck
			return
		}
		json.NewEncoder(w).Encode(Issue{Number: 1, State: "closed"}) //nolint:errcheck
	}))
	defer srv.Close()

	c := newClientWithBase(srv.URL)
	if err := c.CloseIssue(1, "closing"); err != nil {
		t.Fatalf("CloseIssue: %v", err)
	}
}

func TestCloseIssue_NoComment(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(Issue{Number: 1, State: "closed"}) //nolint:errcheck
	}))
	defer srv.Close()

	c := newClientWithBase(srv.URL)
	if err := c.CloseIssue(1, ""); err != nil {
		t.Fatalf("CloseIssue: %v", err)
	}
}

func TestUpdateIssue(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(Issue{Number: 3, Title: "Updated"}) //nolint:errcheck
	}))
	defer srv.Close()

	c := newClientWithBase(srv.URL)
	issue, err := c.UpdateIssue(3, map[string]interface{}{"title": "Updated"})
	if err != nil {
		t.Fatalf("UpdateIssue: %v", err)
	}
	if issue.Title != "Updated" {
		t.Errorf("Title = %q, want Updated", issue.Title)
	}
}

func TestGitHubClient_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer srv.Close()

	c := newClientWithBase(srv.URL)
	_, err := c.GetIssue(999)
	if err == nil {
		t.Error("expected error on 404")
	}
}

// newClientWithBase creates a client pointing at a test server.
// Replaces the hardcoded apiBase by injecting a custom http.Client transport
// that rewrites github.com to srv.URL.
func newClientWithBase(base string) *Client {
	transport := &rebaseTransport{base: base}
	return &Client{
		token:      "test-token",
		httpClient: &http.Client{Timeout: 5 * time.Second, Transport: transport},
		owner:      "owner",
		repo:       "repo",
	}
}

// rebaseTransport rewrites requests to https://api.github.com/... → base/...
type rebaseTransport struct {
	base string
}

func (t *rebaseTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	url := t.base + req.URL.Path
	if req.URL.RawQuery != "" {
		url += "?" + req.URL.RawQuery
	}
	newReq, err := http.NewRequest(req.Method, url, req.Body)
	if err != nil {
		return nil, err
	}
	for k, v := range req.Header {
		newReq.Header[k] = v
	}
	return http.DefaultTransport.RoundTrip(newReq)
}

// ── RepoMonitor ───────────────────────────────────────────────────────────────

func TestNewRepoMonitor(t *testing.T) {
	m := NewRepoMonitor()
	if m == nil {
		t.Fatal("expected non-nil monitor")
	}
}

func TestRepoMonitor_Enqueue(t *testing.T) {
	m := NewRepoMonitor()
	ev := &WebhookEvent{Type: "push", Delivery: "d1", Payload: json.RawMessage(`{}`)}
	m.Enqueue(ev)
}

func TestRepoMonitor_DispatchPush_TouchesReadme(t *testing.T) {
	m := NewRepoMonitor()
	called := false
	m.OnReadmeChange = func(_ json.RawMessage) { called = true }

	payload := `{"ref":"refs/heads/main","commits":[{"id":"a","message":"docs","added":["README.md"],"modified":[],"removed":[]}]}`
	m.Enqueue(&WebhookEvent{Type: "push", Payload: json.RawMessage(payload)})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	m.Start(ctx)
	// Give background goroutine time to drain.
	time.Sleep(100 * time.Millisecond)
	if !called {
		t.Error("expected OnReadmeChange to be called")
	}
}

func TestRepoMonitor_DispatchPush_NoReadme(t *testing.T) {
	m := NewRepoMonitor()
	m.OnReadmeChange = func(_ json.RawMessage) { t.Error("should not call OnReadmeChange") }

	payload := `{"ref":"refs/heads/main","commits":[{"id":"a","message":"feat","added":["src/x.go"],"modified":[],"removed":[]}]}`
	m.Enqueue(&WebhookEvent{Type: "push", Payload: json.RawMessage(payload)})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	m.Start(ctx)
	time.Sleep(100 * time.Millisecond)
}

func TestRepoMonitor_DispatchWorkflowFailure(t *testing.T) {
	m := NewRepoMonitor()
	called := false
	m.OnCIFailure = func(_ json.RawMessage) { called = true }

	payload := `{"action":"completed","conclusion":"failure","workflow_run":{"name":"CI","status":"completed","html_url":""}}`
	m.Enqueue(&WebhookEvent{Type: "workflow_run", Payload: json.RawMessage(payload)})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	m.Start(ctx)
	time.Sleep(100 * time.Millisecond)
	if !called {
		t.Error("expected OnCIFailure to be called")
	}
}

func TestRepoMonitor_DispatchWorkflowSuccess(t *testing.T) {
	m := NewRepoMonitor()
	m.OnCIFailure = func(_ json.RawMessage) { t.Error("should not call OnCIFailure on success") }

	payload := `{"action":"completed","conclusion":"success","workflow_run":{"name":"CI","status":"completed","html_url":""}}`
	m.Enqueue(&WebhookEvent{Type: "workflow_run", Payload: json.RawMessage(payload)})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	m.Start(ctx)
	time.Sleep(100 * time.Millisecond)
}

func TestRepoMonitor_DispatchIssueComment(t *testing.T) {
	m := NewRepoMonitor()
	called := false
	m.OnIssueComment = func(_ json.RawMessage) { called = true }

	m.Enqueue(&WebhookEvent{Type: "issue_comment", Payload: json.RawMessage(`{}`)})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	m.Start(ctx)
	time.Sleep(100 * time.Millisecond)
	if !called {
		t.Error("expected OnIssueComment to be called")
	}
}

func TestRepoMonitor_DispatchUnknownType(t *testing.T) {
	m := NewRepoMonitor()
	// Unknown event type — should not panic, just log.
	m.Enqueue(&WebhookEvent{Type: "star", Payload: json.RawMessage(`{}`)})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	m.Start(ctx)
	time.Sleep(100 * time.Millisecond)
}

func TestRepoMonitor_DispatchPush_BadJSON(t *testing.T) {
	m := NewRepoMonitor()
	m.OnReadmeChange = func(_ json.RawMessage) { t.Error("should not be called on bad JSON") }

	m.Enqueue(&WebhookEvent{Type: "push", Payload: json.RawMessage(`{bad`)})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	m.Start(ctx)
	time.Sleep(100 * time.Millisecond)
}

func TestRepoMonitor_DispatchWorkflowRun_BadJSON(t *testing.T) {
	m := NewRepoMonitor()
	m.OnCIFailure = func(_ json.RawMessage) { t.Error("should not be called on bad JSON") }

	m.Enqueue(&WebhookEvent{Type: "workflow_run", Payload: json.RawMessage(`{bad`)})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	m.Start(ctx)
	time.Sleep(100 * time.Millisecond)
}

func TestRepoMonitor_Enqueue_FullNotifyChannel(t *testing.T) {
	m := NewRepoMonitor()
	// Fill the notify channel so second Enqueue hits the default case.
	m.notifyCh <- struct{}{}
	m.Enqueue(&WebhookEvent{Type: "push", Payload: json.RawMessage(`{}`)})
	// Drain channel so it doesn't block.
	select {
	case <-m.notifyCh:
	default:
	}
}

func TestListIssues_ParseError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("not-json"))
	}))
	defer srv.Close()

	c := newClientWithBase(srv.URL)
	_, err := c.ListIssues("open", nil)
	if err == nil {
		t.Fatal("expected parse error")
	}
}

func TestGetIssue_ParseError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("not-json"))
	}))
	defer srv.Close()

	c := newClientWithBase(srv.URL)
	_, err := c.GetIssue(1)
	if err == nil {
		t.Fatal("expected parse error")
	}
}

func TestCreateIssue_DoPostError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := newClientWithBase(srv.URL)
	_, err := c.CreateIssue("title", "body", nil)
	if err == nil {
		t.Fatal("expected create issue error")
	}
}

func TestCreateIssue_ParseError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("not-json"))
	}))
	defer srv.Close()

	c := newClientWithBase(srv.URL)
	_, err := c.CreateIssue("title", "body", nil)
	if err == nil {
		t.Fatal("expected parse error")
	}
}

func TestAddComment_DoPostError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := newClientWithBase(srv.URL)
	_, err := c.AddComment(1, "hello")
	if err == nil {
		t.Fatal("expected add comment error")
	}
}

func TestAddComment_ParseError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("not-json"))
	}))
	defer srv.Close()

	c := newClientWithBase(srv.URL)
	_, err := c.AddComment(1, "hello")
	if err == nil {
		t.Fatal("expected parse error")
	}
}

func TestCloseIssue_AddCommentError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte(`{"number":1,"state":"closed"}`))
	}))
	defer srv.Close()

	c := newClientWithBase(srv.URL)
	err := c.CloseIssue(1, "closing")
	if err == nil {
		t.Fatal("expected close issue add-comment error")
	}
}

func TestCloseIssue_DoPatchError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := newClientWithBase(srv.URL)
	err := c.CloseIssue(1, "")
	if err == nil {
		t.Fatal("expected close issue patch error")
	}
}

func TestUpdateIssue_DoPatchError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := newClientWithBase(srv.URL)
	_, err := c.UpdateIssue(1, map[string]interface{}{"title": "x"})
	if err == nil {
		t.Fatal("expected update issue error")
	}
}

func TestUpdateIssue_ParseError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("not-json"))
	}))
	defer srv.Close()

	c := newClientWithBase(srv.URL)
	_, err := c.UpdateIssue(1, map[string]interface{}{"title": "x"})
	if err == nil {
		t.Fatal("expected parse error")
	}
}

func TestDoGet_BadURL(t *testing.T) {
	c := newClientWithBase("http://example.com")
	_, err := c.doGet("://bad-url")
	if err == nil {
		t.Fatal("expected bad URL error")
	}
}

func TestDoPost_MarshalError(t *testing.T) {
	c := newClientWithBase("http://example.com")
	_, err := c.doPost("http://example.com/x", map[string]interface{}{"bad": make(chan int)})
	if err == nil {
		t.Fatal("expected marshal error")
	}
}

func TestDoPatch_MarshalError(t *testing.T) {
	c := newClientWithBase("http://example.com")
	_, err := c.doPatch("http://example.com/x", map[string]interface{}{"bad": make(chan int)})
	if err == nil {
		t.Fatal("expected marshal error")
	}
}

func TestDoRequest_ReadBodyError(t *testing.T) {
	c := &Client{
		token: "tok",
		httpClient: &http.Client{Transport: roundTripFuncGH(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       readErrBodyGH{},
				Header:     make(http.Header),
			}, nil
		})},
	}
	req, _ := http.NewRequest(http.MethodGet, "https://api.github.com/test", nil)
	_, err := c.doRequest(req)
	if err == nil {
		t.Fatal("expected body read error")
	}
}

type roundTripFuncGH func(*http.Request) (*http.Response, error)

func (f roundTripFuncGH) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

type readErrBodyGH struct{}

func (readErrBodyGH) Read(p []byte) (int, error) { return 0, errors.New("read error") }

func (readErrBodyGH) Close() error { return nil }

func TestRepoMonitor_DispatchIssues_Opened(t *testing.T) {
	m := NewRepoMonitor()
	called := false
	m.OnIssueOpened = func(_ json.RawMessage) { called = true }

	payload := json.RawMessage(`{"action":"opened","issue":{"number":1,"title":"Bug","body":"desc"},"repository":{"full_name":"owner/repo"},"sender":{"login":"alice"}}`)
	m.Enqueue(&WebhookEvent{Type: "issues", Payload: payload})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	m.Start(ctx)
	time.Sleep(100 * time.Millisecond)
	if !called {
		t.Error("expected OnIssueOpened to be called")
	}
}

func TestRepoMonitor_DispatchIssues_BadJSON(t *testing.T) {
	m := NewRepoMonitor()
	m.OnIssueOpened = func(_ json.RawMessage) { t.Error("should not be called on bad JSON") }

	m.Enqueue(&WebhookEvent{Type: "issues", Payload: json.RawMessage(`{bad`)})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	m.Start(ctx)
	time.Sleep(100 * time.Millisecond)
}

func TestRepoMonitor_DispatchIssues_NotOpened(t *testing.T) {
	m := NewRepoMonitor()
	m.OnIssueOpened = func(_ json.RawMessage) { t.Error("should not be called for non-opened action") }

	payload := json.RawMessage(`{"action":"closed","issue":{"number":1,"title":"Bug"},"repository":{"full_name":"owner/repo"},"sender":{"login":"alice"}}`)
	m.Enqueue(&WebhookEvent{Type: "issues", Payload: payload})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	m.Start(ctx)
	time.Sleep(100 * time.Millisecond)
}

func TestRepoMonitor_TickerPath(t *testing.T) {
	m := NewRepoMonitor()
	m.tickInterval = 5 * time.Millisecond
	processed := make(chan struct{}, 1)
	m.OnReadmeChange = func(_ json.RawMessage) { processed <- struct{}{} }

	payload := json.RawMessage(`{"ref":"refs/heads/main","commits":[{"added":[],"removed":[],"modified":["README.md"]}],"repository":{"full_name":"o/r"}}`)
	// Directly inject into queue without sending to notifyCh, so only the ticker fires.
	m.mu.Lock()
	m.queue = append(m.queue, &MonitoredEvent{Type: "push", Payload: payload, Received: time.Now()})
	m.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	m.Start(ctx)
	select {
	case <-processed:
	case <-ctx.Done():
		t.Fatal("ticker path: process() never called via ticker")
	}
}

func TestListIssues_GetError(t *testing.T) {
	c := &Client{
		token: "tok",
		httpClient: &http.Client{Transport: roundTripFuncGH(func(req *http.Request) (*http.Response, error) {
			return nil, errors.New("connection refused")
		})},
		owner: "o",
		repo:  "r",
	}
	_, err := c.ListIssues("open", nil)
	if err == nil {
		t.Fatal("expected error from ListIssues")
	}
}

func TestDoPost_NewRequestError(t *testing.T) {
	c := newClientWithBase("http://example.com")
	_, err := c.doPost("://bad-url", map[string]string{"k": "v"})
	if err == nil {
		t.Fatal("expected NewRequest error")
	}
}

func TestDoPatch_NewRequestError(t *testing.T) {
	c := newClientWithBase("http://example.com")
	_, err := c.doPatch("://bad-url", map[string]string{"k": "v"})
	if err == nil {
		t.Fatal("expected NewRequest error")
	}
}

func TestDoRequest_DoError(t *testing.T) {
	c := &Client{
		token: "tok",
		httpClient: &http.Client{Transport: roundTripFuncGH(func(req *http.Request) (*http.Response, error) {
			return nil, errors.New("transport error")
		})},
	}
	req, _ := http.NewRequest(http.MethodGet, "https://api.github.com/test", nil)
	_, err := c.doRequest(req)
	if err == nil {
		t.Fatal("expected transport error")
	}
}

func TestStartDeviceFlow_RequestError(t *testing.T) {
	old := deviceCodeURL
	deviceCodeURL = "http://127.0.0.1:1" // port 1 is unreachable
	defer func() { deviceCodeURL = old }()
	_, err := StartDeviceFlow("client-id", "")
	if err == nil {
		t.Fatal("expected request error")
	}
}

func TestStartDeviceFlow_ParseQueryError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("%gg=invalid"))
	}))
	defer srv.Close()
	old := deviceCodeURL
	deviceCodeURL = srv.URL
	defer func() { deviceCodeURL = old }()
	_, err := StartDeviceFlow("client-id", "")
	if err == nil {
		t.Fatal("expected parse error")
	}
}

func TestPollForToken_HTTPError(t *testing.T) {
	old := oauthTokenURL
	oauthTokenURL = "http://127.0.0.1:1"
	defer func() { oauthTokenURL = old }()
	dcr := &DeviceCodeResponse{DeviceCode: "dc", Interval: 0, ExpiresIn: 1}
	var statusCalls []string
	_, _ = PollForToken("client-id", dcr, func(s string) { statusCalls = append(statusCalls, s) })
	found := false
	for _, s := range statusCalls {
		if len(s) > 6 && s[:6] == "error:" {
			found = true
		}
	}
	if !found {
		t.Error("expected onStatus called with error: prefix")
	}
}

func TestGetAuthenticatedUser_TransportError(t *testing.T) {
	old := oauthHTTPClient
	oauthHTTPClient = &http.Client{Transport: roundTripFuncGH(func(req *http.Request) (*http.Response, error) {
		return nil, errors.New("transport error")
	})}
	defer func() { oauthHTTPClient = old }()
	_, err := GetAuthenticatedUser("tok")
	if err == nil {
		t.Fatal("expected transport error")
	}
}

func TestRevokeToken_TransportError(t *testing.T) {
	old := oauthHTTPClient
	oauthHTTPClient = &http.Client{Transport: roundTripFuncGH(func(req *http.Request) (*http.Response, error) {
		return nil, errors.New("transport error")
	})}
	defer func() { oauthHTTPClient = old }()
	err := RevokeToken("client-id", "secret", "token")
	if err == nil {
		t.Fatal("expected transport error")
	}
}

func TestWebhookServeHTTP_BodyReadError(t *testing.T) {
	wr := NewWebhookReceiver("")
	req := httptest.NewRequest("POST", "/webhook", &errReader{})
	rr := httptest.NewRecorder()
	wr.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rr.Code)
	}
}

type errReader struct{}

func (errReader) Read(p []byte) (int, error) { return 0, errors.New("read error") }

func TestAPIBase_EnvOverride(t *testing.T) {
	t.Setenv("GITHUB_API_BASE", "http://custom.example.com")
	got := apiBase()
	if got != "http://custom.example.com" {
		t.Errorf("apiBase() = %q, want custom URL", got)
	}
}
