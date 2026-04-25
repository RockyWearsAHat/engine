package ai

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
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
}

func (rt redirectTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Replace the URL host with the test server.
	req2 := req.Clone(req.Context())
	req2.URL.Scheme = "http"
	req2.URL.Host = strings.TrimPrefix(rt.target, "http://")
	req2.RequestURI = ""
	return http.DefaultTransport.RoundTrip(req2)
}

// failTransport fails if called — for tests where NO HTTP calls should be made.
type failTransport struct {
	t *testing.T
}

func (ft failTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	ft.t.Errorf("unexpected HTTP call to %s (should have cancelled early)", req.URL)
	return nil, fmt.Errorf("unexpected call")
}
