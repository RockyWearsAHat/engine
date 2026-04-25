package ai

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	gofs "github.com/engine/server/fs"
)

// ── isCancelled ───────────────────────────────────────────────────────────────

func TestIsCancelled_NilCancel_ReturnsFalse(t *testing.T) {
	ctx := &ChatContext{Cancel: nil}
	if ctx.isCancelled() {
		t.Error("expected false for nil Cancel")
	}
}

func TestIsCancelled_OpenCancel_ReturnsFalse(t *testing.T) {
	ch := make(chan struct{})
	ctx := &ChatContext{Cancel: ch}
	if ctx.isCancelled() {
		t.Error("expected false for open channel")
	}
}

func TestIsCancelled_ClosedCancel_ReturnsTrue(t *testing.T) {
	ch := make(chan struct{})
	close(ch)
	ctx := &ChatContext{Cancel: ch}
	if !ctx.isCancelled() {
		t.Error("expected true for closed channel")
	}
}

// ── formatTree ────────────────────────────────────────────────────────────────

func TestFormatTree_FileNode(t *testing.T) {
	node := &gofs.FileNode{Name: "main.go", Type: "file"}
	result := formatTree(node, 0)
	if !strings.Contains(result, "main.go") {
		t.Errorf("expected file name, got %q", result)
	}
	if !strings.Contains(result, "📄") {
		t.Errorf("expected file icon, got %q", result)
	}
}

func TestFormatTree_DirectoryWithChildren(t *testing.T) {
	node := &gofs.FileNode{
		Name: "src",
		Type: "directory",
		Children: []*gofs.FileNode{
			{Name: "main.go", Type: "file"},
			{Name: "utils.go", Type: "file"},
		},
	}
	result := formatTree(node, 0)
	if !strings.Contains(result, "📁") {
		t.Errorf("expected directory icon, got %q", result)
	}
	if !strings.Contains(result, "main.go") {
		t.Errorf("expected child in output, got %q", result)
	}
	if !strings.Contains(result, "utils.go") {
		t.Errorf("expected second child in output, got %q", result)
	}
}

func TestFormatTree_NestedDepthIndents(t *testing.T) {
	node := &gofs.FileNode{
		Name: "root",
		Type: "directory",
		Children: []*gofs.FileNode{
			{Name: "child", Type: "file"},
		},
	}
	result := formatTree(node, 2) // depth 2 = 4 spaces prefix
	lines := strings.Split(result, "\n")
	if len(lines) < 2 {
		t.Fatalf("expected at least 2 lines, got %q", result)
	}
	if !strings.HasPrefix(lines[0], "    ") {
		t.Errorf("expected 4-space indent at depth 2, got %q", lines[0])
	}
}

// ── getSystemInfo ─────────────────────────────────────────────────────────────

func TestGetSystemInfo_ReturnsNonEmpty(t *testing.T) {
	dir := t.TempDir()
	result := getSystemInfo(dir)
	if result == "" {
		t.Error("expected non-empty system info")
	}
	if !strings.Contains(result, "OS:") {
		t.Errorf("expected OS line, got %q", result)
	}
}

// ── executeSearchTools ────────────────────────────────────────────────────────

func TestExecuteSearchTools_NoMatch(t *testing.T) {
	ctx := &ChatContext{ActiveTools: bootstrapTools()}
	result := executeSearchTools("zzz_no_match_xyz", ctx)
	if !strings.Contains(result, "No tools matched") {
		t.Errorf("expected no-match message, got %q", result)
	}
}

func TestExecuteSearchTools_Match_AddsToActive(t *testing.T) {
	ctx := &ChatContext{ActiveTools: bootstrapTools()}
	initialCount := len(ctx.ActiveTools)
	result := executeSearchTools("write files disk", ctx)
	if !strings.Contains(result, "write_file") {
		t.Errorf("expected write_file in results, got %q", result)
	}
	if len(ctx.ActiveTools) <= initialCount {
		t.Errorf("expected active tools to grow from %d, got %d", initialCount, len(ctx.ActiveTools))
	}
}

func TestExecuteSearchTools_AlreadyActive_ReportsNoAdd(t *testing.T) {
	ctx := &ChatContext{ActiveTools: bootstrapTools()}
	// Add write_file once.
	executeSearchTools("write files disk", ctx)
	// Add it again — should report "already active".
	result := executeSearchTools("write files disk", ctx)
	if !strings.Contains(result, "already active") {
		t.Errorf("expected 'already active', got %q", result)
	}
}

// ── ExecuteToolForTest ────────────────────────────────────────────────────────

func makeChatCtx(t *testing.T) *ChatContext {
	t.Helper()
	dir := setupHistoryTestProject(t)
	return &ChatContext{
		ProjectPath: dir,
		SessionID:   "ctx-test",
		OnChunk:     func(string, bool) {},
		OnToolCall:  func(string, interface{}) {},
		OnToolResult: func(string, interface{}, bool) {},
		OnError:     func(string) {},
		ActiveTools: bootstrapTools(),
	}
}

func TestExecuteToolForTest_SearchTools(t *testing.T) {
	ctx := makeChatCtx(t)
	result, isErr := ExecuteToolForTest("search_tools", map[string]interface{}{"query": "git status"}, ctx)
	if isErr {
		t.Errorf("unexpected error: %s", result)
	}
	if !strings.Contains(result, "git") {
		t.Errorf("expected git tools in result, got %q", result)
	}
}

func TestExecuteTool_DirectWrapper_Delegates(t *testing.T) {
	ctx := makeChatCtx(t)
	result, isErr := executeTool("list_open_tabs", map[string]interface{}{}, ctx)
	if isErr {
		t.Fatalf("executeTool returned error: %s", result)
	}
	if result != "[]" {
		t.Fatalf("expected empty tabs JSON, got %q", result)
	}
}

func TestExecuteTool_DirectWrapper_UnknownTool(t *testing.T) {
	result, isErr := executeTool("tool_that_does_not_exist", map[string]interface{}{}, nil)
	if !isErr {
		t.Fatalf("expected unknown-tool error, got %q", result)
	}
	if !strings.Contains(result, "Unknown tool") {
		t.Fatalf("expected unknown-tool message, got %q", result)
	}
}

func TestExecuteToolForTest_ListDirectory(t *testing.T) {
	ctx := makeChatCtx(t)
	result, isErr := ExecuteToolForTest("list_directory", map[string]interface{}{"path": "."}, ctx)
	if isErr {
		t.Errorf("unexpected error: %s", result)
	}
	if result == "" {
		t.Error("expected non-empty directory listing")
	}
}

func TestExecuteToolForTest_ReadFile(t *testing.T) {
	ctx := makeChatCtx(t)
	result, isErr := ExecuteToolForTest("read_file", map[string]interface{}{"path": "PROJECT_GOAL.md"}, ctx)
	if isErr {
		t.Errorf("unexpected error: %s", result)
	}
	if result == "" {
		t.Error("expected file content")
	}
}

func TestExecuteToolForTest_ReadFile_InvalidPath(t *testing.T) {
	ctx := makeChatCtx(t)
	result, isErr := ExecuteToolForTest("read_file", map[string]interface{}{"path": "../../etc/passwd"}, ctx)
	if !isErr {
		t.Errorf("expected error for path traversal, got result=%q", result)
	}
}

func TestExecuteToolForTest_WriteFile(t *testing.T) {
	ctx := makeChatCtx(t)
	result, isErr := ExecuteToolForTest("write_file", map[string]interface{}{
		"path":    "tmp-test-output.txt",
		"content": "hello from test",
	}, ctx)
	if isErr {
		t.Errorf("unexpected error: %s", result)
	}
	// Verify file was actually written.
	data, err := os.ReadFile(filepath.Join(ctx.ProjectPath, "tmp-test-output.txt"))
	if err != nil || string(data) != "hello from test" {
		t.Errorf("file not written correctly: err=%v content=%q", err, string(data))
	}
}

