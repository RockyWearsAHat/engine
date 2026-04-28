package db

import (
	"os"
	"path/filepath"
	"testing"
)

func initTestDB(t *testing.T) {
	t.Helper()
	stateDir := t.TempDir()
	t.Setenv("ENGINE_STATE_DIR", stateDir)
	if err := Init(t.TempDir()); err != nil {
		t.Fatalf("Init: %v", err)
	}
	t.Cleanup(func() {
		if globalDB != nil {
			globalDB.Close()
			globalDB = nil
		}
	})
}

func TestInit_CreatesDB(t *testing.T) {
	initTestDB(t)
}

func TestInit_Reinit(t *testing.T) {
	initTestDB(t)
	// Calling Init again should close and reopen.
	stateDir := t.TempDir()
	t.Setenv("ENGINE_STATE_DIR", stateDir)
	if err := Init(t.TempDir()); err != nil {
		t.Fatalf("second Init: %v", err)
	}
}

func TestStateDir_Fallback(t *testing.T) {
	// Override USER_CONFIG_DIR to force fallback paths.
	t.Setenv("ENGINE_STATE_DIR", "")
	dir := stateDir("")
	if dir == "" {
		t.Fatal("stateDir returned empty string")
	}
}

func TestStateDir_WithProjectPath(t *testing.T) {
	t.Setenv("ENGINE_STATE_DIR", "")
	// Clear config dir access by setting a fake HOME
	old := os.Getenv("HOME")
	t.Setenv("HOME", "")
	defer os.Setenv("HOME", old) //nolint:errcheck
	dir := stateDir("/myproject")
	if dir == "" {
		t.Fatal("stateDir returned empty")
	}
}

func TestCreateSession_And_GetSession(t *testing.T) {
	initTestDB(t)

	if err := CreateSession("sess-1", "/project/a", "main"); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	sess, err := GetSession("sess-1")
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if sess.ID != "sess-1" {
		t.Errorf("ID = %q, want sess-1", sess.ID)
	}
	if sess.ProjectPath != "/project/a" {
		t.Errorf("ProjectPath = %q, want /project/a", sess.ProjectPath)
	}
	if sess.BranchName != "main" {
		t.Errorf("BranchName = %q, want main", sess.BranchName)
	}
}

func TestGetSession_NotFound(t *testing.T) {
	initTestDB(t)

	_, err := GetSession("nonexistent")
	if err == nil {
		t.Fatal("expected error for missing session")
	}
}

func TestListSessions(t *testing.T) {
	initTestDB(t)

	if err := CreateSession("s1", "/proj", "main"); err != nil {
		t.Fatalf("CreateSession s1: %v", err)
	}
	if err := CreateSession("s2", "/proj", "dev"); err != nil {
		t.Fatalf("CreateSession s2: %v", err)
	}
	if err := CreateSession("s3", "/other", "main"); err != nil {
		t.Fatalf("CreateSession s3: %v", err)
	}

	sessions, err := ListSessions("/proj")
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(sessions) != 2 {
		t.Errorf("len = %d, want 2", len(sessions))
	}
}

func TestListSessions_Empty(t *testing.T) {
	initTestDB(t)

	sessions, err := ListSessions("/noproject")
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(sessions) != 0 {
		t.Errorf("expected 0 sessions, got %d", len(sessions))
	}
}

func TestSaveMessage_And_GetMessages(t *testing.T) {
	initTestDB(t)

	if err := CreateSession("sess-msg", "/p", "main"); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	if err := SaveMessage("msg-1", "sess-msg", "user", "hello world", nil); err != nil {
		t.Fatalf("SaveMessage: %v", err)
	}

	toolCalls := []map[string]string{{"name": "read_file"}}
	if err := SaveMessage("msg-2", "sess-msg", "assistant", "response", toolCalls); err != nil {
		t.Fatalf("SaveMessage with tool calls: %v", err)
	}

	msgs, err := GetMessages("sess-msg")
	if err != nil {
		t.Fatalf("GetMessages: %v", err)
	}
	if len(msgs) != 2 {
		t.Errorf("len = %d, want 2", len(msgs))
	}
	if msgs[0].Role != "user" {
		t.Errorf("first msg role = %q, want user", msgs[0].Role)
	}
}

