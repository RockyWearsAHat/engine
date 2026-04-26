package db

import (
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"strings"
	"testing"
)

type errCloseDriver struct{}

type errCloseConn struct{}

type noopStmt struct{}

type noopTx struct{}

func (errCloseDriver) Open(string) (driver.Conn, error) { return errCloseConn{}, nil }

func (errCloseConn) Prepare(string) (driver.Stmt, error) { return noopStmt{}, nil }

func (errCloseConn) Close() error { return errors.New("injected close error") }

func (errCloseConn) Begin() (driver.Tx, error) { return noopTx{}, nil }

func (noopStmt) Close() error { return nil }

func (noopStmt) NumInput() int { return -1 }

func (noopStmt) Exec([]driver.Value) (driver.Result, error) { return nil, nil }

func (noopStmt) Query([]driver.Value) (driver.Rows, error) { return nil, nil }

func (noopTx) Commit() error { return nil }

func (noopTx) Rollback() error { return nil }

func openRawDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func setGlobalDBForTest(t *testing.T, testDB *sql.DB) {
	t.Helper()
	orig := globalDB
	globalDB = testDB
	t.Cleanup(func() { globalDB = orig })
}

func mustExec(t *testing.T, dbh *sql.DB, q string) {
	t.Helper()
	if _, err := dbh.Exec(q); err != nil {
		t.Fatalf("exec failed: %v\nquery: %s", err, q)
	}
}

func TestStateDir_InjectableFallbacks(t *testing.T) {
	origCfg := osUserConfigDirFn
	origHome := osUserHomeDirFn
	defer func() {
		osUserConfigDirFn = origCfg
		osUserHomeDirFn = origHome
	}()

	osUserConfigDirFn = func() (string, error) { return "", errors.New("cfg fail") }
	osUserHomeDirFn = func() (string, error) { return "/tmp/home", nil }
	if got := stateDir(""); got != "/tmp/home/.engine" {
		t.Fatalf("stateDir home fallback = %q", got)
	}

	osUserHomeDirFn = func() (string, error) { return "", errors.New("home fail") }
	if got := stateDir(""); got != ".engine" && got != "./.engine" {
		t.Fatalf("stateDir dot fallback = %q", got)
	}
}

func TestInit_PreviousHandleBestEffortClose(t *testing.T) {
	t.Setenv("ENGINE_STATE_DIR", t.TempDir())
	driverName := fmt.Sprintf("errclose_%s", strings.ReplaceAll(t.Name(), "/", "_"))
	sql.Register(driverName, errCloseDriver{})
	db, err := sql.Open(driverName, "")
	if err != nil {
		t.Fatalf("sql.Open custom driver: %v", err)
	}
	orig := globalDB
	globalDB = db
	defer func() { globalDB = orig }()

	err = Init(t.TempDir())
	if err != nil {
		t.Fatalf("expected Init to continue after prior-handle close issue, got %v", err)
	}
}

func TestInit_SqlOpenError(t *testing.T) {
	t.Setenv("ENGINE_STATE_DIR", t.TempDir())
	origOpen := sqlOpenFn
	sqlOpenFn = func(_, _ string) (*sql.DB, error) { return nil, errors.New("open fail") }
	defer func() { sqlOpenFn = origOpen }()

	err := Init(t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "open db") {
		t.Fatalf("expected open db error, got %v", err)
	}
}

func TestInit_MkdirError(t *testing.T) {
	t.Setenv("ENGINE_STATE_DIR", "/dev/null/engine-state")
	err := Init(t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "create engine state dir") {
		t.Fatalf("expected mkdir error, got %v", err)
	}
}

func TestListSessions_QueryAndScanErrors(t *testing.T) {
	initTestDB(t)
	_ = globalDB.Close()
	if _, err := ListSessions("/p"); err == nil {
		t.Fatal("expected query error on closed db")
	}

	raw := openRawDB(t)
	setGlobalDBForTest(t, raw)
	mustExec(t, raw, `CREATE TABLE sessions (id TEXT, project_path TEXT, branch_name TEXT, summary TEXT, created_at TEXT, updated_at TEXT)`)
	mustExec(t, raw, `CREATE TABLE messages (id TEXT, session_id TEXT)`)
	mustExec(t, raw, `INSERT INTO sessions (id, project_path, branch_name, summary, created_at, updated_at) VALUES (NULL, '/p', 'b', 's', 'c', 'u')`)
	if _, err := ListSessions("/p"); err == nil {
		t.Fatal("expected scan error for NULL session id")
	}
}