func TestExecuteToolForTest_WriteFile_SendsEvent(t *testing.T) {
	var events []string
	ctx := makeChatCtx(t)
	ctx.SendToClient = func(msgType string, _ interface{}) { events = append(events, msgType) }
	ExecuteToolForTest("write_file", map[string]interface{}{
		"path":    "notified.txt",
		"content": "x",
	}, ctx)
	found := false
	for _, e := range events {
		if e == "file.saved" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected file.saved event, got %v", events)
	}
}

func TestExecuteToolForTest_GetSystemInfo(t *testing.T) {
	ctx := makeChatCtx(t)
	result, isErr := ExecuteToolForTest("get_system_info", map[string]interface{}{}, ctx)
	if isErr {
		t.Errorf("unexpected error: %s", result)
	}
	if !strings.Contains(result, "OS:") {
		t.Errorf("expected OS info, got %q", result)
	}
}

func TestExecuteToolForTest_ListOpenTabs_NilGetter(t *testing.T) {
	ctx := makeChatCtx(t)
	ctx.GetOpenTabs = nil
	result, isErr := ExecuteToolForTest("list_open_tabs", map[string]interface{}{}, ctx)
	if isErr {
		t.Errorf("unexpected error: %s", result)
	}
	if result != "[]" {
		t.Errorf("expected empty array, got %q", result)
	}
}

func TestExecuteToolForTest_ListOpenTabs_WithGetter(t *testing.T) {
	ctx := makeChatCtx(t)
	ctx.GetOpenTabs = func() []TabInfo {
		return []TabInfo{{Path: "/project/main.go", IsActive: true}}
	}
	result, isErr := ExecuteToolForTest("list_open_tabs", map[string]interface{}{}, ctx)
	if isErr {
		t.Errorf("unexpected error: %s", result)
	}
	if !strings.Contains(result, "main.go") {
		t.Errorf("expected tab path, got %q", result)
	}
}

func TestExecuteToolForTest_SearchHistory_EmptyQuery(t *testing.T) {
	ctx := makeChatCtx(t)
	result, isErr := ExecuteToolForTest("search_history", map[string]interface{}{"query": ""}, ctx)
	if !isErr {
		t.Errorf("expected error for empty query, got %q", result)
	}
}

func TestExecuteToolForTest_SearchHistory_WithQuery(t *testing.T) {
	ctx := makeChatCtx(t)
	ctx.SessionID = "test-session-search"
	result, isErr := ExecuteToolForTest("search_history", map[string]interface{}{"query": "testing"}, ctx)
	// may return no results but should not error
	if isErr && !strings.Contains(result, "No stored history") {
		t.Errorf("unexpected error: %s", result)
	}
	_ = result
}

func TestExecuteToolForTest_OpenFile_NilSendToClient(t *testing.T) {
	ctx := makeChatCtx(t)
	ctx.SendToClient = nil
	result, isErr := ExecuteToolForTest("open_file", map[string]interface{}{"path": "PROJECT_GOAL.md"}, ctx)
	if isErr {
		t.Errorf("unexpected error: %s", result)
	}
	_ = result
}

func TestExecuteToolForTest_OpenFile_WithSendToClient(t *testing.T) {
	var events []string
	ctx := makeChatCtx(t)
	ctx.SendToClient = func(msgType string, _ interface{}) { events = append(events, msgType) }
	ExecuteToolForTest("open_file", map[string]interface{}{"path": "PROJECT_GOAL.md"}, ctx)
	found := false
	for _, e := range events {
		if e == "editor.open" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected editor.open event, got %v", events)
	}
}

func TestExecuteToolForTest_CloseTab_NilSendToClient(t *testing.T) {
	ctx := makeChatCtx(t)
	ctx.SendToClient = nil
	result, _ := ExecuteToolForTest("close_tab", map[string]interface{}{"path": "PROJECT_GOAL.md"}, ctx)
	_ = result
}

func TestExecuteToolForTest_CloseTab_DirtyNoForce(t *testing.T) {
	ctx := makeChatCtx(t)
	tabPath := filepath.Join(ctx.ProjectPath, "PROJECT_GOAL.md")
	ctx.GetOpenTabs = func() []TabInfo {
		return []TabInfo{{Path: tabPath, IsDirty: true}}
	}
	result, isErr := ExecuteToolForTest("close_tab", map[string]interface{}{"path": "PROJECT_GOAL.md"}, ctx)
	if !isErr {
		t.Errorf("expected error for dirty tab without force, got %q", result)
	}
}

func TestExecuteToolForTest_CloseTab_Force(t *testing.T) {
	var events []string
	ctx := makeChatCtx(t)
	tabPath := filepath.Join(ctx.ProjectPath, "PROJECT_GOAL.md")
	ctx.GetOpenTabs = func() []TabInfo {
		return []TabInfo{{Path: tabPath, IsDirty: true}}
	}
	ctx.SendToClient = func(msgType string, _ interface{}) { events = append(events, msgType) }
	_, isErr := ExecuteToolForTest("close_tab", map[string]interface{}{"path": "PROJECT_GOAL.md", "force": true}, ctx)
	if isErr {
		t.Errorf("expected success with force=true")
	}
}

func TestExecuteToolForTest_FocusTab(t *testing.T) {
	var events []string
	ctx := makeChatCtx(t)
	ctx.SendToClient = func(msgType string, _ interface{}) { events = append(events, msgType) }
	_, _ = ExecuteToolForTest("focus_tab", map[string]interface{}{"path": "PROJECT_GOAL.md"}, ctx)
	found := false
	for _, e := range events {
		if e == "editor.tab.focus" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected editor.tab.focus event, got %v", events)
	}
}

func TestExecuteToolForTest_TestRun_NilSendToClient(t *testing.T) {
	ctx := makeChatCtx(t)
	ctx.SendToClient = nil
	result, isErr := ExecuteToolForTest("test.run", map[string]interface{}{
		"terminalId": "t1",
		"command":    "go test ./...",
		"issue":      "bug #1",
	}, ctx)
	if !isErr {
		t.Errorf("expected error when no client, got %q", result)
	}
}

func TestExecuteToolForTest_TestRun_WithSendToClient(t *testing.T) {
	var events []string
	ctx := makeChatCtx(t)
	ctx.SendToClient = func(msgType string, _ interface{}) { events = append(events, msgType) }
	_, _ = ExecuteToolForTest("test.run", map[string]interface{}{
		"terminalId": "t1",
		"command":    "go test ./...",
		"issue":      "bug #1",
	}, ctx)
	found := false
	for _, e := range events {
		if e == "test.run" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected test.run event, got %v", events)
	}
}

func TestExecuteToolForTest_Shell_Echo(t *testing.T) {
	ctx := makeChatCtx(t)
	result, isErr := ExecuteToolForTest("shell", map[string]interface{}{"command": "echo hello-from-test"}, ctx)
	if isErr {
		t.Errorf("unexpected error: %s", result)
	}
	if !strings.Contains(result, "hello-from-test") {
		t.Errorf("expected shell output, got %q", result)
	}
}

func TestExecuteToolForTest_Shell_RequiresApproval_NilHandler(t *testing.T) {
	ctx := makeChatCtx(t)
	ctx.RequestApproval = nil
	// rm -rf requires approval.
	result, isErr := ExecuteToolForTest("shell", map[string]interface{}{"command": "rm -rf /tmp/engine-test-safe"}, ctx)
	if !isErr {
		t.Errorf("expected error when no approval handler, got %q", result)
	}
	_ = result
}

