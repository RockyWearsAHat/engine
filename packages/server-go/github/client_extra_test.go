package github

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRemoveAssignees_EmptyNoop(t *testing.T) {
	c := newClientWithBase("http://example.com")
	if err := c.RemoveAssignees(1, nil); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func TestAddAssignees_EmptyNoop(t *testing.T) {
	c := newClientWithBase("http://example.com")
	if err := c.AddAssignees(1, nil); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func TestRemoveAssignees_Success(t *testing.T) {
	var method string
	var payload map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method = r.Method
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode remove assignees payload: %v", err)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	c := newClientWithBase(srv.URL)
	if err := c.RemoveAssignees(9, []string{"engine-bot"}); err != nil {
		t.Fatalf("RemoveAssignees: %v", err)
	}
	if method != http.MethodDelete {
		t.Fatalf("method = %s, want DELETE", method)
	}
	list, ok := payload["assignees"].([]any)
	if !ok || len(list) != 1 || list[0] != "engine-bot" {
		t.Fatalf("unexpected payload: %#v", payload)
	}
}

func TestRemoveAssignees_InvalidBase_RequestError(t *testing.T) {
	t.Setenv("GITHUB_API_BASE", "://bad")
	c := NewClientWithToken("owner", "repo", "tok")

	if err := c.RemoveAssignees(1, []string{"engine-bot"}); err == nil {
		t.Fatal("expected request construction error")
	}
}

func TestCreatePR_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"number":17,"html_url":"https://example/pr/17","state":"open"}`))
	}))
	defer srv.Close()

	c := newClientWithBase(srv.URL)
	pr, err := c.CreatePR("title", "body", "feature", "main")
	if err != nil {
		t.Fatalf("CreatePR: %v", err)
	}
	if pr.Number != 17 || pr.State != "open" {
		t.Fatalf("unexpected PR: %+v", pr)
	}
}

func TestCreatePR_ParseError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("not-json"))
	}))
	defer srv.Close()

	c := newClientWithBase(srv.URL)
	if _, err := c.CreatePR("title", "body", "feature", "main"); err == nil {
		t.Fatal("expected parse error")
	}
}

func TestUnassignEngine_Success(t *testing.T) {
	t.Setenv("ENGINE_GITHUB_LOGIN", "engine-bot")
	t.Setenv("ENGINE_GITHUB_BOT_TOKEN", "tok")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Fatalf("method = %s, want DELETE", r.Method)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	t.Setenv("GITHUB_API_BASE", srv.URL)
	if err := UnassignEngine("owner", "repo", 22); err != nil {
		t.Fatalf("UnassignEngine: %v", err)
	}
}