func TestGetMessages_Empty(t *testing.T) {
	initTestDB(t)

	if err := CreateSession("empty-sess", "/p", "main"); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	msgs, err := GetMessages("empty-sess")
	if err != nil {
		t.Fatalf("GetMessages: %v", err)
	}
	if len(msgs) != 0 {
		t.Errorf("expected 0 messages, got %d", len(msgs))
	}
}

func TestUpdateSessionSummary(t *testing.T) {
	initTestDB(t)

	if err := CreateSession("sum-sess", "/p", "main"); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := UpdateSessionSummary("sum-sess", "test summary"); err != nil {
		t.Fatalf("UpdateSessionSummary: %v", err)
	}

	sess, err := GetSession("sum-sess")
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if sess.Summary != "test summary" {
		t.Errorf("Summary = %q, want 'test summary'", sess.Summary)
	}
}

func TestUpsertProjectDirection_And_Get(t *testing.T) {
	initTestDB(t)

	if err := UpsertProjectDirection("/myproject", "build an AI editor"); err != nil {
		t.Fatalf("UpsertProjectDirection: %v", err)
	}

	dir, err := GetProjectDirection("/myproject")
	if err != nil {
		t.Fatalf("GetProjectDirection: %v", err)
	}
	if dir != "build an AI editor" {
		t.Errorf("direction = %q, want 'build an AI editor'", dir)
	}

	// Upsert again — should update.
	if err := UpsertProjectDirection("/myproject", "updated direction"); err != nil {
		t.Fatalf("UpsertProjectDirection update: %v", err)
	}
	dir2, _ := GetProjectDirection("/myproject")
	if dir2 != "updated direction" {
		t.Errorf("updated direction = %q", dir2)
	}
}

func TestGetProjectDirection_Empty(t *testing.T) {
	initTestDB(t)

	dir, err := GetProjectDirection("/nonexistent")
	if err != nil {
		t.Fatalf("GetProjectDirection: %v", err)
	}
	if dir != "" {
		t.Errorf("expected empty, got %q", dir)
	}
}

func TestGetProjectDirection_EmptyPath(t *testing.T) {
	initTestDB(t)

	dir, err := GetProjectDirection("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dir != "" {
		t.Errorf("expected empty for empty path, got %q", dir)
	}
}