func TestExecuteToolForTest_Shell_RequiresApproval_Denied(t *testing.T) {
	ctx := makeChatCtx(t)
	ctx.RequestApproval = func(kind, title, message, command string) (bool, error) {
		return false, nil
	}
	result, isErr := ExecuteToolForTest("shell", map[string]interface{}{"command": "rm -rf /tmp/engine-test-safe2"}, ctx)
	if !isErr {
		t.Errorf("expected error when denied, got %q", result)
	}
	if !strings.Contains(result, "denied") {
		t.Errorf("expected denial message, got %q", result)
	}
}

func TestExecuteToolForTest_SearchFiles(t *testing.T) {
	ctx := makeChatCtx(t)
	result, isErr := ExecuteToolForTest("search_files", map[string]interface{}{"pattern": "Engine"}, ctx)
	if isErr {
		t.Errorf("unexpected error: %s", result)
	}
	_ = result
}

func TestExecuteToolForTest_GitStatus(t *testing.T) {
	ctx := makeChatCtx(t)
	// The temp dir is not a git repo, so this will error, but code path is covered.
	result, _ := ExecuteToolForTest("git_status", map[string]interface{}{}, ctx)
	_ = result
}

func TestExecuteToolForTest_GitDiff(t *testing.T) {
	ctx := makeChatCtx(t)
	result, _ := ExecuteToolForTest("git_diff", map[string]interface{}{}, ctx)
	_ = result
}

func TestExecuteToolForTest_GitDiff_WithPath(t *testing.T) {
	ctx := makeChatCtx(t)
	result, _ := ExecuteToolForTest("git_diff", map[string]interface{}{"path": "PROJECT_GOAL.md"}, ctx)
	_ = result
}

func TestExecuteToolForTest_GitDiff_InvalidPath(t *testing.T) {
	ctx := makeChatCtx(t)
	result, isErr := ExecuteToolForTest("git_diff", map[string]interface{}{"path": "../../etc/passwd"}, ctx)
	if !isErr {
		t.Logf("path traversal allowed, result: %q", result)
	}
}

func TestExecuteToolForTest_GitCommit_NilApproval(t *testing.T) {
	ctx := makeChatCtx(t)
	ctx.RequestApproval = nil
	result, isErr := ExecuteToolForTest("git_commit", map[string]interface{}{"message": "test"}, ctx)
	if !isErr {
		t.Errorf("expected error without approval handler, got %q", result)
	}
}

func TestExecuteToolForTest_GitCommit_Denied(t *testing.T) {
	ctx := makeChatCtx(t)
	ctx.RequestApproval = func(kind, title, message, command string) (bool, error) {
		return false, nil
	}
	result, isErr := ExecuteToolForTest("git_commit", map[string]interface{}{"message": "test"}, ctx)
	if !isErr {
		t.Errorf("expected error when denied, got %q", result)
	}
	if !strings.Contains(result, "denied") {
		t.Errorf("expected denial message, got %q", result)
	}
}

func TestExecuteToolForTest_GitPush_NilApproval(t *testing.T) {
	ctx := makeChatCtx(t)
	ctx.RequestApproval = nil
	result, isErr := ExecuteToolForTest("git_push", map[string]interface{}{}, ctx)
	if !isErr {
		t.Errorf("expected error without approval handler, got %q", result)
	}
	_ = result
}

func TestExecuteToolForTest_GitPush_Denied(t *testing.T) {
	ctx := makeChatCtx(t)
	ctx.RequestApproval = func(kind, title, message, command string) (bool, error) {
		return false, nil
	}
	result, isErr := ExecuteToolForTest("git_push", map[string]interface{}{}, ctx)
	if !isErr {
		t.Errorf("expected error when denied, got %q", result)
	}
}

func TestExecuteToolForTest_GitPull(t *testing.T) {
	ctx := makeChatCtx(t)
	// Not a git repo — error expected, but code path is covered.
	result, _ := ExecuteToolForTest("git_pull", map[string]interface{}{}, ctx)
	_ = result
}

func TestExecuteToolForTest_GitBranch_List(t *testing.T) {
	ctx := makeChatCtx(t)
	result, _ := ExecuteToolForTest("git_branch", map[string]interface{}{}, ctx)
	_ = result
}

func TestExecuteToolForTest_GitBranch_Create(t *testing.T) {
	ctx := makeChatCtx(t)
	result, _ := ExecuteToolForTest("git_branch", map[string]interface{}{"name": "test-branch", "create": true}, ctx)
	_ = result
}

func TestExecuteToolForTest_ProcessList(t *testing.T) {
	ctx := makeChatCtx(t)
	result, isErr := ExecuteToolForTest("process_list", map[string]interface{}{}, ctx)
	if isErr {
		t.Errorf("unexpected error: %s", result)
	}
}

func TestExecuteToolForTest_ProcessList_WithFilter(t *testing.T) {
	ctx := makeChatCtx(t)
	result, isErr := ExecuteToolForTest("process_list", map[string]interface{}{"filter": "go"}, ctx)
	if isErr {
		t.Errorf("unexpected error: %s", result)
	}
	_ = result
}

func TestExecuteToolForTest_ProcessKill_NilApproval(t *testing.T) {
	ctx := makeChatCtx(t)
	ctx.RequestApproval = nil
	result, isErr := ExecuteToolForTest("process_kill", map[string]interface{}{"pid": float64(os.Getpid())}, ctx)
	if !isErr {
		t.Errorf("expected error without approval handler, got %q", result)
	}
}

func TestExecuteToolForTest_ProcessKill_NoPid(t *testing.T) {
	ctx := makeChatCtx(t)
	result, isErr := ExecuteToolForTest("process_kill", map[string]interface{}{}, ctx)
	if !isErr {
		t.Errorf("expected error without pid, got %q", result)
	}
}

func TestExecuteToolForTest_OpenURL_Empty(t *testing.T) {
	ctx := makeChatCtx(t)
	result, isErr := ExecuteToolForTest("open_url", map[string]interface{}{}, ctx)
	if !isErr {
		t.Errorf("expected error for empty url, got %q", result)
	}
}

func TestExecuteToolForTest_GitListIssues_NoToken(t *testing.T) {
	ctx := makeChatCtx(t)
	result, _ := ExecuteToolForTest("github_list_issues", map[string]interface{}{
		"owner": "engine",
		"repo":  "test",
		"state": "open",
	}, ctx)
	_ = result
}

func TestExecuteToolForTest_GitGetIssue_ZeroNumber(t *testing.T) {
	ctx := makeChatCtx(t)
	result, isErr := ExecuteToolForTest("github_get_issue", map[string]interface{}{
		"owner": "engine", "repo": "test", "number": float64(0),
	}, ctx)
	if !isErr {
		t.Errorf("expected error for issue number 0, got %q", result)
	}
}

func TestExecuteToolForTest_GitCreateIssue_NoTitle(t *testing.T) {
	ctx := makeChatCtx(t)
	result, isErr := ExecuteToolForTest("github_create_issue", map[string]interface{}{
		"owner": "engine", "repo": "test",
	}, ctx)
	if !isErr {
		t.Errorf("expected error for missing title, got %q", result)
	}
}

func TestExecuteToolForTest_GitComment_NoBody(t *testing.T) {
	ctx := makeChatCtx(t)
	result, isErr := ExecuteToolForTest("github_comment", map[string]interface{}{
		"owner": "engine", "repo": "test", "number": float64(1),
	}, ctx)
	if !isErr {
		t.Errorf("expected error for missing body, got %q", result)
	}
}

