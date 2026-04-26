package db

import (
	"strings"
	"testing"
	"time"
)

func TestUsageDashboard_ProjectScopeAggregates(t *testing.T) {
	initTestDB(t)

	const projectA = "/workspace/a"
	const projectB = "/workspace/b"

	if err := CreateSession("s-project-a", projectA, "main"); err != nil {
		t.Fatalf("CreateSession project A: %v", err)
	}
	if err := CreateSession("s-project-b", projectB, "main"); err != nil {
		t.Fatalf("CreateSession project B: %v", err)
	}

	if err := LogUsageEvent("u1", "s-project-a", projectA, "anthropic", "claude-sonnet-4.6", 100, 40, 140, 0.014, 2500); err != nil {
		t.Fatalf("LogUsageEvent u1: %v", err)
	}
	if err := LogUsageEvent("u2", "s-project-a", projectA, "openai", "gpt-4o", 50, 20, 70, 0.007, 1300); err != nil {
		t.Fatalf("LogUsageEvent u2: %v", err)
	}
	if err := LogUsageEvent("u3", "s-project-b", projectB, "anthropic", "claude-sonnet-4.6", 90, 10, 100, 0.01, 1200); err != nil {
		t.Fatalf("LogUsageEvent u3: %v", err)
	}

	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	if _, err := globalDB.Exec(`INSERT INTO messages (id, session_id, role, content, tool_calls, created_at) VALUES (?,?,?,?,?,?)`, "m1", "s-project-a", "user", "one", "[]", base.Format(time.RFC3339)); err != nil {
		t.Fatalf("insert m1: %v", err)
	}
	if _, err := globalDB.Exec(`INSERT INTO messages (id, session_id, role, content, tool_calls, created_at) VALUES (?,?,?,?,?,?)`, "m2", "s-project-a", "assistant", "two", "[]", base.Add(5*time.Minute).Format(time.RFC3339)); err != nil {
		t.Fatalf("insert m2: %v", err)
	}
	// Gap above cap should clamp to activeGapCap (15m).
	if _, err := globalDB.Exec(`INSERT INTO messages (id, session_id, role, content, tool_calls, created_at) VALUES (?,?,?,?,?,?)`, "m3", "s-project-a", "user", "three", "[]", base.Add(70*time.Minute).Format(time.RFC3339)); err != nil {
		t.Fatalf("insert m3: %v", err)
	}

	dashboard, err := GetUsageDashboard("project", projectA, "")
	if err != nil {
		t.Fatalf("GetUsageDashboard project: %v", err)
	}

	if dashboard.Scope != "project" {
		t.Fatalf("scope = %q, want project", dashboard.Scope)
	}
	if dashboard.ProjectPath != projectA {
		t.Fatalf("projectPath = %q, want %q", dashboard.ProjectPath, projectA)
	}
	if dashboard.Totals.Requests != 2 {
		t.Fatalf("requests = %d, want 2", dashboard.Totals.Requests)
	}
	if dashboard.Totals.TotalTokens != 210 {
		t.Fatalf("totalTokens = %d, want 210", dashboard.Totals.TotalTokens)
	}
	if dashboard.Totals.AIComputeMs != 3800 {
		t.Fatalf("aiComputeMs = %d, want 3800", dashboard.Totals.AIComputeMs)
	}
	if dashboard.Totals.ActiveDevelopmentMs != 20*60*1000 {
		t.Fatalf("activeDevelopmentMs = %d, want %d", dashboard.Totals.ActiveDevelopmentMs, 20*60*1000)
	}
	if len(dashboard.Projects) != 1 || dashboard.Projects[0].ProjectPath != projectA {
		t.Fatalf("project breakdown unexpected: %#v", dashboard.Projects)
	}
	if len(dashboard.Models) != 2 {
		t.Fatalf("models len = %d, want 2", len(dashboard.Models))
	}
	if dashboard.Models[0].CostUSD < dashboard.Models[1].CostUSD {
		t.Fatalf("models should be sorted by cost desc: %#v", dashboard.Models)
	}
}

