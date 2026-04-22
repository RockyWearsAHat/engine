package db

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

var globalDB *sql.DB

func stateDir(projectPath string) string {
	if override := os.Getenv("ENGINE_STATE_DIR"); override != "" {
		return override
	}
	if configDir, err := os.UserConfigDir(); err == nil && configDir != "" {
		return filepath.Join(configDir, "Engine")
	}
	if homeDir, err := os.UserHomeDir(); err == nil && homeDir != "" {
		return filepath.Join(homeDir, ".engine")
	}
	if projectPath != "" {
		return filepath.Join(projectPath, ".engine")
	}
	return filepath.Join(".", ".engine")
}

// Init opens (or creates) the SQLite database at the Engine app state path.
func Init(projectPath string) error {
	dir := stateDir(projectPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create engine state dir: %w", err)
	}
	dbPath := filepath.Join(dir, "state.db")

	db, err := sql.Open("sqlite", dbPath+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	globalDB = db
	return migrate()
}

func migrate() error {
	_, err := globalDB.Exec(`
		CREATE TABLE IF NOT EXISTS sessions (
			id          TEXT PRIMARY KEY,
			project_path TEXT NOT NULL,
			branch_name  TEXT NOT NULL DEFAULT '',
			summary      TEXT NOT NULL DEFAULT '',
			created_at   TEXT NOT NULL,
			updated_at   TEXT NOT NULL
		);
		CREATE TABLE IF NOT EXISTS messages (
			id         TEXT PRIMARY KEY,
			session_id TEXT NOT NULL,
			role       TEXT NOT NULL,
			content    TEXT NOT NULL,
			tool_calls TEXT,
			created_at TEXT NOT NULL,
			FOREIGN KEY(session_id) REFERENCES sessions(id)
		);
		CREATE TABLE IF NOT EXISTS tool_log (
			id          TEXT PRIMARY KEY,
			session_id  TEXT NOT NULL,
			name        TEXT NOT NULL,
			input       TEXT,
			result      TEXT,
			is_error    INTEGER NOT NULL DEFAULT 0,
			duration_ms INTEGER NOT NULL DEFAULT 0,
			created_at  TEXT NOT NULL
		);
		CREATE TABLE IF NOT EXISTS validation_results (
			id             TEXT PRIMARY KEY,
			session_id     TEXT NOT NULL,
			issue          TEXT NOT NULL DEFAULT '',
			issue_resolved INTEGER NOT NULL DEFAULT 0,
			test_passed    INTEGER NOT NULL DEFAULT 0,
			error_count    INTEGER NOT NULL DEFAULT 0,
			warning_count  INTEGER NOT NULL DEFAULT 0,
			duration_ms    INTEGER NOT NULL DEFAULT 0,
			evidence       TEXT NOT NULL DEFAULT '',
			command        TEXT NOT NULL DEFAULT '',
			created_at     TEXT NOT NULL,
			FOREIGN KEY(session_id) REFERENCES sessions(id)
		);
		CREATE TABLE IF NOT EXISTS learning_events (
			id          TEXT PRIMARY KEY,
			session_id  TEXT NOT NULL,
			pattern     TEXT NOT NULL,
			outcome     TEXT NOT NULL,
			confidence  REAL NOT NULL DEFAULT 0.5,
			category    TEXT NOT NULL DEFAULT '',
			context     TEXT NOT NULL DEFAULT '',
			created_at  TEXT NOT NULL,
			FOREIGN KEY(session_id) REFERENCES sessions(id)
		);
		CREATE TABLE IF NOT EXISTS attention_residuals (
			id           TEXT PRIMARY KEY,
			session_id   TEXT NOT NULL,
			message_id   TEXT NOT NULL,
			source_key   TEXT NOT NULL,
			source_type  TEXT NOT NULL,
			source_label TEXT NOT NULL DEFAULT '',
			query_text   TEXT NOT NULL DEFAULT '',
			weight       REAL NOT NULL DEFAULT 0,
			score        REAL NOT NULL DEFAULT 0,
			context      TEXT NOT NULL DEFAULT '',
			created_at   TEXT NOT NULL,
			FOREIGN KEY(session_id) REFERENCES sessions(id),
			FOREIGN KEY(message_id) REFERENCES messages(id)
		);
		CREATE INDEX IF NOT EXISTS idx_attention_residuals_session_created
			ON attention_residuals(session_id, created_at DESC);
		CREATE INDEX IF NOT EXISTS idx_attention_residuals_source_key_created
			ON attention_residuals(source_key, created_at DESC);

		-- Message branching: stores checkpointed conversation tails so users can
		-- navigate back to prior versions after editing a message.
		CREATE TABLE IF NOT EXISTS message_branches (
			id            TEXT PRIMARY KEY,
			session_id    TEXT NOT NULL,
			edit_group_id TEXT NOT NULL,  -- shared across all branches at the same edit slot
			after_msg_id  TEXT NOT NULL,  -- ID of the message just before the edit point ('' = start)
			branch_index  INTEGER NOT NULL,
			messages_json TEXT NOT NULL,  -- JSON array of Message from the edit point to end
			created_at    TEXT NOT NULL
		);
		CREATE INDEX IF NOT EXISTS idx_message_branches_group
			ON message_branches(edit_group_id, branch_index);
		CREATE INDEX IF NOT EXISTS idx_message_branches_session
			ON message_branches(session_id, after_msg_id);

		-- Project memories: explicit key facts, decisions, preferences and progress
		-- written by the model or auto-extracted. Always injected into every system
		-- prompt unconditionally — no retrieval scoring, no query filtering.
		CREATE TABLE IF NOT EXISTS memories (
			id           TEXT PRIMARY KEY,
			project_path TEXT NOT NULL,
			session_id   TEXT NOT NULL DEFAULT '',
			category     TEXT NOT NULL DEFAULT 'fact',  -- fact|decision|preference|progress|error
			content      TEXT NOT NULL,
			importance   REAL NOT NULL DEFAULT 0.5,
			created_at   TEXT NOT NULL,
			updated_at   TEXT NOT NULL,
			access_count INTEGER NOT NULL DEFAULT 0,
			last_accessed TEXT NOT NULL DEFAULT ''
		);
		CREATE INDEX IF NOT EXISTS idx_memories_project
			ON memories(project_path, importance DESC, updated_at DESC);

		-- memory_versions is an append-only audit log of every change to a memory.
		-- Enables the model to see how a belief evolved and why — and lets the system
		-- detect volatile memories (updated many times = treat with lower confidence).
		CREATE TABLE IF NOT EXISTS memory_versions (
			id          TEXT PRIMARY KEY,
			memory_id   TEXT NOT NULL REFERENCES memories(id) ON DELETE CASCADE,
			old_content TEXT NOT NULL,
			new_content TEXT NOT NULL,
			reason      TEXT NOT NULL DEFAULT '',
			changed_at  TEXT NOT NULL
		);
		CREATE INDEX IF NOT EXISTS idx_memory_versions_memory
			ON memory_versions(memory_id, changed_at DESC);
	`)
	return err
}

// Session mirrors the TypeScript Session type.
type Session struct {
	ID           string `json:"id"`
	ProjectPath  string `json:"projectPath"`
	BranchName   string `json:"branchName"`
	Summary      string `json:"summary"`
	CreatedAt    string `json:"createdAt"`
	UpdatedAt    string `json:"updatedAt"`
	MessageCount int    `json:"messageCount"`
}

// Message mirrors the TypeScript Message type.
type Message struct {
	ID        string      `json:"id"`
	SessionID string      `json:"sessionId"`
	Role      string      `json:"role"`
	Content   string      `json:"content"`
	ToolCalls interface{} `json:"toolCalls,omitempty"`
	CreatedAt string      `json:"createdAt"`
}

func now() string { return time.Now().UTC().Format(time.RFC3339) }

func CreateSession(id, projectPath, branchName string) error {
	t := now()
	_, err := globalDB.Exec(
		`INSERT INTO sessions (id, project_path, branch_name, summary, created_at, updated_at) VALUES (?,?,?,'',?,?)`,
		id, projectPath, branchName, t, t,
	)
	return err
}

func GetSession(id string) (*Session, error) {
	row := globalDB.QueryRow(`
		SELECT s.id, s.project_path, s.branch_name, s.summary, s.created_at, s.updated_at,
		       COUNT(m.id) as message_count
		FROM sessions s
		LEFT JOIN messages m ON m.session_id = s.id
		WHERE s.id = ?
		GROUP BY s.id`, id)
	return scanSession(row)
}

func ListSessions(projectPath string) ([]Session, error) {
	rows, err := globalDB.Query(`
		SELECT s.id, s.project_path, s.branch_name, s.summary, s.created_at, s.updated_at,
		       COUNT(m.id) as message_count
		FROM sessions s
		LEFT JOIN messages m ON m.session_id = s.id
		WHERE s.project_path = ?
		GROUP BY s.id
		ORDER BY s.updated_at DESC`, projectPath)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var sessions []Session
	for rows.Next() {
		s, err := scanSession(rows)
		if err != nil {
			return nil, err
		}
		sessions = append(sessions, *s)
	}
	if sessions == nil {
		sessions = []Session{}
	}
	return sessions, nil
}

type scanner interface {
	Scan(dest ...any) error
}

func scanSession(s scanner) (*Session, error) {
	var sess Session
	err := s.Scan(&sess.ID, &sess.ProjectPath, &sess.BranchName, &sess.Summary,
		&sess.CreatedAt, &sess.UpdatedAt, &sess.MessageCount)
	if err != nil {
		return nil, err
	}
	return &sess, nil
}

func SaveMessage(id, sessionId, role, content string, toolCalls interface{}) error {
	var tcJSON *string
	if toolCalls != nil {
		b, err := json.Marshal(toolCalls)
		if err == nil {
			s := string(b)
			tcJSON = &s
		}
	}
	t := now()
	_, err := globalDB.Exec(
		`INSERT INTO messages (id, session_id, role, content, tool_calls, created_at) VALUES (?,?,?,?,?,?)`,
		id, sessionId, role, content, tcJSON, t,
	)
	if err != nil {
		return err
	}
	_, err = globalDB.Exec(`UPDATE sessions SET updated_at=? WHERE id=?`, t, sessionId)
	return err
}

func GetMessages(sessionId string) ([]Message, error) {
	rows, err := globalDB.Query(
		`SELECT id, session_id, role, content, tool_calls, created_at FROM messages WHERE session_id=? ORDER BY created_at ASC`,
		sessionId,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var msgs []Message
	for rows.Next() {
		var m Message
		var tc *string
		if err := rows.Scan(&m.ID, &m.SessionID, &m.Role, &m.Content, &tc, &m.CreatedAt); err != nil {
			return nil, err
		}
		if tc != nil {
			var raw interface{}
			if err := json.Unmarshal([]byte(*tc), &raw); err == nil {
				m.ToolCalls = raw
			}
		}
		msgs = append(msgs, m)
	}
	if msgs == nil {
		msgs = []Message{}
	}
	return msgs, nil
}

func LogToolCall(id, sessionId, name string, input interface{}, result string, isError bool, durationMs int64) error {
	inputJSON, _ := json.Marshal(input)
	errInt := 0
	if isError {
		errInt = 1
	}
	_, err := globalDB.Exec(
		`INSERT INTO tool_log (id, session_id, name, input, result, is_error, duration_ms, created_at) VALUES (?,?,?,?,?,?,?,?)`,
		id, sessionId, name, string(inputJSON), result, errInt, durationMs, now(),
	)
	return err
}

// GetRecentToolNames returns distinct tool names used in recent successful calls for
// a session, newest first. Used to pre-populate ActiveTools so the model doesn't
// have to re-run search_tools at the start of every request.
func GetRecentToolNames(sessionId string, limit int) ([]string, error) {
	rows, err := globalDB.Query(
		`SELECT name FROM tool_log WHERE session_id=? AND is_error=0 ORDER BY created_at DESC LIMIT ?`,
		sessionId, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	seen := map[string]bool{}
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err == nil && !seen[name] {
			seen[name] = true
			names = append(names, name)
		}
	}
	return names, nil
}

func UpdateSessionSummary(sessionId, summary string) error {
	_, err := globalDB.Exec(`UPDATE sessions SET summary=?, updated_at=? WHERE id=?`, summary, now(), sessionId)
	return err
}

// ValidationResult mirrors ai.BehavioralResult with DB metadata.
type ValidationResult struct {
	ID            string `json:"id"`
	SessionID     string `json:"sessionId"`
	Issue         string `json:"issue"`
	IssueResolved bool   `json:"issueResolved"`
	TestPassed    bool   `json:"testPassed"`
	ErrorCount    int    `json:"errorCount"`
	WarningCount  int    `json:"warningCount"`
	DurationMs    int64  `json:"durationMs"`
	Evidence      string `json:"evidence"`
	Command       string `json:"command"`
	CreatedAt     string `json:"createdAt"`
}

// SaveValidationResult persists a behavioral validation result.
func SaveValidationResult(id, sessionId, issue string, issueResolved, testPassed bool, errorCount, warningCount int, durationMs int64, evidence, command string) error {
	resolved := 0
	if issueResolved {
		resolved = 1
	}
	passed := 0
	if testPassed {
		passed = 1
	}
	_, err := globalDB.Exec(
		`INSERT INTO validation_results (id, session_id, issue, issue_resolved, test_passed, error_count, warning_count, duration_ms, evidence, command, created_at) VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		id, sessionId, issue, resolved, passed, errorCount, warningCount, durationMs, evidence, command, now(),
	)
	return err
}

// GetValidationResults returns all validation results for a session, newest first.
func GetValidationResults(sessionId string) ([]ValidationResult, error) {
	rows, err := globalDB.Query(
		`SELECT id, session_id, issue, issue_resolved, test_passed, error_count, warning_count, duration_ms, evidence, command, created_at
		 FROM validation_results WHERE session_id=? ORDER BY created_at DESC`, sessionId,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var results []ValidationResult
	for rows.Next() {
		var r ValidationResult
		var resolved, passed int
		if err := rows.Scan(&r.ID, &r.SessionID, &r.Issue, &resolved, &passed, &r.ErrorCount, &r.WarningCount, &r.DurationMs, &r.Evidence, &r.Command, &r.CreatedAt); err != nil {
			return nil, err
		}
		r.IssueResolved = resolved == 1
		r.TestPassed = passed == 1
		results = append(results, r)
	}
	if results == nil {
		results = []ValidationResult{}
	}
	return results, nil
}

// GetLatestValidation returns the most recent validation for a session+issue combo.
func GetLatestValidation(sessionId, issue string) (*ValidationResult, error) {
	row := globalDB.QueryRow(
		`SELECT id, session_id, issue, issue_resolved, test_passed, error_count, warning_count, duration_ms, evidence, command, created_at
		 FROM validation_results WHERE session_id=? AND issue=? ORDER BY created_at DESC LIMIT 1`,
		sessionId, issue,
	)
	var r ValidationResult
	var resolved, passed int
	err := row.Scan(&r.ID, &r.SessionID, &r.Issue, &resolved, &passed, &r.ErrorCount, &r.WarningCount, &r.DurationMs, &r.Evidence, &r.Command, &r.CreatedAt)
	if err != nil {
		return nil, err
	}
	r.IssueResolved = resolved == 1
	r.TestPassed = passed == 1
	return &r, nil
}

// LearningEvent records what worked or failed during a test cycle.
type LearningEvent struct {
	ID         string  `json:"id"`
	SessionID  string  `json:"sessionId"`
	Pattern    string  `json:"pattern"`
	Outcome    string  `json:"outcome"`
	Confidence float64 `json:"confidence"`
	Category   string  `json:"category"`
	Context    string  `json:"context"`
	CreatedAt  string  `json:"createdAt"`
}

// SaveLearningEvent records what worked or failed for cross-session learning.
func SaveLearningEvent(id, sessionId, pattern, outcome string, confidence float64, category, context string) error {
	_, err := globalDB.Exec(
		`INSERT INTO learning_events (id, session_id, pattern, outcome, confidence, category, context, created_at) VALUES (?,?,?,?,?,?,?,?)`,
		id, sessionId, pattern, outcome, confidence, category, context, now(),
	)
	return err
}

// GetRelevantLearnings queries past learning events matching a pattern keyword.
func GetRelevantLearnings(keyword string, limit int) ([]LearningEvent, error) {
	rows, err := globalDB.Query(
		`SELECT id, session_id, pattern, outcome, confidence, category, context, created_at
		 FROM learning_events
		 WHERE pattern LIKE ? OR category LIKE ? OR context LIKE ?
		 ORDER BY confidence DESC, created_at DESC LIMIT ?`,
		"%"+keyword+"%", "%"+keyword+"%", "%"+keyword+"%", limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var events []LearningEvent
	for rows.Next() {
		var e LearningEvent
		if err := rows.Scan(&e.ID, &e.SessionID, &e.Pattern, &e.Outcome, &e.Confidence, &e.Category, &e.Context, &e.CreatedAt); err != nil {
			return nil, err
		}
		events = append(events, e)
	}
	if events == nil {
		events = []LearningEvent{}
	}
	return events, nil
}

// GetLearningsByCategory returns all learnings for a specific category.
func GetLearningsByCategory(category string) ([]LearningEvent, error) {
	rows, err := globalDB.Query(
		`SELECT id, session_id, pattern, outcome, confidence, category, context, created_at
		 FROM learning_events WHERE category=? ORDER BY confidence DESC, created_at DESC`,
		category,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var events []LearningEvent
	for rows.Next() {
		var e LearningEvent
		if err := rows.Scan(&e.ID, &e.SessionID, &e.Pattern, &e.Outcome, &e.Confidence, &e.Category, &e.Context, &e.CreatedAt); err != nil {
			return nil, err
		}
		events = append(events, e)
	}
	if events == nil {
		events = []LearningEvent{}
	}
	return events, nil
}

// ── Message branching ─────────────────────────────────────────────────────────

// MessageBranchInfo summarises the branch state at a given edit slot.
type MessageBranchInfo struct {
	EditGroupID   string `json:"editGroupId"`
	CurrentIndex  int    `json:"current"`
	TotalBranches int    `json:"total"`
}

// SaveMessageBranch snapshots a conversation tail into the branch table.
// afterMsgId is the ID of the last message that stays; the snapshot covers
// everything from the edit point to the end.
func SaveMessageBranch(id, sessionId, afterMsgId, editGroupId string, branchIndex int, msgs []Message) error {
	b, err := json.Marshal(msgs)
	if err != nil {
		return err
	}
	_, err = globalDB.Exec(
		`INSERT INTO message_branches (id, session_id, edit_group_id, after_msg_id, branch_index, messages_json, created_at)
		 VALUES (?,?,?,?,?,?,?)`,
		id, sessionId, editGroupId, afterMsgId, branchIndex, string(b), now(),
	)
	return err
}

// NextBranchIndex returns one past the highest branch_index stored for editGroupId.
func NextBranchIndex(editGroupId string) (int, error) {
	row := globalDB.QueryRow(
		`SELECT COALESCE(MAX(branch_index), -1) FROM message_branches WHERE edit_group_id=?`,
		editGroupId,
	)
	var max int
	if err := row.Scan(&max); err != nil {
		return 0, err
	}
	return max + 1, nil
}

// GetEditGroupID returns the edit_group_id for a (sessionId, afterMsgId) slot,
// or "" if none exists yet.
func GetEditGroupID(sessionId, afterMsgId string) (string, error) {
	row := globalDB.QueryRow(
		`SELECT edit_group_id FROM message_branches WHERE session_id=? AND after_msg_id=? LIMIT 1`,
		sessionId, afterMsgId,
	)
	var gid string
	err := row.Scan(&gid)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return gid, err
}

// GetBranchInfo returns a map of afterMsgId → MessageBranchInfo for a session.
func GetBranchInfo(sessionId string) (map[string]MessageBranchInfo, error) {
	rows, err := globalDB.Query(
		`SELECT after_msg_id, edit_group_id, MAX(branch_index) as cur, COUNT(*) as total
		 FROM message_branches WHERE session_id=?
		 GROUP BY edit_group_id, after_msg_id`,
		sessionId,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := map[string]MessageBranchInfo{}
	for rows.Next() {
		var afterMsgId, editGroupId string
		var cur, total int
		if err := rows.Scan(&afterMsgId, &editGroupId, &cur, &total); err != nil {
			return nil, err
		}
		result[afterMsgId] = MessageBranchInfo{
			EditGroupID:   editGroupId,
			CurrentIndex:  cur,
			TotalBranches: total,
		}
	}
	return result, nil
}

// GetBranch returns the Message snapshot for a given edit_group_id + branch index.
func GetBranch(editGroupId string, branchIndex int) ([]Message, error) {
	row := globalDB.QueryRow(
		`SELECT messages_json FROM message_branches WHERE edit_group_id=? AND branch_index=?`,
		editGroupId, branchIndex,
	)
	var raw string
	if err := row.Scan(&raw); err != nil {
		return nil, err
	}
	var msgs []Message
	if err := json.Unmarshal([]byte(raw), &msgs); err != nil {
		return nil, err
	}
	return msgs, nil
}

// DeleteMessagesFrom removes messages with the given IDs from the messages table.
func DeleteMessagesFrom(sessionId string, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	placeholders := make([]string, len(ids))
	args := make([]interface{}, len(ids)+1)
	args[0] = sessionId
	for i, id := range ids {
		placeholders[i] = "?"
		args[i+1] = id
	}
	q := "DELETE FROM messages WHERE session_id=? AND id IN (" + joinStrings(placeholders, ",") + ")"
	_, err := globalDB.Exec(q, args...)
	return err
}

// GetAfterMsgIDForGroup returns the after_msg_id anchor for an edit group.
func GetAfterMsgIDForGroup(editGroupId string) (string, error) {
	row := globalDB.QueryRow(
		`SELECT after_msg_id FROM message_branches WHERE edit_group_id=? LIMIT 1`,
		editGroupId,
	)
	var id string
	err := row.Scan(&id)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return id, err
}

func joinStrings(ss []string, sep string) string {
	out := ""
	for i, s := range ss {
		if i > 0 {
			out += sep
		}
		out += s
	}
	return out
}

// Memory is a persistent fact, decision, preference, or progress note written by
// the model or auto-extracted from conversation. Always injected into system prompt.
type Memory struct {
	ID           string  `json:"id"`
	ProjectPath  string  `json:"projectPath"`
	SessionID    string  `json:"sessionId"`
	Category     string  `json:"category"` // fact | decision | preference | progress | error
	Content      string  `json:"content"`
	Importance   float64 `json:"importance"`
	CreatedAt    string  `json:"createdAt"`
	UpdatedAt    string  `json:"updatedAt"`
	VersionCount int     `json:"versionCount"` // how many times this memory has been updated
}

// MemoryVersion is one entry in the append-only audit log for a memory.
type MemoryVersion struct {
	ID        string `json:"id"`
	MemoryID  string `json:"memoryId"`
	OldContent string `json:"oldContent"`
	NewContent string `json:"newContent"`
	Reason    string `json:"reason"`
	ChangedAt string `json:"changedAt"`
}

// SaveMemory inserts a new memory record.
func SaveMemory(m Memory) error {
	now := time.Now().UTC().Format(time.RFC3339)
	if m.ID == "" {
		m.ID = fmt.Sprintf("mem-%d", time.Now().UnixNano())
	}
	if m.CreatedAt == "" {
		m.CreatedAt = now
	}
	m.UpdatedAt = now
	if m.Importance <= 0 {
		m.Importance = 0.5
	}
	_, err := globalDB.Exec(
		`INSERT INTO memories (id, project_path, session_id, category, content, importance, created_at, updated_at, access_count, last_accessed)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, 0, '')`,
		m.ID, m.ProjectPath, m.SessionID, m.Category, m.Content, m.Importance, m.CreatedAt, m.UpdatedAt,
	)
	return err
}

// UpdateMemory replaces the content of a memory and appends a version record
// so the full history of changes is never lost.
func UpdateMemory(id, content, reason string) error {
	now := time.Now().UTC().Format(time.RFC3339)

	// Fetch current content before overwriting so we can record what changed.
	var oldContent string
	_ = globalDB.QueryRow(`SELECT content FROM memories WHERE id=?`, id).Scan(&oldContent)

	tx, err := globalDB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck

	if _, err := tx.Exec(`UPDATE memories SET content=?, updated_at=? WHERE id=?`, content, now, id); err != nil {
		return err
	}
	versionID := fmt.Sprintf("memv-%d", time.Now().UnixNano())
	if _, err := tx.Exec(
		`INSERT INTO memory_versions (id, memory_id, old_content, new_content, reason, changed_at) VALUES (?,?,?,?,?,?)`,
		versionID, id, oldContent, content, reason, now,
	); err != nil {
		return err
	}
	return tx.Commit()
}

// DeleteMemory removes a memory by ID.
func DeleteMemory(id string) error {
	_, err := globalDB.Exec(`DELETE FROM memories WHERE id=?`, id)
	return err
}

// GetProjectMemories returns the top N memories for a project with their version counts,
// ordered by importance DESC then updated_at DESC.
func GetProjectMemories(projectPath string, limit int) ([]Memory, error) {
	rows, err := globalDB.Query(
		`SELECT m.id, m.project_path, m.session_id, m.category, m.content,
		        m.importance, m.created_at, m.updated_at,
		        COUNT(v.id) as version_count
		 FROM memories m
		 LEFT JOIN memory_versions v ON v.memory_id = m.id
		 WHERE m.project_path=?
		 GROUP BY m.id
		 ORDER BY m.importance DESC, m.updated_at DESC
		 LIMIT ?`,
		projectPath, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Memory
	for rows.Next() {
		var m Memory
		if err := rows.Scan(&m.ID, &m.ProjectPath, &m.SessionID, &m.Category, &m.Content,
			&m.Importance, &m.CreatedAt, &m.UpdatedAt, &m.VersionCount); err != nil {
			continue
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// GetMemoryHistory returns the full version history for a single memory, newest first.
func GetMemoryHistory(memoryID string) ([]MemoryVersion, error) {
	rows, err := globalDB.Query(
		`SELECT id, memory_id, old_content, new_content, reason, changed_at
		 FROM memory_versions WHERE memory_id=? ORDER BY changed_at DESC`,
		memoryID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MemoryVersion
	for rows.Next() {
		var v MemoryVersion
		if err := rows.Scan(&v.ID, &v.MemoryID, &v.OldContent, &v.NewContent, &v.Reason, &v.ChangedAt); err != nil {
			continue
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// TouchMemory increments access_count and sets last_accessed for a memory.
func TouchMemory(id string) {
	now := time.Now().UTC().Format(time.RFC3339)
	globalDB.Exec( //nolint:errcheck
		`UPDATE memories SET access_count=access_count+1, last_accessed=? WHERE id=?`,
		now, id,
	)
}