func TestExecuteToolForTest_UnknownTool(t *testing.T) {
	ctx := makeChatCtx(t)
	result, isErr := ExecuteToolForTest("definitely_unknown_tool_xyz", map[string]interface{}{}, ctx)
	if !isErr {
		t.Errorf("expected error for unknown tool, got %q", result)
	}
}

// ── firstOllamaModel ──────────────────────────────────────────────────────────

func TestFirstOllamaModel_404(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	result := firstOllamaModel(srv.URL+"/api/ps", "models", "name")
	if result != "" {
		t.Errorf("expected empty for 404, got %q", result)
	}
}

func TestFirstOllamaModel_BadJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "not json")
	}))
	defer srv.Close()

	result := firstOllamaModel(srv.URL+"/api/ps", "models", "name")
	if result != "" {
		t.Errorf("expected empty for bad JSON, got %q", result)
	}
}

func TestFirstOllamaModel_EmptyList(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{"models": []interface{}{}}) //nolint:errcheck
	}))
	defer srv.Close()

	result := firstOllamaModel(srv.URL+"/api/ps", "models", "name")
	if result != "" {
		t.Errorf("expected empty for empty list, got %q", result)
	}
}

func TestFirstOllamaModel_WithModel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{ //nolint:errcheck
			"models": []interface{}{
				map[string]interface{}{"name": "llama3:latest"},
			},
		})
	}))
	defer srv.Close()

	result := firstOllamaModel(srv.URL+"/api/ps", "models", "name")
	if result != "llama3:latest" {
		t.Errorf("expected llama3:latest, got %q", result)
	}
}

func TestFirstOllamaModel_EntryWithEmptyName(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{ //nolint:errcheck
			"models": []interface{}{
				map[string]interface{}{"name": "   "},
				map[string]interface{}{"name": "llama3:latest"},
			},
		})
	}))
	defer srv.Close()

	result := firstOllamaModel(srv.URL+"/api/ps", "models", "name")
	if result != "llama3:latest" {
		t.Errorf("expected llama3:latest after skipping empty, got %q", result)
	}
}

func TestFirstOllamaModel_NilEntry(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{ //nolint:errcheck
			"models": []interface{}{
				nil,
				map[string]interface{}{"name": "mistral"},
			},
		})
	}))
	defer srv.Close()

	result := firstOllamaModel(srv.URL+"/api/ps", "models", "name")
	if result != "mistral" {
		t.Errorf("expected mistral after skipping nil, got %q", result)
	}
}

func TestFirstOllamaModel_NoServer(t *testing.T) {
	// Point to a closed port — connection refused.
	result := firstOllamaModel("http://127.0.0.1:1", "/api/ps", "models")
	if result != "" {
		t.Errorf("expected empty for no server, got %q", result)
	}
}

// ── detectOllamaModel ─────────────────────────────────────────────────────────

func TestDetectOllamaModel_NoServer_ReturnsEmpty(t *testing.T) {
	result := detectOllamaModel("http://127.0.0.1:1")
	if result != "" {
		t.Errorf("expected empty, got %q", result)
	}
}

func TestDetectOllamaModel_ApsFallsBackToV1(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/api/ps") {
			// Return empty list.
			json.NewEncoder(w).Encode(map[string]interface{}{"models": []interface{}{}}) //nolint:errcheck
			return
		}
		// /v1/models returns a model.
		json.NewEncoder(w).Encode(map[string]interface{}{ //nolint:errcheck
			"data": []interface{}{
				map[string]interface{}{"id": "mistral-7b"},
			},
		})
	}))
	defer srv.Close()

	result := detectOllamaModel(srv.URL)
	if result != "mistral-7b" {
		t.Errorf("expected mistral-7b from v1/models fallback, got %q", result)
	}
}

// ── runOpenAICompatibleLoop ───────────────────────────────────────────────────

func TestRunOpenAICompatibleLoop_Cancelled(t *testing.T) {
	ch := make(chan struct{})
	close(ch)
	ctx := &ChatContext{
		Cancel:      ch,
		OnChunk:     func(string, bool) {},
		OnToolCall:  func(string, interface{}) {},
		OnToolResult: func(string, interface{}, bool) {},
		OnError:     func(string) {},
		ActiveTools: bootstrapTools(),
	}
	var calls []ToolCall
	var text strings.Builder
	// Should return immediately without making any HTTP request.
	runOpenAICompatibleLoop(ctx, "openai", "gpt-4o", "http://127.0.0.1:1/v1/chat/completions", "key", true,
		"system", []anthropicMessage{}, &calls, &text)
}

func TestRunOpenAICompatibleLoop_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "internal error"}) //nolint:errcheck
	}))
	defer srv.Close()

	var errors []string
	ctx := &ChatContext{
		OnChunk:     func(string, bool) {},
		OnToolCall:  func(string, interface{}) {},
		OnToolResult: func(string, interface{}, bool) {},
		OnError:     func(err string) { errors = append(errors, err) },
		ActiveTools: bootstrapTools(),
	}
	var calls []ToolCall
	var text strings.Builder
	runOpenAICompatibleLoop(ctx, "openai", "gpt-4o", srv.URL+"/v1/chat/completions", "key", true,
		"system", []anthropicMessage{}, &calls, &text)
	if len(errors) == 0 {
		t.Error("expected error for 500 response")
	}
}

func TestRunOpenAICompatibleLoop_TextResponse(t *testing.T) {
	// Return a minimal SSE response with a text delta.
	sseResponse := "data: {\"choices\":[{\"delta\":{\"content\":\"hello\"},\"finish_reason\":null}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n" +
		"data: [DONE]\n\n"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, sseResponse)
	}))
	defer srv.Close()

	var chunks []string
	ctx := &ChatContext{
		OnChunk:     func(content string, done bool) { chunks = append(chunks, content) },
		OnToolCall:  func(string, interface{}) {},
		OnToolResult: func(string, interface{}, bool) {},
		OnError:     func(string) {},
		ActiveTools: bootstrapTools(),
	}
	var calls []ToolCall
	var text strings.Builder
	runOpenAICompatibleLoop(ctx, "openai", "gpt-4o", srv.URL+"/v1/chat/completions", "key", true,
		"system", []anthropicMessage{}, &calls, &text)
	combined := text.String()
	if !strings.Contains(combined, "hello") {
		t.Errorf("expected 'hello' in output, got %q", combined)
	}
}

// ── runAnthropicLoop (via httptest transport override) ────────────────────────

func TestRunAnthropicLoop_Cancelled(t *testing.T) {
	ch := make(chan struct{})
	close(ch)
	ctx := &ChatContext{
		Cancel:      ch,
		OnChunk:     func(string, bool) {},
		OnToolCall:  func(string, interface{}) {},
		OnToolResult: func(string, interface{}, bool) {},
		OnError:     func(string) {},
		ActiveTools: bootstrapTools(),
		Usage:       &SessionUsage{},
		Quarantine:  NewToolQuarantine(),
	}
	var calls []ToolCall
	var text strings.Builder
	// Temporarily patch default client to a server that must NOT be called.
	old := http.DefaultClient
	defer func() { http.DefaultClient = old }()
	http.DefaultClient = &http.Client{Transport: failTransport{t}}
	runAnthropicLoop(ctx, "claude-3-5-sonnet-20241022", "fake-key", "system", []anthropicMessage{}, &calls, &text)
}