func TestUsageDashboard_UserScopeAndModelFilter(t *testing.T) {
	initTestDB(t)

	const projectA = "/workspace/a"
	const projectB = "/workspace/b"

	if err := CreateSession("s-user-a", projectA, "main"); err != nil {
		t.Fatalf("CreateSession A: %v", err)
	}
	if err := CreateSession("s-user-b", projectB, "main"); err != nil {
		t.Fatalf("CreateSession B: %v", err)
	}

	if err := LogUsageEvent("u4", "s-user-a", projectA, "openai", "gpt-4o", 10, 5, 15, 0.0015, 300); err != nil {
		t.Fatalf("LogUsageEvent u4: %v", err)
	}
	if err := LogUsageEvent("u5", "s-user-b", projectB, "openai", "gpt-4o-mini", 30, 10, 40, 0.002, 450); err != nil {
		t.Fatalf("LogUsageEvent u5: %v", err)
	}

	allDashboard, err := GetUsageDashboard("user", "", "")
	if err != nil {
		t.Fatalf("GetUsageDashboard user: %v", err)
	}
	if allDashboard.Scope != "user" {
		t.Fatalf("scope = %q, want user", allDashboard.Scope)
	}
	if allDashboard.Totals.Requests != 2 {
		t.Fatalf("requests = %d, want 2", allDashboard.Totals.Requests)
	}
	if len(allDashboard.Projects) != 2 {
		t.Fatalf("projects len = %d, want 2", len(allDashboard.Projects))
	}

	filtered, err := GetUsageDashboard("user", "", "gPt-4O")
	if err != nil {
		t.Fatalf("GetUsageDashboard filtered: %v", err)
	}
	if filtered.Totals.Requests != 1 || filtered.Totals.TotalTokens != 15 {
		t.Fatalf("filtered totals unexpected: %#v", filtered.Totals)
	}
	if len(filtered.Models) != 1 || strings.ToLower(filtered.Models[0].Model) != "gpt-4o" {
		t.Fatalf("filtered models unexpected: %#v", filtered.Models)
	}
}

func TestUsageDashboard_ValidationAndEmptyPaths(t *testing.T) {
	initTestDB(t)

	if _, err := GetUsageDashboard("invalid", "/workspace/a", ""); err == nil {
		t.Fatal("expected unsupported scope error")
	}
	if _, err := GetUsageDashboard("project", "", ""); err == nil {
		t.Fatal("expected projectPath required error")
	}

	dashboard, err := GetUsageDashboard("", "/workspace/a", "")
	if err != nil {
		t.Fatalf("GetUsageDashboard default scope: %v", err)
	}
	if dashboard.Scope != "project" {
		t.Fatalf("default scope = %q, want project", dashboard.Scope)
	}
	if dashboard.Totals.Requests != 0 || len(dashboard.Models) != 0 || len(dashboard.Projects) != 0 {
		t.Fatalf("expected empty dashboard, got %#v", dashboard)
	}
}

func TestUsageDashboard_InternalHelpers(t *testing.T) {
	initTestDB(t)

	// Empty set returns zero quickly.
	active, err := computeActiveDevelopmentMs(map[string]struct{}{})
	if err != nil {
		t.Fatalf("computeActiveDevelopmentMs empty: %v", err)
	}
	if active != 0 {
		t.Fatalf("active = %d, want 0", active)
	}

	if err := CreateSession("s-helper", "/workspace/a", "main"); err != nil {
		t.Fatalf("CreateSession helper: %v", err)
	}
	if _, err := globalDB.Exec(`INSERT INTO messages (id, session_id, role, content, tool_calls, created_at) VALUES (?,?,?,?,?,?)`, "mh1", "s-helper", "user", "x", "[]", "not-a-time"); err != nil {
		t.Fatalf("insert helper message: %v", err)
	}
	active, err = computeActiveDevelopmentMs(map[string]struct{}{"s-helper": {}})
	if err != nil {
		t.Fatalf("computeActiveDevelopmentMs invalid timestamp should not fail: %v", err)
	}
	if active != 0 {
		t.Fatalf("active with invalid timestamp = %d, want 0", active)
	}

	byProject, err := computeActiveDevelopmentMsByProject(map[string]map[string]struct{}{
		"/workspace/a": {"s-helper": {}},
	})
	if err != nil {
		t.Fatalf("computeActiveDevelopmentMsByProject: %v", err)
	}
	if _, ok := byProject["/workspace/a"]; !ok {
		t.Fatalf("missing project key in active map: %#v", byProject)
	}

	if got := averagePricePerToken(1.23, 0); got != 0 {
		t.Fatalf("averagePricePerToken zero tokens = %f, want 0", got)
	}
	if got := averagePricePerToken(1.2, 3); got <= 0 {
		t.Fatalf("averagePricePerToken positive tokens = %f, want > 0", got)
	}
}

