package github

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNormaliseStatusState_Mapping(t *testing.T) {
	cases := map[string]string{
		"plan":      "pending",
		"execute":   "pending",
		"review":    "pending",
		"validate":  "pending",
		"approve":   "success",
		"done":      "success",
		"failure":   "failure",
		"blocked":   "failure",
		"reject":    "failure",
		"":          "pending",
		"garbage":   "pending",
	}
	for in, want := range cases {
		if got := normaliseStatusState(in); got != want {
			t.Errorf("normaliseStatusState(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestClampDescription_Limit(t *testing.T) {
	long := strings.Repeat("x", 500)
	got := clampDescription(long)
	// 139 x's + "…" (one rune, 3 bytes UTF-8) — well under GitHub's hard cap.
	if !strings.HasSuffix(got, "…") {
		t.Errorf("expected ellipsis suffix, got %q", got)
	}
	if cnt := strings.Count(got, "x"); cnt != 139 {
		t.Errorf("clamped x-count = %d, want 139", cnt)
	}
}

func TestPostCommitStatus_MissingFieldsReturnsError(t *testing.T) {
	if err := PostCommitStatus("", "r", "sha", "pending", "ctx", "d", ""); err == nil {
		t.Error("expected error on empty owner")
	}
	if err := PostCommitStatus("o", "", "sha", "pending", "ctx", "d", ""); err == nil {
		t.Error("expected error on empty repo")
	}
	if err := PostCommitStatus("o", "r", "", "pending", "ctx", "d", ""); err == nil {
		t.Error("expected error on empty sha")
	}
}

func TestPostCommitStatus_PostsExpectedPayload(t *testing.T) {
	var captured map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &captured)
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte("{}"))
	}))
	defer server.Close()
	t.Setenv("GITHUB_API_BASE", server.URL)
	t.Setenv("GITHUB_TOKEN", "fake")

	if err := PostCommitStatus("rocky", "demo", "abc123", "execute", "engine/orchestrator", "step 2 underway", "https://example/run"); err != nil {
		t.Fatalf("post: %v", err)
	}
	if captured["state"] != "pending" {
		t.Errorf("state = %v, want pending", captured["state"])
	}
	if captured["context"] != "engine/orchestrator" {
		t.Errorf("context = %v", captured["context"])
	}
	if captured["target_url"] != "https://example/run" {
		t.Errorf("target_url = %v", captured["target_url"])
	}
}

func TestPostIssueComment_MissingFieldsReturnsError(t *testing.T) {
	if err := PostIssueComment("o", "r", 0, "hello"); err == nil {
		t.Error("expected error on issue 0")
	}
	if err := PostIssueComment("o", "r", 1, ""); err == nil {
		t.Error("expected error on empty body")
	}
}

func TestPostIssueComment_PostsExpectedPayload(t *testing.T) {
	var captured map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &captured)
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte("{}"))
	}))
	defer server.Close()
	t.Setenv("GITHUB_API_BASE", server.URL)
	t.Setenv("GITHUB_TOKEN", "fake")

	if err := PostIssueComment("rocky", "demo", 42, "hello"); err != nil {
		t.Fatalf("post: %v", err)
	}
	if captured["body"] != "hello" {
		t.Errorf("body = %v, want hello", captured["body"])
	}
}

func TestFindHeadSHA_ParsesShaField(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"sha":"deadbeefdeadbeef","commit":{}}`))
	}))
	defer server.Close()
	t.Setenv("GITHUB_API_BASE", server.URL)
	t.Setenv("GITHUB_TOKEN", "fake")

	got, err := FindHeadSHA("o", "r", "HEAD")
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if got != "deadbeefdeadbeef" {
		t.Errorf("sha = %q", got)
	}
}

func TestFindHeadSHA_EmptyRef_DefaultsToHEAD(t *testing.T) {
	var gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Write([]byte(`{"sha":"abcdef"}`))
	}))
	defer server.Close()
	t.Setenv("GITHUB_API_BASE", server.URL)
	t.Setenv("GITHUB_TOKEN", "fake")

	if _, err := FindHeadSHA("o", "r", ""); err != nil {
		t.Fatalf("find: %v", err)
	}
	if gotPath != "/repos/o/r/commits/HEAD" {
		t.Fatalf("path = %q, want /repos/o/r/commits/HEAD", gotPath)
	}
}

func TestFindHeadSHA_NewClientError_NoToken(t *testing.T) {
	t.Setenv("ENGINE_GITHUB_BOT_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "")

	if _, err := FindHeadSHA("o", "r", "main"); err == nil {
		t.Fatal("expected NewClient token error")
	}
}

func TestPostCommitStatus_NoTokenIsBestEffort(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "")
	if err := PostCommitStatus("o", "r", "abc", "pending", "ctx", "d", ""); err != nil {
		t.Errorf("expected nil for missing token (best-effort), got %v", err)
	}
}