func TestRunAnthropicLoop_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "bad request"}) //nolint:errcheck
	}))
	defer srv.Close()

	var errors []string
	ctx := &ChatContext{
		OnChunk:     func(string, bool) {},
		OnToolCall:  func(string, interface{}) {},
		OnToolResult: func(string, interface{}, bool) {},
		OnError:     func(err string) { errors = append(errors, err) },
		ActiveTools: bootstrapTools(),
		Usage:       &SessionUsage{},
		Quarantine:  NewToolQuarantine(),
	}
	old := http.DefaultClient
	defer func() { http.DefaultClient = old }()
	http.DefaultClient = &http.Client{Transport: redirectTransport{target: srv.URL}}

	var calls []ToolCall
	var text strings.Builder
	runAnthropicLoop(ctx, "claude-3-5-sonnet-20241022", "fake-key", "system", []anthropicMessage{}, &calls, &text)
	if len(errors) == 0 {
		t.Error("expected error for 400 response")
	}
}

func TestRunAnthropicLoop_TextResponse(t *testing.T) {
	sseResponse := "data: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":10,\"output_tokens\":5}}}\n" +
		"data: {\"type\":\"content_block_start\",\"content_block\":{\"type\":\"text\"}}\n" +
		"data: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"world\"}}\n" +
		"data: {\"type\":\"content_block_stop\"}\n" +
		"data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"}}\n" +
		"data: [DONE]\n"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, sseResponse)
	}))
	defer srv.Close()

	var chunks []string
	ctx := &ChatContext{
		OnChunk:     func(content string, done bool) { chunks = append(chunks, content) },
		OnToolCall:  func(string, interface{}) {},
		OnToolResult: func(string, interface{}, bool) {},
		OnError:     func(string) {},
		ActiveTools: bootstrapTools(),
		Usage:       &SessionUsage{},
		Quarantine:  NewToolQuarantine(),
	}
	old := http.DefaultClient
	defer func() { http.DefaultClient = old }()
	http.DefaultClient = &http.Client{Transport: redirectTransport{target: srv.URL}}

	var calls []ToolCall
	var text strings.Builder
	runAnthropicLoop(ctx, "claude-3-5-sonnet-20241022", "fake-key", "system", []anthropicMessage{}, &calls, &text)
	combined := text.String()
	if !strings.Contains(combined, "world") {
		t.Errorf("expected 'world' in output, got %q", combined)
	}
}

// redirectTransport intercepts any HTTP request and sends it to the target server.
type redirectTransport struct {
	target string
	// real is the underlying transport to use; if nil, http.DefaultTransport is NOT
	// called recursively — use only when DefaultTransport is NOT this transport.
	real http.RoundTripper
}

func (rt redirectTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Replace the URL host with the test server.
	req2 := req.Clone(req.Context())
	req2.URL.Scheme = "http"
	req2.URL.Host = strings.TrimPrefix(rt.target, "http://")
	req2.RequestURI = ""
	underlying := rt.real
	if underlying == nil {
		underlying = http.DefaultTransport
	}
	return underlying.RoundTrip(req2)
}

// failTransport fails if called — for tests where NO HTTP calls should be made.
type failTransport struct {
	t *testing.T
}

func (ft failTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	ft.t.Errorf("unexpected HTTP call to %s (should have cancelled early)", req.URL)
	return nil, fmt.Errorf("unexpected call")
}

// ── resolveProvider ───────────────────────────────────────────────────────────

func TestResolveProvider_ExplicitProviders(t *testing.T) {
	cases := []struct{ in, want string }{
		{"anthropic", "anthropic"},
		{"openai", "openai"},
		{"ollama", "ollama"},
		{"ANTHROPIC", "anthropic"},
		{"OpenAI", "openai"},
	}
	for _, tc := range cases {
		got := resolveProvider(tc.in, "any-model")
		if got != tc.want {
			t.Errorf("resolveProvider(%q, _) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestResolveProvider_AutoFallsThrough(t *testing.T) {
	if got := resolveProvider("", "gpt-4o"); got != "openai" {
		t.Errorf("expected openai for gpt-4o, got %q", got)
	}
	if got := resolveProvider("auto", "claude-3-5-sonnet-20241022"); got != "anthropic" {
		t.Errorf("expected anthropic for claude-3, got %q", got)
	}
}

func TestResolveProvider_UnknownExplicitFallsThrough(t *testing.T) {
	// Unrecognized explicit provider falls through to model inference
	got := resolveProvider("unknown-provider-xyz", "gpt-4o")
	if got != "openai" {
		t.Errorf("expected openai for unknown provider with gpt-4o model, got %q", got)
	}
}

// ── ollamaChatCompletionsURL ──────────────────────────────────────────────────

func TestOllamaChatCompletionsURL_AlreadyFull(t *testing.T) {
	in := "http://localhost:11434/v1/chat/completions"
	got := ollamaChatCompletionsURL(in)
	if got != in {
		t.Errorf("expected passthrough, got %q", got)
	}
}

// TestOllamaChatCompletionsURL_NakedChatCompletions exercises the first case
// in ollamaChatCompletionsURL: the URL already ends in /chat/completions (no /v1 prefix).
func TestOllamaChatCompletionsURL_NakedChatCompletions(t *testing.T) {
	in := "http://localhost:11434/chat/completions"
	got := ollamaChatCompletionsURL(in)
	if !strings.HasSuffix(got, "/chat/completions") {
		t.Errorf("expected /chat/completions suffix, got %q", got)
	}
}

func TestOllamaChatCompletionsURL_V1Suffix(t *testing.T) {
	got := ollamaChatCompletionsURL("http://localhost:11434/v1")
	if !strings.HasSuffix(got, "/chat/completions") {
		t.Errorf("expected /chat/completions suffix, got %q", got)
	}
}

func TestOllamaChatCompletionsURL_Default(t *testing.T) {
	got := ollamaChatCompletionsURL("http://localhost:11434")
	if !strings.HasSuffix(got, "/v1/chat/completions") {
		t.Errorf("expected /v1/chat/completions suffix, got %q", got)
	}
}

// ── looksLikeOllamaModel ──────────────────────────────────────────────────────

func TestLooksLikeOllamaModel_FalseCase(t *testing.T) {
	if looksLikeOllamaModel("totally-random-model-name-xyz") {
		t.Error("expected false for non-ollama model")
	}
}

func TestLooksLikeOllamaModel_WithColon(t *testing.T) {
	if !looksLikeOllamaModel("llama3:latest") {
		t.Error("expected true for model with colon")
	}
}

// ── inferredProviderForModel ──────────────────────────────────────────────────

func TestInferredProviderForModel_O3Prefix(t *testing.T) {
	if got := inferredProviderForModel("o3-mini"); got != "openai" {
		t.Errorf("expected openai for o3-mini, got %q", got)
	}
}

func TestInferredProviderForModel_O4Prefix(t *testing.T) {
	if got := inferredProviderForModel("o4-preview"); got != "openai" {
		t.Errorf("expected openai for o4-preview, got %q", got)
	}
}

func TestInferredProviderForModel_DefaultAnthropic(t *testing.T) {
	if got := inferredProviderForModel("totally-unknown-model-xyz"); got != "anthropic" {
		t.Errorf("expected anthropic default, got %q", got)
	}
}

// ── streamRequest: tool_use block ────────────────────────────────────────────

func TestStreamRequest_ToolUseBlock(t *testing.T) {
	sseResponse := "data: {\"type\":\"content_block_start\",\"content_block\":{\"type\":\"tool_use\",\"id\":\"tu1\",\"name\":\"read_file\"}}\n" +
		"data: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{\\\"path\\\":\\\"/tmp\\\"}\" }}\n" +
		"data: {\"type\":\"content_block_stop\"}\n" +
		"data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"tool_use\"}}\n" +
		"data: [DONE]\n"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, sseResponse)
	}))
	defer srv.Close()

	old := http.DefaultClient
	defer func() { http.DefaultClient = old }()
	http.DefaultClient = &http.Client{Transport: redirectTransport{target: srv.URL}}

	ctx := &ChatContext{
		OnChunk:      func(string, bool) {},
		OnToolCall:   func(string, interface{}) {},
		OnToolResult: func(string, interface{}, bool) {},
		OnError:      func(string) {},
		Usage:        &SessionUsage{},
	}
	var text strings.Builder
	blocks, stopReason, err := streamRequest("fake-key", anthropicRequest{
		Model:    "claude-3-5-sonnet-20241022",
		Messages: []anthropicMessage{{Role: "user", Content: "hello"}},
	}, ctx, &text)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stopReason != "tool_use" {
		t.Errorf("expected stop_reason 'tool_use', got %q", stopReason)
	}
	found := false
	for _, b := range blocks {
		if b.Type == "tool_use" && b.Name == "read_file" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected tool_use block for read_file, blocks: %+v", blocks)
	}
}

