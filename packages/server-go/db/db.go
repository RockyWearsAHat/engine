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