func TestSaveAndGetMessages_ErrorPaths(t *testing.T) {
	raw := openRawDB(t)
	setGlobalDBForTest(t, raw)

	if err := SaveMessage("m1", "s1", "user", "hi", nil); err == nil {
		t.Fatal("expected insert error without messages table")
	}

	mustExec(t, raw, `CREATE TABLE messages (id TEXT, session_id TEXT, role TEXT, content TEXT, tool_calls TEXT, created_at TEXT)`)
	mustExec(t, raw, `INSERT INTO messages (id, session_id, role, content, tool_calls, created_at) VALUES (NULL, 's1', 'u', 'c', NULL, 't')`)
	if _, err := GetMessages("s1"); err == nil {
		t.Fatal("expected scan error for NULL message id")
	}

	_ = raw.Close()
	if _, err := GetMessages("s1"); err == nil {
		t.Fatal("expected query error on closed db")
	}
}

func TestValidationAndLearning_ErrorPaths(t *testing.T) {
	raw := openRawDB(t)
	setGlobalDBForTest(t, raw)

	mustExec(t, raw, `CREATE TABLE validation_results (id TEXT, session_id TEXT, issue TEXT, issue_resolved INTEGER, test_passed INTEGER, error_count INTEGER, warning_count INTEGER, duration_ms INTEGER, evidence TEXT, command TEXT, created_at TEXT)`)
	mustExec(t, raw, `INSERT INTO validation_results (id, session_id, issue, issue_resolved, test_passed, error_count, warning_count, duration_ms, evidence, command, created_at) VALUES (NULL, 's1', 'iss', 1, 1, 0, 0, 0, 'e', 'c', 't')`)
	if _, err := GetValidationResults("s1"); err == nil {
		t.Fatal("expected scan error for NULL validation id")
	}

	mustExec(t, raw, `CREATE TABLE sessions (id TEXT, project_path TEXT, branch_name TEXT, summary TEXT, created_at TEXT, updated_at TEXT)`)
	mustExec(t, raw, `INSERT INTO sessions (id, project_path, branch_name, summary, created_at, updated_at) VALUES ('s1', '/p', 'main', '', 't', 't')`)
	mustExec(t, raw, `CREATE TABLE learning_events (id TEXT, session_id TEXT, pattern TEXT, outcome TEXT, confidence REAL, category TEXT, context TEXT, created_at TEXT)`)
	mustExec(t, raw, `INSERT INTO learning_events (id, session_id, pattern, outcome, confidence, category, context, created_at) VALUES (NULL, 's1', 'p', 'o', 0.9, 'cat', 'ctx', 't')`)
	if _, err := GetRelevantLearnings("/p", "p", 10); err == nil {
		t.Fatal("expected scan error for NULL learning id")
	}
	if _, err := GetLearningsByCategory("/p", "cat"); err == nil {
		t.Fatal("expected scan error for NULL learning id by category")
	}

	_ = raw.Close()
	if _, err := GetValidationResults("s1"); err == nil {
		t.Fatal("expected query error for validations on closed db")
	}
	if _, err := GetRelevantLearnings("/p", "p", 10); err == nil {
		t.Fatal("expected query error for relevant learnings on closed db")
	}
	if _, err := GetLearningsByCategory("/p", "cat"); err == nil {
		t.Fatal("expected query error for category learnings on closed db")
	}
}