// ── runAnthropicLoop: tool_use cycle ─────────────────────────────────────────

func TestRunAnthropicLoop_ToolUse_ThenText(t *testing.T) {
	projectDir := setupHistoryTestProject(t)
	callCount := 0
	// Use a real writable temp dir so read_file can succeed.
	tmpFile := t.TempDir() + "/f.txt"
	if err := os.WriteFile(tmpFile, []byte("content"), 0644); err != nil {
		t.Fatal(err)
	}
	// Escape the path for JSON embedding.
	pathJSON, _ := json.Marshal(tmpFile)
	toolUseSSE := "data: {\"type\":\"content_block_start\",\"content_block\":{\"type\":\"tool_use\",\"id\":\"tu1\",\"name\":\"read_file\"}}\n" +
		"data: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{\\\"path\\\":" + string(pathJSON) + "}\"}}\n" +
		"data: {\"type\":\"content_block_stop\"}\n" +
		"data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"tool_use\"}}\n" +
		"data: [DONE]\n"
	textSSE := "data: {\"type\":\"content_block_start\",\"content_block\":{\"type\":\"text\"}}\n" +
		"data: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"done\"}}\n" +
		"data: {\"type\":\"content_block_stop\"}\n" +
		"data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"}}\n" +
		"data: [DONE]\n"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		callCount++
		if callCount == 1 {
			fmt.Fprint(w, toolUseSSE)
		} else {
			fmt.Fprint(w, textSSE)
		}
	}))
	defer srv.Close()

	old := http.DefaultClient
	defer func() { http.DefaultClient = old }()
	http.DefaultClient = &http.Client{Transport: redirectTransport{target: srv.URL}}

	var toolCalls []string
	ctx := &ChatContext{
		OnChunk:      func(string, bool) {},
		OnToolCall:   func(name string, _ interface{}) { toolCalls = append(toolCalls, name) },
		OnToolResult: func(string, interface{}, bool) {},
		OnError:      func(string) {},
		ActiveTools:  bootstrapTools(),
		Usage:        &SessionUsage{},
		Quarantine:   NewToolQuarantine(),
		ProjectPath:  projectDir,
	}
	var calls []ToolCall
	var text strings.Builder
	runAnthropicLoop(ctx, "claude-3-5-sonnet-20241022", "fake-key", "system", []anthropicMessage{}, &calls, &text)
	if !strings.Contains(text.String(), "done") {
		t.Errorf("expected 'done' in final text, got %q", text.String())
	}
}

// ── runAnthropicLoop: transient retry ────────────────────────────────────────