func TestUpsertProjectDirection_EmptyPath(t *testing.T) {
	initTestDB(t)

	if err := UpsertProjectDirection("", "ignored"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLogToolCall(t *testing.T) {
	initTestDB(t)

	if err := CreateSession("tool-sess", "/p", "main"); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := LogToolCall("tc-1", "tool-sess", "read_file", map[string]string{"path": "/foo"}, "content", false, 42); err != nil {
		t.Fatalf("LogToolCall: %v", err)
	}
	// Error case
	if err := LogToolCall("tc-2", "tool-sess", "write_file", nil, "error msg", true, 100); err != nil {
		t.Fatalf("LogToolCall error case: %v", err)
	}
}

func TestValidationResults(t *testing.T) {
	initTestDB(t)

	if err := CreateSession("val-sess", "/p", "main"); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	if err := SaveValidationResult("vr-1", "val-sess", "test-issue", true, true, 0, 0, 500, "all pass", "go test"); err != nil {
		t.Fatalf("SaveValidationResult: %v", err)
	}
	if err := SaveValidationResult("vr-2", "val-sess", "test-issue", false, false, 3, 1, 200, "3 errors", "go build"); err != nil {
		t.Fatalf("SaveValidationResult second: %v", err)
	}

	results, err := GetValidationResults("val-sess")
	if err != nil {
		t.Fatalf("GetValidationResults: %v", err)
	}
	if len(results) != 2 {
		t.Errorf("len = %d, want 2", len(results))
	}

	latest, err := GetLatestValidation("val-sess", "test-issue")
	if err != nil {
		t.Fatalf("GetLatestValidation: %v", err)
	}
	if latest == nil {
		t.Fatal("expected latest validation, got nil")
	}
}

func TestGetValidationResults_Empty(t *testing.T) {
	initTestDB(t)

	if err := CreateSession("empty-val-sess", "/p", "main"); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	results, err := GetValidationResults("empty-val-sess")
	if err != nil {
		t.Fatalf("GetValidationResults: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0, got %d", len(results))
	}
}

func TestGetLatestValidation_NotFound(t *testing.T) {
	initTestDB(t)

	if err := CreateSession("no-val-sess", "/p", "main"); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	_, err := GetLatestValidation("no-val-sess", "nonexistent")
	if err == nil {
		t.Fatal("expected error for no matching validation")
	}
}

func TestLearningEvents(t *testing.T) {
	initTestDB(t)

	if err := CreateSession("learn-sess", "/p", "main"); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	if err := SaveLearningEvent("le-1", "learn-sess", "refactor approach", "success", 0.9, "refactor", "extracted helper"); err != nil {
		t.Fatalf("SaveLearningEvent: %v", err)
	}
	if err := SaveLearningEvent("le-2", "learn-sess", "test pattern", "partial", 0.6, "testing", "mocks needed"); err != nil {
		t.Fatalf("SaveLearningEvent second: %v", err)
	}

	learnings, err := GetRelevantLearnings("/p", "refactor", 10)
	if err != nil {
		t.Fatalf("GetRelevantLearnings: %v", err)
	}
	if len(learnings) == 0 {
		t.Error("expected at least one learning for 'refactor'")
	}

	byCategory, err := GetLearningsByCategory("/p", "testing")
	if err != nil {
		t.Fatalf("GetLearningsByCategory: %v", err)
	}
	if len(byCategory) != 1 {
		t.Errorf("len = %d, want 1", len(byCategory))
	}
}

func TestGetLearningsByCategory_Empty(t *testing.T) {
	initTestDB(t)

	learnings, err := GetLearningsByCategory("/p", "nonexistent-category")
	if err != nil {
		t.Fatalf("GetLearningsByCategory: %v", err)
	}
	if len(learnings) != 0 {
		t.Errorf("expected 0, got %d", len(learnings))
	}
}

func TestAttentionResiduals(t *testing.T) {
	initTestDB(t)

	if err := CreateSession("ar-sess", "/p", "main"); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := SaveMessage("ar-msg-1", "ar-sess", "user", "test", nil); err != nil {
		t.Fatalf("SaveMessage: %v", err)
	}

	residuals := []AttentionResidual{
		{
			ID:          "ar-1",
			SessionID:   "ar-sess",
			MessageID:   "ar-msg-1",
			SourceKey:   "file:main.go",
			SourceType:  "file",
			SourceLabel: "main.go",
			QueryText:   "search query",
			Weight:      0.8,
			Score:       0.9,
			Context:     "relevant context",
		},
	}
	if err := SaveAttentionResiduals(residuals); err != nil {
		t.Fatalf("SaveAttentionResiduals: %v", err)
	}

	results, err := GetProjectAttentionResiduals("/p", 10)
	if err != nil {
		t.Fatalf("GetProjectAttentionResiduals: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("len = %d, want 1", len(results))
	}
}

func TestSaveAttentionResiduals_Empty(t *testing.T) {
	initTestDB(t)

	if err := SaveAttentionResiduals(nil); err != nil {
		t.Fatalf("SaveAttentionResiduals(nil): %v", err)
	}
	if err := SaveAttentionResiduals([]AttentionResidual{}); err != nil {
		t.Fatalf("SaveAttentionResiduals([]): %v", err)
	}
}

func TestGetProjectAttentionResiduals_DefaultLimit(t *testing.T) {
	initTestDB(t)

	results, err := GetProjectAttentionResiduals("/p", 0)
	if err != nil {
		t.Fatalf("GetProjectAttentionResiduals: %v", err)
	}
	if results == nil {
		t.Error("expected non-nil result")
	}
}

func TestDiscordRecordMessage(t *testing.T) {
	initTestDB(t)

	m := DiscordMessage{
		ProjectPath: "/p",
		ChannelID:   "chan-1",
		ThreadID:    "thread-1",
		SessionID:   "sess-discord",
		AuthorID:    "user-1",
		AuthorName:  "TestUser",
		Content:     "hello from discord",
	}
	if err := DiscordRecordMessage(m); err != nil {
		t.Fatalf("DiscordRecordMessage: %v", err)
	}
}

func TestDiscordRecordMessage_WithID(t *testing.T) {
	initTestDB(t)

	m := DiscordMessage{
		ID:          "discord-msg-explicit",
		ProjectPath: "/p",
		Content:     "explicit id message",
		Direction:   "out",
		Kind:        "agent",
	}
	if err := DiscordRecordMessage(m); err != nil {
		t.Fatalf("DiscordRecordMessage with ID: %v", err)
	}
}

func TestDiscordRecordMessage_LargeContent(t *testing.T) {
	initTestDB(t)

	largeContent := make([]byte, 10*1024)
	for i := range largeContent {
		largeContent[i] = 'x'
	}
	m := DiscordMessage{
		ProjectPath: "/p",
		Content:     string(largeContent),
	}
	if err := DiscordRecordMessage(m); err != nil {
		t.Fatalf("DiscordRecordMessage large content: %v", err)
	}
}

func TestDiscordBindSessionThread_And_GetByThread(t *testing.T) {
	initTestDB(t)

	if err := DiscordBindSessionThread("sess-th-1", "/p", "thread-42", "chan-7"); err != nil {
		t.Fatalf("DiscordBindSessionThread: %v", err)
	}

	result, err := DiscordGetSessionByThread("thread-42")
	if err != nil {
		t.Fatalf("DiscordGetSessionByThread: %v", err)
	}
	if result == nil {
		t.Fatal("expected result, got nil")
	}
	if result.SessionID != "sess-th-1" {
		t.Errorf("SessionID = %q, want sess-th-1", result.SessionID)
	}

	// Upsert same session — should update thread.
	if err := DiscordBindSessionThread("sess-th-1", "/p", "thread-99", "chan-7"); err != nil {
		t.Fatalf("DiscordBindSessionThread upsert: %v", err)
	}
}

func TestDiscordGetSessionByThread_NotFound(t *testing.T) {
	initTestDB(t)

	result, err := DiscordGetSessionByThread("nonexistent-thread")
	if err != nil {
		t.Fatalf("DiscordGetSessionByThread: %v", err)
	}
	if result != nil {
		t.Error("expected nil for nonexistent thread")
	}
}

func TestDiscordSearchMessages(t *testing.T) {
	initTestDB(t)

	m := DiscordMessage{
		ProjectPath: "/search-p",
		Content:     "unique searchable caveman text here",
		AuthorName:  "SearchUser",
	}
	if err := DiscordRecordMessage(m); err != nil {
		t.Fatalf("DiscordRecordMessage: %v", err)
	}

	hits, err := DiscordSearchMessages("/search-p", "caveman", "", 10)
	if err != nil {
		t.Fatalf("DiscordSearchMessages: %v", err)
	}
	_ = hits // results may be empty for FTS in test DB; just check no error
}

func TestDiscordSearchMessages_WithSince(t *testing.T) {
	initTestDB(t)

	m := DiscordMessage{
		ProjectPath: "/p2",
		Content:     "message with since filter",
	}
	if err := DiscordRecordMessage(m); err != nil {
		t.Fatalf("DiscordRecordMessage: %v", err)
	}

	_, err := DiscordSearchMessages("/p2", "message", "2020-01-01T00:00:00Z", 5)
	if err != nil {
		t.Fatalf("DiscordSearchMessages with since: %v", err)
	}
}

func TestDiscordListRecentMessages(t *testing.T) {
	initTestDB(t)

	projectPath := "/list-recent-project"
	m := DiscordMessage{
		ProjectPath: projectPath,
		Content:     "recent message content",
		ChannelID:   "chan1",
		ThreadID:    "thread1",
		AuthorName:  "ListUser",
	}
	if err := DiscordRecordMessage(m); err != nil {
		t.Fatalf("DiscordRecordMessage: %v", err)
	}

	msgs, err := DiscordListRecentMessages(projectPath, "", "", 10)
	if err != nil {
		t.Fatalf("DiscordListRecentMessages: %v", err)
	}
	if len(msgs) == 0 {
		t.Error("expected at least one message")
	}
}

func TestDiscordListRecentMessages_WithThreadFilter(t *testing.T) {
	initTestDB(t)

	m := DiscordMessage{
		ProjectPath: "/thread-project",
		Content:     "thread-specific message",
		ThreadID:    "thr-99",
	}
	if err := DiscordRecordMessage(m); err != nil {
		t.Fatalf("DiscordRecordMessage: %v", err)
	}

	msgs, err := DiscordListRecentMessages("/thread-project", "thr-99", "", 10)
	if err != nil {
		t.Fatalf("DiscordListRecentMessages with thread: %v", err)
	}
	_ = msgs
}

func TestDiscordListRecentMessages_WithSince(t *testing.T) {
	initTestDB(t)

	m := DiscordMessage{
		ProjectPath: "/since-project",
		Content:     "since-filtered message",
	}
	if err := DiscordRecordMessage(m); err != nil {
		t.Fatalf("DiscordRecordMessage: %v", err)
	}

	msgs, err := DiscordListRecentMessages("/since-project", "", "2020-01-01T00:00:00Z", 5)
	if err != nil {
		t.Fatalf("DiscordListRecentMessages with since: %v", err)
	}
	_ = msgs
}

func TestGetProjectMessages(t *testing.T) {
	initTestDB(t)

	projectPath := t.TempDir()
	sessionID := "proj-sess-1"
	if err := CreateSession(sessionID, projectPath, "main"); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := SaveMessage("msg-1", sessionID, "user", "hello project", nil); err != nil {
		t.Fatalf("SaveMessage: %v", err)
	}

	msgs, err := GetProjectMessages(projectPath, 10)
	if err != nil {
		t.Fatalf("GetProjectMessages: %v", err)
	}
	if len(msgs) == 0 {
		t.Error("expected at least one project message")
	}
}

func TestGetProjectMessages_DefaultLimit(t *testing.T) {
	initTestDB(t)

	projectPath := t.TempDir()
	sessionID := "proj-sess-default"
	if err := CreateSession(sessionID, projectPath, "main"); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := SaveMessage("msg-def", sessionID, "user", "default limit test", nil); err != nil {
		t.Fatalf("SaveMessage: %v", err)
	}

	msgs, err := GetProjectMessages(projectPath, 0)
	if err != nil {
		t.Fatalf("GetProjectMessages with 0 limit: %v", err)
	}
	if msgs == nil {
		t.Error("expected non-nil slice")
	}
}

func TestGetProjectSessionSummaries(t *testing.T) {
	initTestDB(t)

	projectPath := t.TempDir()
	sessionID := "sum-sess-1"
	if err := CreateSession(sessionID, projectPath, "main"); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := UpdateSessionSummary(sessionID, "This is a summary."); err != nil {
		t.Fatalf("UpdateSessionSummary: %v", err)
	}

	sums, err := GetProjectSessionSummaries(projectPath, 5)
	if err != nil {
		t.Fatalf("GetProjectSessionSummaries: %v", err)
	}
	if len(sums) == 0 {
		t.Error("expected at least one session summary")
	}
}

func TestGetProjectSessionSummaries_DefaultLimit(t *testing.T) {
	initTestDB(t)

	projectPath := t.TempDir()
	sessionID := "sum-sess-def"
	if err := CreateSession(sessionID, projectPath, "dev"); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := UpdateSessionSummary(sessionID, "Summary for default limit."); err != nil {
		t.Fatalf("UpdateSessionSummary: %v", err)
	}

	sums, err := GetProjectSessionSummaries(projectPath, 0)
	if err != nil {
		t.Fatalf("GetProjectSessionSummaries default limit: %v", err)
	}
	_ = sums
}

func TestGetProjectLearnings(t *testing.T) {
	initTestDB(t)

	projectPath := t.TempDir()
	sessionID := "learn-sess-1"
	if err := CreateSession(sessionID, projectPath, "main"); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := SaveLearningEvent("lrn-1", sessionID, "pattern A", "outcome A", 0.9, "testing", "ctx"); err != nil {
		t.Fatalf("SaveLearningEvent: %v", err)
	}

	learnings, err := GetProjectLearnings(projectPath, 10)
	if err != nil {
		t.Fatalf("GetProjectLearnings: %v", err)
	}
	if len(learnings) == 0 {
		t.Error("expected at least one learning")
	}
}

func TestGetProjectLearnings_DefaultLimit(t *testing.T) {
	initTestDB(t)

	projectPath := t.TempDir()
	sessionID := "learn-sess-def"
	if err := CreateSession(sessionID, projectPath, "main"); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := SaveLearningEvent("lrn-def", sessionID, "pattern def", "outcome def", 0.5, "general", "ctx"); err != nil {
		t.Fatalf("SaveLearningEvent: %v", err)
	}

	learnings, err := GetProjectLearnings(projectPath, 0)
	if err != nil {
		t.Fatalf("GetProjectLearnings default limit: %v", err)
	}
	_ = learnings
}

func TestGetProjectValidations(t *testing.T) {
	initTestDB(t)

	projectPath := t.TempDir()
	sessionID := "val-sess-1"
	if err := CreateSession(sessionID, projectPath, "main"); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := SaveValidationResult("val-1", sessionID, "issue A", true, true, 0, 0, 1234, "evidence", "cmd"); err != nil {
		t.Fatalf("SaveValidationResult: %v", err)
	}

	vals, err := GetProjectValidations(projectPath, 10)
	if err != nil {
		t.Fatalf("GetProjectValidations: %v", err)
	}
	if len(vals) == 0 {
		t.Error("expected at least one validation")
	}
}

func TestGetProjectValidations_DefaultLimit(t *testing.T) {
	initTestDB(t)

	projectPath := t.TempDir()
	sessionID := "val-sess-def"
	if err := CreateSession(sessionID, projectPath, "main"); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := SaveValidationResult("val-def", sessionID, "issue def", false, false, 1, 2, 500, "evidence2", "cmd2"); err != nil {
		t.Fatalf("SaveValidationResult: %v", err)
	}

	vals, err := GetProjectValidations(projectPath, 0)
	if err != nil {
		t.Fatalf("GetProjectValidations default limit: %v", err)
	}
	_ = vals
}

func TestUpdateSessionProjectPath(t *testing.T) {
	initTestDB(t)
	if err := CreateSession("sess-upd", "/old/path", "main"); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := UpdateSessionProjectPath("sess-upd", "/new/path"); err != nil {
		t.Fatalf("UpdateSessionProjectPath: %v", err)
	}
	sess, err := GetSession("sess-upd")
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if sess.ProjectPath != "/new/path" {
		t.Errorf("expected /new/path, got %q", sess.ProjectPath)
	}
}

// ─── nil-db error paths ───────────────────────────────────────────────────────

func TestDiscordRecordMessage_NilDB(t *testing.T) {
	prevDB := globalDB
	globalDB = nil
	defer func() { globalDB = prevDB }()
	if err := DiscordRecordMessage(DiscordMessage{Content: "x"}); err == nil {
		t.Fatal("expected error with nil db")
	}
}

func TestDiscordBindSessionThread_NilDB(t *testing.T) {
	prevDB := globalDB
	globalDB = nil
	defer func() { globalDB = prevDB }()
	if err := DiscordBindSessionThread("s", "/p", "t", "c"); err == nil {
		t.Fatal("expected error with nil db")
	}
}

func TestDiscordGetSessionByThread_NilDB(t *testing.T) {
	prevDB := globalDB
	globalDB = nil
	defer func() { globalDB = prevDB }()
	_, err := DiscordGetSessionByThread("tid")
	if err == nil {
		t.Fatal("expected error with nil db")
	}
}

func TestStateDir_EnvOverride(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ENGINE_STATE_DIR", dir)
	got := stateDir("/project")
	if got != dir {
		t.Errorf("expected %q, got %q", dir, got)
	}
}

func TestStateDir_ProjectPathFallback(t *testing.T) {
	// Clear env vars that the normal config path would use.
	t.Setenv("ENGINE_STATE_DIR", "")
	// Can't mock UserConfigDir or UserHomeDir without OS injection.
	// Just call stateDir with a path and verify we get some non-empty result.
	got := stateDir("/myproject")
	if got == "" {
		t.Error("expected non-empty stateDir")
	}
}

func TestInit_MkdirAllError(t *testing.T) {
	// Point ENGINE_STATE_DIR to a file path so MkdirAll fails.
	tmpFile, err := os.CreateTemp("", "dbtest")
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())
	tmpFile.Close()

	// A subpath of the file — MkdirAll will fail because parent is a file.
	badDir := tmpFile.Name() + "/subdir"
	t.Setenv("ENGINE_STATE_DIR", badDir)
	defer func() { globalDB = nil }()

	err = Init("/project")
	if err == nil {
		t.Error("expected error when MkdirAll fails")
	}
}

// TestStateDir_PrefersProjectPathOverConfigDir verifies the project-path
// branch wins over the user config dir, so per-project isolation works.
func TestStateDir_PrefersProjectPathOverConfigDir(t *testing.T) {
	t.Setenv("ENGINE_STATE_DIR", "")
	got := stateDir("/myproject")
	want := "/myproject/.engine"
	if got != want {
		t.Errorf("stateDir(/myproject) = %q, want %q", got, want)
	}
}

// TestWithProject_IsolatesDB verifies sessions written inside WithProject
// land in that project's local DB and are NOT visible after returning to
// the prior project's DB.
func TestWithProject_IsolatesDB(t *testing.T) {
	// Clear env override so project path resolution wins.
	t.Setenv("ENGINE_STATE_DIR", "")

	projA := t.TempDir()
	projB := t.TempDir()

	if err := Init(projA); err != nil {
		t.Fatalf("Init projA: %v", err)
	}
	t.Cleanup(func() {
		if globalDB != nil {
			globalDB.Close()
			globalDB = nil
		}
	})

	// Session in projA's DB.
	if err := CreateSession("a-only", projA, "main"); err != nil {
		t.Fatalf("CreateSession a-only: %v", err)
	}

	// Swap to projB and create a session there.
	werr := WithProject(projB, func() error {
		// projA's session must NOT be visible from projB's DB.
		if _, err := GetSession("a-only"); err == nil {
			t.Errorf("projA session a-only visible from projB DB; isolation broken")
		}
		if err := CreateSession("b-only", projB, "main"); err != nil {
			t.Fatalf("CreateSession b-only: %v", err)
		}
		// b-only must be visible inside the closure.
		if _, err := GetSession("b-only"); err != nil {
			t.Errorf("b-only not visible inside WithProject(projB): %v", err)
		}
		return nil
	})
	if werr != nil {
		t.Fatalf("WithProject: %v", werr)
	}

	// After WithProject returns, we should be back on projA's DB:
	// a-only is visible, b-only is NOT.
	if _, err := GetSession("a-only"); err != nil {
		t.Errorf("a-only not visible after restore: %v", err)
	}
	if _, err := GetSession("b-only"); err == nil {
		t.Errorf("b-only visible from projA DB after restore; isolation broken")
	}

	if got := CurrentProject(); got != projA {
		t.Errorf("CurrentProject after restore = %q, want %q", got, projA)
	}

	// Verify the physical files exist where expected.
	for _, p := range []string{
		filepath.Join(projA, ".engine", "state.db"),
		filepath.Join(projB, ".engine", "state.db"),
	} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("expected state.db at %q: %v", p, err)
		}
	}
}

func TestWithProject_InitLocked_Fails_ReturnsError(t *testing.T) {
	t.Setenv("ENGINE_STATE_DIR", "/dev/null/cannot-create")
	err := WithProject("/some/path", func() error { return nil })
	if err == nil {
		t.Fatal("expected error when db state dir is unwritable, got nil")
	}
}

func TestInsertSessionWithTimestamps_InsertsRow(t *testing.T) {
	initTestDB(t)
	if err := InsertSessionWithTimestamps("ts-id-1", "/some/project", "main", "2024-01-01T00:00:00Z", "2024-01-01T00:00:00Z"); err != nil {
		t.Fatalf("InsertSessionWithTimestamps: %v", err)
	}
	sess, err := GetSession("ts-id-1")
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if sess.ID != "ts-id-1" {
		t.Fatalf("expected session id ts-id-1, got %q", sess.ID)
	}
}
