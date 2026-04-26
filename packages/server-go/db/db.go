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

// osUserConfigDirFn and osUserHomeDirFn are injectable for tests.
var (
	osUserConfigDirFn = os.UserConfigDir
	osUserHomeDirFn   = os.UserHomeDir
	sqlOpenFn         = sql.Open
)

func stateDir(projectPath string) string {
	if override := os.Getenv("ENGINE_STATE_DIR"); override != "" {
		return override
	}
	if configDir, err := osUserConfigDirFn(); err == nil && configDir != "" {
		return filepath.Join(configDir, "Engine")
	}
	if homeDir, err := osUserHomeDirFn(); err == nil && homeDir != "" {
		return filepath.Join(homeDir, ".engine")
	}
	if projectPath != "" {
		return filepath.Join(projectPath, ".engine")
	}
	return filepath.Join(".", ".engine")
}

// Init opens (or creates) the SQLite database at the Engine app state path.
func Init(projectPath string) error {
	if globalDB != nil {
		_ = globalDB.Close()
		globalDB = nil
	}

	dir := stateDir(projectPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create engine state dir: %w", err)
	}
	dbPath := filepath.Join(dir, "state.db")

	db, err := sqlOpenFn("sqlite", dbPath+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
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
		CREATE TABLE IF NOT EXISTS project_directions (
			project_path TEXT PRIMARY KEY,
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
		CREATE TABLE IF NOT EXISTS discord_messages (
			id            TEXT PRIMARY KEY,
			project_path  TEXT NOT NULL,
			channel_id    TEXT NOT NULL DEFAULT '',
			thread_id     TEXT NOT NULL DEFAULT '',
			session_id    TEXT NOT NULL DEFAULT '',
			author_id     TEXT NOT NULL DEFAULT '',
			author_name   TEXT NOT NULL DEFAULT '',
			direction     TEXT NOT NULL DEFAULT 'in',
			kind          TEXT NOT NULL DEFAULT 'message',
			content       TEXT NOT NULL DEFAULT '',
			created_at    TEXT NOT NULL
		);
		CREATE INDEX IF NOT EXISTS idx_discord_messages_project_created
			ON discord_messages(project_path, created_at DESC);
		CREATE INDEX IF NOT EXISTS idx_discord_messages_thread_created
			ON discord_messages(thread_id, created_at DESC);
		CREATE INDEX IF NOT EXISTS idx_discord_messages_session_created
			ON discord_messages(session_id, created_at DESC);
		CREATE VIRTUAL TABLE IF NOT EXISTS discord_messages_fts USING fts5(
			content,
			author_name,
			content='discord_messages',
			content_rowid='rowid',
			tokenize='porter unicode61'
		);
		CREATE TRIGGER IF NOT EXISTS discord_messages_ai AFTER INSERT ON discord_messages BEGIN
			INSERT INTO discord_messages_fts(rowid, content, author_name)
			VALUES (new.rowid, new.content, new.author_name);
		END;
		CREATE TRIGGER IF NOT EXISTS discord_messages_ad AFTER DELETE ON discord_messages BEGIN
			INSERT INTO discord_messages_fts(discord_messages_fts, rowid, content, author_name)
			VALUES ('delete', old.rowid, old.content, old.author_name);
		END;
		CREATE TABLE IF NOT EXISTS discord_session_threads (
			session_id   TEXT PRIMARY KEY,
			project_path TEXT NOT NULL,
			thread_id    TEXT NOT NULL,
			channel_id   TEXT NOT NULL DEFAULT '',
			created_at   TEXT NOT NULL
		);
		CREATE INDEX IF NOT EXISTS idx_discord_session_threads_thread
			ON discord_session_threads(thread_id);
		CREATE INDEX IF NOT EXISTS idx_discord_session_threads_project
			ON discord_session_threads(project_path, created_at DESC);
	`)
	return err
}

// Session mirrors the TypeScript Session type.
type Session struct {
	ID           string `json:"id"`
	ProjectPath  string `json:"projectPath"`
	BranchName   string `json:"branchName"`
	ProjectDirection string `json:"projectDirection,omitempty"`
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
	sess, err := scanSession(row)
	if err != nil {
		return nil, err
	}
	if direction, directionErr := GetProjectDirection(sess.ProjectPath); directionErr == nil {
		sess.ProjectDirection = direction
	}
	return sess, nil
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
	if direction, directionErr := GetProjectDirection(projectPath); directionErr == nil {
		for i := range sessions {
			sessions[i].ProjectDirection = direction
		}
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

func UpdateSessionSummary(sessionId, summary string) error {
	_, err := globalDB.Exec(`UPDATE sessions SET summary=?, updated_at=? WHERE id=?`, summary, now(), sessionId)
	return err
}

// UpdateSessionProjectPath updates the project_path of an existing session.
func UpdateSessionProjectPath(id, path string) error {
	_, err := globalDB.Exec(`UPDATE sessions SET project_path=?, updated_at=? WHERE id=?`, path, now(), id)
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

// GetRelevantLearnings queries past learning events for a specific project matching a pattern keyword.
func GetRelevantLearnings(projectPath string, keyword string, limit int) ([]LearningEvent, error) {
	rows, err := globalDB.Query(
		`SELECT l.id, l.session_id, l.pattern, l.outcome, l.confidence, l.category, l.context, l.created_at
		 FROM learning_events l
		 JOIN sessions s ON s.id = l.session_id
		 WHERE s.project_path = ?
		   AND (l.pattern LIKE ? OR l.category LIKE ? OR l.context LIKE ?)
		 ORDER BY l.confidence DESC, l.created_at DESC LIMIT ?`,
		projectPath, "%"+keyword+"%", "%"+keyword+"%", "%"+keyword+"%", limit,
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

// GetLearningsByCategory returns all learnings for a specific project and category.
func GetLearningsByCategory(projectPath string, category string) ([]LearningEvent, error) {
	rows, err := globalDB.Query(
		`SELECT l.id, l.session_id, l.pattern, l.outcome, l.confidence, l.category, l.context, l.created_at
		 FROM learning_events l
		 JOIN sessions s ON s.id = l.session_id
		 WHERE s.project_path = ? AND l.category = ?
		 ORDER BY l.confidence DESC, l.created_at DESC`,
		projectPath, category,
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