func TestUsageDashboard_ErrorPaths(t *testing.T) {
	initTestDB(t)

	if err := CreateSession("s-error", "/workspace/a", "main"); err != nil {
		t.Fatalf("CreateSession error session: %v", err)
	}
	if err := LogUsageEvent("u6", "s-error", "/workspace/a", "openai", "gpt-4o", 1, 1, 2, 0.0001, 10); err != nil {
		t.Fatalf("LogUsageEvent setup: %v", err)
	}

	if err := globalDB.Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}

	if err := LogUsageEvent("u7", "s-error", "/workspace/a", "openai", "gpt-4o", 1, 1, 2, 0.0001, 10); err == nil {
		t.Fatal("expected LogUsageEvent failure on closed db")
	}
	if _, err := loadUsageEvents("project", "/workspace/a", ""); err == nil {
		t.Fatal("expected loadUsageEvents failure on closed db")
	}
	if _, err := GetUsageDashboard("project", "/workspace/a", ""); err == nil {
		t.Fatal("expected GetUsageDashboard failure when event load fails")
	}
	if _, err := computeActiveDevelopmentMs(map[string]struct{}{"s-error": {}}); err == nil {
		t.Fatal("expected computeActiveDevelopmentMs failure on closed db")
	}
	if _, err := computeActiveDevelopmentMsByProject(map[string]map[string]struct{}{
		"/workspace/a": {"s-error": {}},
	}); err == nil {
		t.Fatal("expected computeActiveDevelopmentMsByProject failure on closed db")
	}
}