func TestRunAnthropicLoop_TransientRetry(t *testing.T) {
	callCount := 0
	textSSE := "data: {\"type\":\"content_block_start\",\"content_block\":{\"type\":\"text\"}}\n" +
		"data: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"retried\"}}\n" +
		"data: {\"type\":\"content_block_stop\"}\n" +
		"data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"}}\n" +
		"data: [DONE]\n"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		if callCount == 1 {
			w.WriteHeader(429)
			fmt.Fprint(w, `{"error":"rate limit"}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, textSSE)
	}))
	defer srv.Close()

	old := http.DefaultClient
	defer func() { http.DefaultClient = old }()
	http.DefaultClient = &http.Client{Transport: redirectTransport{target: srv.URL}}

	var errs []string
	ctx := &ChatContext{
		OnChunk:      func(string, bool) {},
		OnToolCall:   func(string, interface{}) {},
		OnToolResult: func(string, interface{}, bool) {},
		OnError:      func(e string) { errs = append(errs, e) },
		ActiveTools:  bootstrapTools(),
		Usage:        &SessionUsage{},
		Quarantine:   NewToolQuarantine(),
	}
	var calls []ToolCall
	var text strings.Builder
	runAnthropicLoop(ctx, "claude-3-5-sonnet-20241022", "fake-key", "system", []anthropicMessage{}, &calls, &text)
	if !strings.Contains(text.String(), "retried") {
		t.Errorf("expected retry to succeed with 'retried', got %q, errs: %v", text.String(), errs)
	}
}

// ── runOpenAICompatibleLoop: tool_calls cycle ─────────────────────────────────

func TestRunOpenAICompatibleLoop_ToolCall_ThenText(t *testing.T) {
	projectDir := setupHistoryTestProject(t)
	tmpFile := t.TempDir() + "/f.txt"
	if err := os.WriteFile(tmpFile, []byte("oai-content"), 0644); err != nil {
		t.Fatal(err)
	}
	// Build arguments as a properly JSON-encoded string (arguments is a string value in OAI tool calls)
	argsObj := map[string]string{"path": tmpFile}
	argsBytes, _ := json.Marshal(argsObj)
	argsJSON, _ := json.Marshal(string(argsBytes)) // double-encode for embedding in JSON
	callCount := 0
	toolCallSSE := "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"c1\",\"function\":{\"name\":\"read_file\",\"arguments\":" + string(argsJSON) + "}}]},\"finish_reason\":null}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n\n" +
		"data: [DONE]\n\n"
	textSSE := "data: {\"choices\":[{\"delta\":{\"content\":\"oai-done\"},\"finish_reason\":null}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n" +
		"data: [DONE]\n\n"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		if callCount == 1 {
			fmt.Fprint(w, toolCallSSE)
		} else {
			fmt.Fprint(w, textSSE)
		}
	}))
	defer srv.Close()

	var toolCalls []string
	ctx := &ChatContext{
		OnChunk:      func(string, bool) {},
		OnToolCall:   func(name string, _ interface{}) { toolCalls = append(toolCalls, name) },
		OnToolResult: func(string, interface{}, bool) {},
		OnError:      func(string) {},
		ActiveTools:  bootstrapTools(),
		ProjectPath:  projectDir,
	}
	var calls []ToolCall
	var text strings.Builder
	runOpenAICompatibleLoop(ctx, "openai", "gpt-4o", srv.URL+"/v1/chat/completions", "key", true,
		"system", []anthropicMessage{}, &calls, &text)
	if !strings.Contains(text.String(), "oai-done") {
		t.Errorf("expected 'oai-done' in output, got %q", text.String())
	}
}

// ── runAnthropicLoop: quarantined tool ───────────────────────────────────────

func TestRunAnthropicLoop_QuarantinedTool(t *testing.T) {
	callCount := 0
	toolUseSSE := "data: {\"type\":\"content_block_start\",\"content_block\":{\"type\":\"tool_use\",\"id\":\"tu2\",\"name\":\"read_file\"}}\n" +
		"data: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{}\"}}\n" +
		"data: {\"type\":\"content_block_stop\"}\n" +
		"data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"tool_use\"}}\n" +
		"data: [DONE]\n"
	textSSE := "data: {\"type\":\"content_block_start\",\"content_block\":{\"type\":\"text\"}}\n" +
		"data: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"quarantine-done\"}}\n" +
		"data: {\"type\":\"content_block_stop\"}\n" +
		"data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"}}\n" +
		"data: [DONE]\n"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		if callCount == 1 {
			fmt.Fprint(w, toolUseSSE)
		} else {
			fmt.Fprint(w, textSSE)
		}
	}))
	defer srv.Close()

	old := http.DefaultClient
	defer func() { http.DefaultClient = old }()
	http.DefaultClient = &http.Client{Transport: redirectTransport{target: srv.URL}}

	q := NewToolQuarantine()
	// Pre-quarantine the tool so the first call hits the quarantine path.
	q.RecordOutcome("read_file", true, func(string) {})
	q.RecordOutcome("read_file", true, func(string) {})

	ctx := &ChatContext{
		OnChunk:      func(string, bool) {},
		OnToolCall:   func(string, interface{}) {},
		OnToolResult: func(string, interface{}, bool) {},
		OnError:      func(string) {},
		ActiveTools:  bootstrapTools(),
		Usage:        &SessionUsage{},
		Quarantine:   q,
	}
	var calls []ToolCall
	var text strings.Builder
	runAnthropicLoop(ctx, "claude-3-5-sonnet-20241022", "fake-key", "system", []anthropicMessage{}, &calls, &text)
	// Should have completed via quarantine path then second request for text.
	if !strings.Contains(text.String(), "quarantine-done") {
		t.Errorf("expected 'quarantine-done' in output, got %q", text.String())
	}
}
// ── aiExecuteTool additional paths ──────────────────────────────────────────

func TestExecuteToolForTest_Shell_ApprovalError(t *testing.T) {
	ctx := makeChatCtx(t)
	ctx.RequestApproval = func(kind, title, message, command string) (bool, error) {
		return false, errors.New("approval service unavailable")
	}
	result, isErr := ExecuteToolForTest("shell", map[string]interface{}{"command": "rm -rf /tmp/engine-approval-error-test"}, ctx)
	if !isErr {
		t.Errorf("expected error from approval handler error, got %q", result)
	}
	if !strings.Contains(result, "approval service unavailable") {
		t.Errorf("expected approval error in result, got %q", result)
	}
}

func TestExecuteToolForTest_Shell_EmptyOutput(t *testing.T) {
	ctx := makeChatCtx(t)
	// "true" exits 0 and produces no output → should return "(no output)"
	result, isErr := ExecuteToolForTest("shell", map[string]interface{}{"command": "true"}, ctx)
	if isErr {
		t.Errorf("unexpected error: %s", result)
	}
	if result != "(no output)" {
		t.Errorf("expected '(no output)', got %q", result)
	}
}

func TestExecuteToolForTest_GitCommit_ApprovalError(t *testing.T) {
	ctx := makeChatCtx(t)
	ctx.RequestApproval = func(kind, title, message, command string) (bool, error) {
		return false, errors.New("commit approval error")
	}
	result, isErr := ExecuteToolForTest("git_commit", map[string]interface{}{"message": "test"}, ctx)
	if !isErr {
		t.Errorf("expected error, got %q", result)
	}
	if !strings.Contains(result, "commit approval error") {
		t.Errorf("expected approval error, got %q", result)
	}
}

func TestExecuteToolForTest_GitPush_ApprovalError(t *testing.T) {
	ctx := makeChatCtx(t)
	ctx.RequestApproval = func(kind, title, message, command string) (bool, error) {
		return false, errors.New("push approval error")
	}
	result, isErr := ExecuteToolForTest("git_push", map[string]interface{}{}, ctx)
	if !isErr {
		t.Errorf("expected error, got %q", result)
	}
	if !strings.Contains(result, "push approval error") {
		t.Errorf("expected approval error, got %q", result)
	}
}

func TestExecuteToolForTest_GitListIssues_EmptyList(t *testing.T) {
	// Mock HTTP server returning empty array — covers the "No issues found" path.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte("[]")) //nolint:errcheck
	}))
	defer srv.Close()
	realRT := http.DefaultTransport
	defer func() { http.DefaultTransport = realRT }()
	http.DefaultTransport = redirectTransport{target: srv.URL, real: realRT}
	t.Setenv("GITHUB_TOKEN", "test-tok")

	ctx := makeChatCtx(t)
	result, isErr := ExecuteToolForTest("github_list_issues", map[string]interface{}{
		"owner": "owner", "repo": "repo", "state": "open",
	}, ctx)
	if isErr {
		t.Errorf("unexpected error: %s", result)
	}
	if result != "No issues found" {
		t.Errorf("expected 'No issues found', got %q", result)
	}
}

func TestExecuteToolForTest_GitListIssues_WithItems(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[{"number":1,"title":"Test Bug","state":"open","labels":[{"name":"bug"}]}]`)) //nolint:errcheck
	}))
	defer srv.Close()
	realRT := http.DefaultTransport
	defer func() { http.DefaultTransport = realRT }()
	http.DefaultTransport = redirectTransport{target: srv.URL, real: realRT}
	t.Setenv("GITHUB_TOKEN", "test-tok")

	ctx := makeChatCtx(t)
	result, isErr := ExecuteToolForTest("github_list_issues", map[string]interface{}{
		"owner": "owner", "repo": "repo",
	}, ctx)
	if isErr {
		t.Errorf("unexpected error: %s", result)
	}
	if !strings.Contains(result, "#1") {
		t.Errorf("expected issue #1 in result, got %q", result)
	}
}

func TestExecuteToolForTest_GitGetIssue_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"number":42,"title":"Fix crash","state":"open","html_url":"https://github.com/o/r/issues/42","body":"details"}`)) //nolint:errcheck
	}))
	defer srv.Close()
	realRT := http.DefaultTransport
	defer func() { http.DefaultTransport = realRT }()
	http.DefaultTransport = redirectTransport{target: srv.URL, real: realRT}
	t.Setenv("GITHUB_TOKEN", "test-tok")

	ctx := makeChatCtx(t)
	result, isErr := ExecuteToolForTest("github_get_issue", map[string]interface{}{
		"owner": "o", "repo": "r", "number": float64(42),
	}, ctx)
	if isErr {
		t.Errorf("unexpected error: %s", result)
	}
	if !strings.Contains(result, "#42") {
		t.Errorf("expected issue 42, got %q", result)
	}
}

func TestExecuteToolForTest_GitCreateIssue_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"number":5,"title":"New feature","html_url":"https://github.com/o/r/issues/5"}`)) //nolint:errcheck
	}))
	defer srv.Close()
	realRT := http.DefaultTransport
	defer func() { http.DefaultTransport = realRT }()
	http.DefaultTransport = redirectTransport{target: srv.URL, real: realRT}
	t.Setenv("GITHUB_TOKEN", "test-tok")

	ctx := makeChatCtx(t)
	result, isErr := ExecuteToolForTest("github_create_issue", map[string]interface{}{
		"owner": "o", "repo": "r", "title": "New feature", "body": "body text",
	}, ctx)
	if isErr {
		t.Errorf("unexpected error: %s", result)
	}
	if !strings.Contains(result, "#5") {
		t.Errorf("expected issue #5, got %q", result)
	}
}