func TestProjectHistory_ErrorPaths(t *testing.T) {
	raw := openRawDB(t)
	setGlobalDBForTest(t, raw)

	mustExec(t, raw, `CREATE TABLE sessions (id TEXT, project_path TEXT, branch_name TEXT, summary TEXT, created_at TEXT, updated_at TEXT)`)
	mustExec(t, raw, `CREATE TABLE messages (id TEXT, session_id TEXT, role TEXT, content TEXT, created_at TEXT)`)
	mustExec(t, raw, `CREATE TABLE learning_events (id TEXT, session_id TEXT, pattern TEXT, outcome TEXT, confidence REAL, category TEXT, context TEXT, created_at TEXT)`)
	mustExec(t, raw, `CREATE TABLE validation_results (id TEXT, session_id TEXT, issue TEXT, issue_resolved INTEGER, test_passed INTEGER, error_count INTEGER, warning_count INTEGER, duration_ms INTEGER, evidence TEXT, command TEXT, created_at TEXT)`)

	mustExec(t, raw, `INSERT INTO sessions (id, project_path, branch_name, summary, created_at, updated_at) VALUES ('s1', '/p', 'b1', 'sum', 'c', 'u')`)
	mustExec(t, raw, `INSERT INTO messages (id, session_id, role, content, created_at) VALUES (NULL, 's1', 'user', 'msg', 't')`)
	if _, err := GetProjectMessages("/p", 10); err == nil {
		t.Fatal("expected scan error for project messages")
	}

	mustExec(t, raw, `DELETE FROM messages`)
	mustExec(t, raw, `UPDATE sessions SET id=NULL WHERE project_path='/p'`)
	if _, err := GetProjectSessionSummaries("/p", 10); err == nil {
		t.Fatal("expected scan error for session summaries")
	}

	mustExec(t, raw, `DELETE FROM sessions`)
	mustExec(t, raw, `INSERT INTO sessions (id, project_path, branch_name, summary, created_at, updated_at) VALUES ('s2', '/p', 'b2', 'sum2', 'c', 'u')`)
	mustExec(t, raw, `INSERT INTO learning_events (id, session_id, pattern, outcome, confidence, category, context, created_at) VALUES (NULL, 's2', 'p', 'o', 1.0, 'cat', 'ctx', 't')`)
	if _, err := GetProjectLearnings("/p", 10); err == nil {
		t.Fatal("expected scan error for project learnings")
	}

	mustExec(t, raw, `INSERT INTO validation_results (id, session_id, issue, issue_resolved, test_passed, error_count, warning_count, duration_ms, evidence, command, created_at) VALUES (NULL, 's2', 'iss', 1, 1, 0, 0, 0, 'e', 'c', 't')`)
	if _, err := GetProjectValidations("/p", 10); err == nil {
		t.Fatal("expected scan error for project validations")
	}

	_ = raw.Close()
	if _, err := GetProjectMessages("/p", 10); err == nil {
		t.Fatal("expected query error for project messages on closed db")
	}
	if _, err := GetProjectSessionSummaries("/p", 10); err == nil {
		t.Fatal("expected query error for session summaries on closed db")
	}
	if _, err := GetProjectLearnings("/p", 10); err == nil {
		t.Fatal("expected query error for project learnings on closed db")
	}
	if _, err := GetProjectValidations("/p", 10); err == nil {
		t.Fatal("expected query error for project validations on closed db")
	}
}

func TestProjectDirection_QueryError(t *testing.T) {
	raw := openRawDB(t)
	setGlobalDBForTest(t, raw)
	_ = raw.Close()
	if _, err := GetProjectDirection("/p"); err == nil {
		t.Fatal("expected query error on closed db")
	}
}

