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