func TestUsageDashboard_SortTieAndScanErrorPaths(t *testing.T) {
	initTestDB(t)

	const projectA = "/workspace/a"
	const projectB = "/workspace/b"
	if err := CreateSession("s-tie-a", projectA, "main"); err != nil {
		t.Fatalf("CreateSession tie a: %v", err)
	}
	if err := CreateSession("s-tie-b", projectB, "main"); err != nil {
		t.Fatalf("CreateSession tie b: %v", err)
	}

	if err := LogUsageEvent("tie-1", "s-tie-a", projectA, "openai", "gpt-4o", 10, 0, 10, 0.01, 100); err != nil {
		t.Fatalf("LogUsageEvent tie-1: %v", err)
	}
	if err := LogUsageEvent("tie-2", "s-tie-a", projectA, "anthropic", "claude-sonnet-4.6", 10, 0, 10, 0.01, 100); err != nil {
		t.Fatalf("LogUsageEvent tie-2: %v", err)
	}
	if err := LogUsageEvent("tie-3", "s-tie-b", projectB, "openai", "gpt-4o-mini", 10, 0, 10, 0.01, 100); err != nil {
		t.Fatalf("LogUsageEvent tie-3: %v", err)
	}
	if err := LogUsageEvent("tie-4", "s-tie-b", projectB, "openai", "gpt-4.1", 10, 0, 10, 0.01, 100); err != nil {
		t.Fatalf("LogUsageEvent tie-4: %v", err)
	}

	if _, err := globalDB.Exec(`INSERT INTO messages (id, session_id, role, content, tool_calls, created_at) VALUES (?,?,?,?,?,?)`, "tm1", "s-tie-a", "user", "x", "[]", time.Now().UTC().Format(time.RFC3339)); err != nil {
		t.Fatalf("insert tm1: %v", err)
	}
	if _, err := globalDB.Exec(`INSERT INTO messages (id, session_id, role, content, tool_calls, created_at) VALUES (?,?,?,?,?,?)`, "tm2", "s-tie-a", "assistant", "x", "[]", "not-rfc3339"); err != nil {
		t.Fatalf("insert tm2: %v", err)
	}
	if _, err := globalDB.Exec(`INSERT INTO messages (id, session_id, role, content, tool_calls, created_at) VALUES (?,?,?,?,?,?)`, "tm3", "s-tie-b", "user", "x", "[]", time.Now().UTC().Format(time.RFC3339)); err != nil {
		t.Fatalf("insert tm3: %v", err)
	}

	dashboard, err := GetUsageDashboard("user", "", "")
	if err != nil {
		t.Fatalf("GetUsageDashboard tie case: %v", err)
	}
	if len(dashboard.Models) < 2 || len(dashboard.Projects) < 2 {
		t.Fatalf("expected model and project rows for tie sorting, got %#v", dashboard)
	}

	// Force rows.Scan failure in loadUsageEvents by recreating usage_events without NOT NULL
	// on created_at, then inserting NULL so Scan into string fails.
	if _, err := globalDB.Exec(`ALTER TABLE usage_events RENAME TO usage_events_bak`); err != nil {
		t.Fatalf("rename usage_events: %v", err)
	}
	if _, err := globalDB.Exec(`CREATE TABLE usage_events (
		id              TEXT PRIMARY KEY,
		session_id      TEXT NOT NULL,
		project_path    TEXT NOT NULL,
		provider        TEXT NOT NULL,
		model           TEXT NOT NULL,
		input_tokens    INTEGER NOT NULL DEFAULT 0,
		output_tokens   INTEGER NOT NULL DEFAULT 0,
		total_tokens    INTEGER NOT NULL DEFAULT 0,
		cost_usd        REAL NOT NULL DEFAULT 0,
		api_duration_ms INTEGER NOT NULL DEFAULT 0,
		created_at      TEXT,
		FOREIGN KEY(session_id) REFERENCES sessions(id)
	)`); err != nil {
		t.Fatalf("create nullable usage_events: %v", err)
	}
	if _, err := globalDB.Exec(`INSERT INTO usage_events SELECT * FROM usage_events_bak`); err != nil {
		t.Fatalf("copy rows: %v", err)
	}
	if _, err := globalDB.Exec(`INSERT INTO usage_events (id, session_id, project_path, provider, model, input_tokens, output_tokens, total_tokens, cost_usd, api_duration_ms, created_at) VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		"bad-row", "s-tie-a", projectA, "openai", "gpt-4o", 1, 1, 2, 0.0001, 1, nil,
	); err != nil {
		t.Fatalf("insert bad usage row: %v", err)
	}
	if _, err := loadUsageEvents("project", projectA, ""); err == nil {
		t.Fatal("expected loadUsageEvents scan error for NULL created_at")
	}
	// Restore original table.
	if _, err := globalDB.Exec(`DROP TABLE usage_events`); err != nil {
		t.Fatalf("drop nullable usage_events: %v", err)
	}
	if _, err := globalDB.Exec(`ALTER TABLE usage_events_bak RENAME TO usage_events`); err != nil {
		t.Fatalf("restore usage_events: %v", err)
	}

	// Force rows.Scan failure in computeActiveDevelopmentMs by recreating messages without NOT NULL.
	if _, err := globalDB.Exec(`ALTER TABLE messages RENAME TO messages_bak`); err != nil {
		t.Fatalf("rename messages: %v", err)
	}
	if _, err := globalDB.Exec(`CREATE TABLE messages (
		id         TEXT PRIMARY KEY,
		session_id TEXT NOT NULL,
		role       TEXT NOT NULL,
		content    TEXT NOT NULL,
		tool_calls TEXT,
		created_at TEXT,
		FOREIGN KEY(session_id) REFERENCES sessions(id)
	)`); err != nil {
		t.Fatalf("create nullable messages: %v", err)
	}
	if _, err := globalDB.Exec(`INSERT INTO messages SELECT * FROM messages_bak`); err != nil {
		t.Fatalf("copy message rows: %v", err)
	}
	if _, err := globalDB.Exec(`INSERT INTO messages (id, session_id, role, content, tool_calls, created_at) VALUES (?,?,?,?,?,?)`, "tm-null", "s-tie-a", "assistant", "x", "[]", nil); err != nil {
		t.Fatalf("insert NULL created_at message: %v", err)
	}
	if _, err := computeActiveDevelopmentMs(map[string]struct{}{"s-tie-a": {}}); err == nil {
		t.Fatal("expected computeActiveDevelopmentMs scan error for NULL created_at")
	}
	// Restore original messages table.
	if _, err := globalDB.Exec(`DROP TABLE messages`); err != nil {
		t.Fatalf("drop nullable messages: %v", err)
	}
	if _, err := globalDB.Exec(`ALTER TABLE messages_bak RENAME TO messages`); err != nil {
		t.Fatalf("restore messages: %v", err)
	}
}

func TestUsageDashboard_ComputeActiveProjectErrorAfterEventsLoad(t *testing.T) {
	initTestDB(t)

	const projectA = "/workspace/a"
	if err := CreateSession("s-compute-error", projectA, "main"); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := LogUsageEvent("compute-1", "s-compute-error", projectA, "openai", "gpt-4o", 4, 2, 6, 0.0006, 20); err != nil {
		t.Fatalf("LogUsageEvent: %v", err)
	}

	if _, err := globalDB.Exec(`DROP TABLE messages`); err != nil {
		t.Fatalf("drop messages table: %v", err)
	}

	if _, err := GetUsageDashboard("project", projectA, ""); err == nil {
		t.Fatal("expected computeActiveDevelopmentMsByProject error when messages table is missing")
	}
}