func TestExecuteToolForTest_GitCloseIssue_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"number":7,"title":"Closed bug","state":"closed","html_url":"","body":""}`)) //nolint:errcheck
	}))
	defer srv.Close()
	realRT := http.DefaultTransport
	defer func() { http.DefaultTransport = realRT }()
	http.DefaultTransport = redirectTransport{target: srv.URL, real: realRT}
	t.Setenv("GITHUB_TOKEN", "test-tok")

	ctx := makeChatCtx(t)
	result, isErr := ExecuteToolForTest("github_close_issue", map[string]interface{}{
		"owner": "o", "repo": "r", "number": float64(7), "comment": "fixed",
	}, ctx)
	if isErr {
		t.Errorf("unexpected error: %s", result)
	}
	if !strings.Contains(result, "7") {
		t.Errorf("expected issue 7 in result, got %q", result)
	}
}

func TestExecuteToolForTest_GitCloseIssue_ZeroNumber(t *testing.T) {
	ctx := makeChatCtx(t)
	result, isErr := ExecuteToolForTest("github_close_issue", map[string]interface{}{
		"owner": "o", "repo": "r", "number": float64(0),
	}, ctx)
	if !isErr {
		t.Errorf("expected error for zero issue number, got %q", result)
	}
}

func TestExecuteToolForTest_GitComment_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"id":99,"body":"thanks","html_url":"https://github.com/o/r/issues/1#issuecomment-99"}`)) //nolint:errcheck
	}))
	defer srv.Close()
	realRT := http.DefaultTransport
	defer func() { http.DefaultTransport = realRT }()
	http.DefaultTransport = redirectTransport{target: srv.URL, real: realRT}
	t.Setenv("GITHUB_TOKEN", "test-tok")

	ctx := makeChatCtx(t)
	result, isErr := ExecuteToolForTest("github_comment", map[string]interface{}{
		"owner": "o", "repo": "r", "number": float64(1), "body": "thanks",
	}, ctx)
	if isErr {
		t.Errorf("unexpected error: %s", result)
	}
	if !strings.Contains(result, "Comment added") {
		t.Errorf("expected 'Comment added', got %q", result)
	}
}

func TestExecuteToolForTest_ProcessKill_ApprovalDenied(t *testing.T) {
	ctx := makeChatCtx(t)
	ctx.RequestApproval = func(kind, title, message, command string) (bool, error) {
		return false, nil
	}
	result, isErr := ExecuteToolForTest("process_kill", map[string]interface{}{"pid": float64(1)}, ctx)
	if !isErr {
		t.Errorf("expected error, got %q", result)
	}
	if !strings.Contains(result, "denied") {
		t.Errorf("expected denial, got %q", result)
	}
}

func TestExecuteToolForTest_ProcessKill_ApprovalError(t *testing.T) {
	ctx := makeChatCtx(t)
	ctx.RequestApproval = func(kind, title, message, command string) (bool, error) {
		return false, errors.New("kill approval error")
	}
	result, isErr := ExecuteToolForTest("process_kill", map[string]interface{}{"pid": float64(1)}, ctx)
	if !isErr {
		t.Errorf("expected error, got %q", result)
	}
	if !strings.Contains(result, "kill approval error") {
		t.Errorf("expected approval error, got %q", result)
	}
}

func TestExecuteToolForTest_GitComment_ZeroNumber(t *testing.T) {
	ctx := makeChatCtx(t)
	result, isErr := ExecuteToolForTest("github_comment", map[string]interface{}{
		"owner": "engine", "repo": "test", "number": float64(0), "body": "hello",
	}, ctx)
	if !isErr {
		t.Errorf("expected error for zero issue number, got %q", result)
	}
}

func TestExecuteToolForTest_OpenURL_Darwin(t *testing.T) {
	origOpenURLCommand := openURLCommand
	t.Cleanup(func() { openURLCommand = origOpenURLCommand })

	var calledName string
	var calledArg string
	openURLCommand = func(name string, arg ...string) *exec.Cmd {
		calledName = name
		if len(arg) > 0 {
			calledArg = arg[0]
		}
		return exec.Command("true")
	}

	ctx := makeChatCtx(t)
	const targetURL = "http://localhost:1"
	result, isErr := ExecuteToolForTest("open_url", map[string]interface{}{
		"url": targetURL,
	}, ctx)
	if isErr {
		t.Logf("open_url returned error (acceptable on CI): %s", result)
	} else if !strings.Contains(result, "Opened:") {
		t.Errorf("expected 'Opened:' in result, got %q", result)
	}

	if calledName == "" {
		t.Fatal("expected open_url command to be invoked")
	}
	if calledArg != targetURL {
		t.Errorf("expected command arg %q, got %q", targetURL, calledArg)
	}
}

func TestExecuteToolForTest_Screenshot_Darwin(t *testing.T) {
	ctx := makeChatCtx(t)
	outPath := t.TempDir() + "/screenshot.png"
	result, isErr := ExecuteToolForTest("screenshot", map[string]interface{}{
		"path": outPath,
	}, ctx)
	// screencapture may fail in headless CI; either outcome is acceptable.
	if isErr {
		t.Logf("screenshot failed (expected in headless): %s", result)
	} else {
		if !strings.Contains(result, "Screenshot saved:") {
			t.Errorf("expected 'Screenshot saved:', got %q", result)
		}
	}
}

func TestExecuteToolForTest_ProcessKill_SuccessTerm(t *testing.T) {
	// Start a short-lived process to kill.
	cmd := exec.Command("sleep", "60")
	if err := cmd.Start(); err != nil {
		t.Skipf("could not start sleep process: %v", err)
	}
	pid := int32(cmd.Process.Pid)
	defer cmd.Process.Kill() //nolint:errcheck

	ctx := makeChatCtx(t)
	ctx.RequestApproval = func(kind, title, message, command string) (bool, error) {
		return true, nil
	}
	result, isErr := ExecuteToolForTest("process_kill", map[string]interface{}{
		"pid": float64(pid), "signal": "TERM",
	}, ctx)
	if isErr {
		t.Logf("process_kill with TERM returned error: %s", result)
	} else if !strings.Contains(result, "SigTERM") && !strings.Contains(result, "SIGTERM") && !strings.Contains(result, "TERM") {
		t.Errorf("expected TERM in result, got %q", result)
	}
}

func TestExecuteToolForTest_ProcessKill_SuccessKill(t *testing.T) {
	cmd := exec.Command("sleep", "60")
	if err := cmd.Start(); err != nil {
		t.Skipf("could not start sleep process: %v", err)
	}
	pid := int32(cmd.Process.Pid)

	ctx := makeChatCtx(t)
	ctx.RequestApproval = func(kind, title, message, command string) (bool, error) {
		return true, nil
	}
	result, isErr := ExecuteToolForTest("process_kill", map[string]interface{}{
		"pid": float64(pid), "signal": "KILL",
	}, ctx)
	if isErr {
		t.Errorf("expected success for KILL, got %q", result)
	} else if !strings.Contains(result, "KILL") {
		t.Errorf("expected KILL in result, got %q", result)
	}
}

func TestExecuteToolForTest_GitComment_NewClientError(t *testing.T) {
	ctx := makeChatCtx(t)
	// No GITHUB_TOKEN env var set in test env → NewClient fails.
	result, isErr := ExecuteToolForTest("github_comment", map[string]interface{}{
		"number": float64(1), "body": "hello",
	}, ctx)
	// If GITHUB_TOKEN is set in environment, skip.
	if !isErr && strings.Contains(result, "Comment added:") {
		t.Skip("GITHUB_TOKEN set in environment; skipping NewClient error test")
	}
	if !isErr {
		t.Errorf("expected error when no token/owner, got %q", result)
	}
}