func TestDiscordDB_ErrorPaths(t *testing.T) {
	raw := openRawDB(t)
	setGlobalDBForTest(t, raw)

	mustExec(t, raw, `CREATE TABLE discord_session_threads (session_id TEXT, project_path TEXT, thread_id TEXT, channel_id TEXT, created_at TEXT)`)
	mustExec(t, raw, `INSERT INTO discord_session_threads (session_id, project_path, thread_id, channel_id, created_at) VALUES (NULL, '/p', 't1', 'c1', 'now')`)
	if _, err := DiscordGetSessionByThread("t1"); err == nil {
		t.Fatal("expected scan error for discord session thread")
	}

	mustExec(t, raw, `CREATE TABLE discord_messages (id TEXT, project_path TEXT, channel_id TEXT, thread_id TEXT, session_id TEXT, author_id TEXT, author_name TEXT, direction TEXT, kind TEXT, content TEXT, created_at TEXT)`)
	mustExec(t, raw, `CREATE VIRTUAL TABLE discord_messages_fts USING fts5(content, author_name)`)
	mustExec(t, raw, `INSERT INTO discord_messages (id, project_path, channel_id, thread_id, session_id, author_id, author_name, direction, kind, content, created_at) VALUES (NULL, '/p', 'c', 't', 's', 'a', 'n', 'in', 'message', 'hello', 'now')`)
	mustExec(t, raw, `INSERT INTO discord_messages_fts (rowid, content, author_name) VALUES (1, 'hello', 'n')`)

	if _, err := DiscordSearchMessages("/p", "hello", "", 10); err == nil {
		t.Fatal("expected scan error for discord search")
	}
	if _, err := DiscordListRecentMessages("/p", "", "", 10); err == nil {
		t.Fatal("expected scan error for discord recent list")
	}

	_ = raw.Close()
	if _, err := DiscordSearchMessages("/p", "hello", "", 10); err == nil {
		t.Fatal("expected query error for discord search on closed db")
	}
	if _, err := DiscordListRecentMessages("/p", "", "", 10); err == nil {
		t.Fatal("expected query error for discord list on closed db")
	}
}

func TestAttentionResiduals_ErrorPaths(t *testing.T) {
	raw := openRawDB(t)
	setGlobalDBForTest(t, raw)
	_ = raw.Close()
	if err := SaveAttentionResiduals([]AttentionResidual{{ID: "1"}}); err == nil {
		t.Fatal("expected begin error on closed db")
	}
	if _, err := GetProjectAttentionResiduals("/p", 10); err == nil {
		t.Fatal("expected query error on closed db")
	}

	raw2 := openRawDB(t)
	setGlobalDBForTest(t, raw2)
	if err := SaveAttentionResiduals([]AttentionResidual{{ID: "1"}}); err == nil {
		t.Fatal("expected prepare error without table")
	}

	mustExec(t, raw2, `CREATE TABLE attention_residuals (id TEXT PRIMARY KEY, session_id TEXT, message_id TEXT, source_key TEXT, source_type TEXT, source_label TEXT, query_text TEXT, weight REAL, score REAL, context TEXT, created_at TEXT)`)
	mustExec(t, raw2, `CREATE TABLE sessions (id TEXT, project_path TEXT, branch_name TEXT)`)
	mustExec(t, raw2, `INSERT INTO attention_residuals (id, session_id, message_id, source_key, source_type, source_label, query_text, weight, score, context, created_at) VALUES (NULL, 's1', 'm1', 'k', 't', 'l', 'q', 1, 1, 'c', 'now')`)
	mustExec(t, raw2, `INSERT INTO sessions (id, project_path, branch_name) VALUES ('s1', '/p', 'b1')`)

	if _, err := GetProjectAttentionResiduals("/p", 10); err == nil {
		t.Fatal("expected scan error for attention residuals")
	}

	dup := []AttentionResidual{
		{ID: "dup", SessionID: "s1", MessageID: "m1", SourceKey: "k", SourceType: "t", SourceLabel: "l", QueryText: "q", Weight: 1, Score: 1, Context: "c"},
		{ID: "dup", SessionID: "s1", MessageID: "m1", SourceKey: "k", SourceType: "t", SourceLabel: "l", QueryText: "q", Weight: 1, Score: 1, Context: "c"},
	}
	if err := SaveAttentionResiduals(dup); err == nil {
		t.Fatal("expected exec error on duplicate id")
	}
}

func TestGetRelevantLearnings_EmptySlice(t *testing.T) {
	initTestDB(t)
	out, err := GetRelevantLearnings("/p", "no-match", 10)
	if err != nil {
		t.Fatalf("GetRelevantLearnings: %v", err)
	}
	if out == nil || len(out) != 0 {
		t.Fatalf("expected empty non-nil slice, got %#v", out)
	}
}

func TestDiscordSearchMessages_NilDBAndEmptyCases(t *testing.T) {
	setGlobalDBForTest(t, nil)
	if _, err := DiscordSearchMessages("/p", "hello", "", 10); err == nil {
		t.Fatal("expected db not initialized error")
	}

	initTestDB(t)
	if _, err := DiscordSearchMessages("/p", "   ", "", 10); err == nil {
		t.Fatal("expected query required error")
	}
	hits, err := DiscordSearchMessages("/p", "no-results-query", "", -1)
	if err != nil {
		t.Fatalf("DiscordSearchMessages: %v", err)
	}
	if hits == nil || len(hits) != 0 {
		t.Fatalf("expected empty non-nil hits slice, got %#v", hits)
	}
}

func TestDiscordListRecentMessages_NilDBAndEmptyCases(t *testing.T) {
	setGlobalDBForTest(t, nil)
	if _, err := DiscordListRecentMessages("/p", "", "", 10); err == nil {
		t.Fatal("expected db not initialized error")
	}

	initTestDB(t)
	out, err := DiscordListRecentMessages("/p", "", "", -1)
	if err != nil {
		t.Fatalf("DiscordListRecentMessages: %v", err)
	}
	if out == nil || len(out) != 0 {
		t.Fatalf("expected empty non-nil list, got %#v", out)
	}
}

func TestProjectHistory_EmptySlices(t *testing.T) {
	initTestDB(t)
	if err := CreateSession("sess-empty", "/proj-empty", "main"); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	messages, err := GetProjectMessages("/proj-empty", 10)
	if err != nil {
		t.Fatalf("GetProjectMessages: %v", err)
	}
	if messages == nil || len(messages) != 0 {
		t.Fatalf("expected empty non-nil project messages, got %#v", messages)
	}

	summaries, err := GetProjectSessionSummaries("/proj-empty", 10)
	if err != nil {
		t.Fatalf("GetProjectSessionSummaries: %v", err)
	}
	if summaries == nil || len(summaries) != 0 {
		t.Fatalf("expected empty non-nil summaries, got %#v", summaries)
	}

	learnings, err := GetProjectLearnings("/proj-empty", 10)
	if err != nil {
		t.Fatalf("GetProjectLearnings: %v", err)
	}
	if learnings == nil || len(learnings) != 0 {
		t.Fatalf("expected empty non-nil learnings, got %#v", learnings)
	}

	validations, err := GetProjectValidations("/proj-empty", 10)
	if err != nil {
		t.Fatalf("GetProjectValidations: %v", err)
	}
	if validations == nil || len(validations) != 0 {
		t.Fatalf("expected empty non-nil validations, got %#v", validations)
	}
}

// ── WorkingState persistence ──────────────────────────────────────────────────

func TestSaveWorkingState_AndLoad_RoundTrip(t *testing.T) {
	initTestDB(t)
	err := SaveWorkingState("session1", `{"CurrentTask":"fix bug"}`)
	if err != nil {
		t.Fatal(err)
	}
	got, err := LoadWorkingState("session1")
	if err != nil {
		t.Fatal(err)
	}
	if got != `{"CurrentTask":"fix bug"}` {
		t.Errorf("expected round-trip, got %q", got)
	}
}

func TestSaveWorkingState_Upsert(t *testing.T) {
	initTestDB(t)
	_ = SaveWorkingState("s-upsert", `{"CurrentTask":"v1"}`)
	_ = SaveWorkingState("s-upsert", `{"CurrentTask":"v2"}`)
	got, err := LoadWorkingState("s-upsert")
	if err != nil {
		t.Fatal(err)
	}
	if got != `{"CurrentTask":"v2"}` {
		t.Errorf("expected upsert to write v2, got %q", got)
	}
}

func TestLoadWorkingState_NotFound_ReturnsError(t *testing.T) {
	initTestDB(t)
	got, err := LoadWorkingState("nonexistent-session")
	if err == nil {
		t.Error("expected error for missing session")
	}
	if got != "" {
		t.Errorf("expected empty string, got %q", got)
	}
}
