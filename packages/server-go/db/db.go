package db

import (
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

var globalDB *sql.DB

// globalDBMu serializes Init / WithProject swaps. Read paths use globalDB
// directly without locking; the mutex only guards swap operations.
var (
	globalDBMu      sync.Mutex
	globalDBProject string
)

// osUserConfigDirFn and osUserHomeDirFn are injectable for tests.
var (
	osUserConfigDirFn = os.UserConfigDir
	osUserHomeDirFn   = os.UserHomeDir
	sqlOpenFn         = sql.Open
)

// stateDir resolves the Engine state directory for a project. Per-project
// isolation: when projectPath is non-empty, state lives at
// <projectPath>/.engine so each cloned repo carries its own database,
// session memory, and tooling artifacts alongside its source.
func stateDir(projectPath string) string {
	if override := strings.TrimSpace(os.Getenv("ENGINE_STATE_DIR")); override != "" {
		return override
	}
	if pp := strings.TrimSpace(projectPath); pp != "" {
		return filepath.Join(pp, ".engine")
	}
	if configDir, err := osUserConfigDirFn(); err == nil && configDir != "" {
		return filepath.Join(configDir, "Engine")
	}
	if homeDir, err := osUserHomeDirFn(); err == nil && homeDir != "" {
		return filepath.Join(homeDir, ".engine")
	}
	return filepath.Join(".", ".engine")
}

// Init opens (or creates) the SQLite database at the project's state path.
func Init(projectPath string) error {
	globalDBMu.Lock()
	defer globalDBMu.Unlock()
	return initLocked(projectPath)
}

// initLocked performs the swap without acquiring the mutex. Callers must
// hold globalDBMu.
func initLocked(projectPath string) error {
	if globalDB != nil {
		if err := globalDB.Close(); err != nil {
			fmt.Printf("warning: close existing db connection failed: %v\n", err)
		}
		globalDB = nil
	}

	dir := stateDir(projectPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
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
	globalDBProject = projectPath
	return migrate()
}

// WithProject swaps the global DB to the given project's local database for
// the duration of fn, then restores the previously active project DB. Calls
// are serialized via globalDBMu so concurrent autonomous triggers (scaffold,
// CI fix, issue session) each operate against their own repo's state file.
func WithProject(projectPath string, fn func() error) error {
	globalDBMu.Lock()
	defer globalDBMu.Unlock()
	prev := globalDBProject
	if err := initLocked(projectPath); err != nil {
		return err
	}
	defer func() {
		_ = initLocked(prev)
	}()
	return fn()
}

// CurrentProject returns the project path the global DB is currently bound
// to. Primarily for diagnostics and tests.
func CurrentProject() string {
	globalDBMu.Lock()
	defer globalDBMu.Unlock()
	return globalDBProject
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
		CREATE TABLE IF NOT EXISTS usage_events (
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
			created_at      TEXT NOT NULL,
			FOREIGN KEY(session_id) REFERENCES sessions(id)
		);
		CREATE INDEX IF NOT EXISTS idx_usage_events_session_created
			ON usage_events(session_id, created_at DESC);
		CREATE INDEX IF NOT EXISTS idx_usage_events_project_created
			ON usage_events(project_path, created_at DESC);
		CREATE INDEX IF NOT EXISTS idx_usage_events_model_created
			ON usage_events(model, created_at DESC);
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
		CREATE TABLE IF NOT EXISTS working_state (
			session_id TEXT PRIMARY KEY,
			state      TEXT NOT NULL DEFAULT '{}',
			updated_at TEXT NOT NULL
		);
		CREATE TABLE IF NOT EXISTS project_profiles (
			project_path TEXT PRIMARY KEY,
			profile_json TEXT NOT NULL DEFAULT '{}',
			created_at   TEXT NOT NULL,
			updated_at   TEXT NOT NULL
		);
		CREATE TABLE IF NOT EXISTS memory_ledger_events (
			id            TEXT PRIMARY KEY,
			project_path  TEXT NOT NULL,
			session_id    TEXT NOT NULL DEFAULT '',
			sequence      INTEGER NOT NULL,
			event_type    TEXT NOT NULL,
			actor_model   TEXT NOT NULL DEFAULT '',
			payload_json  TEXT NOT NULL DEFAULT '{}',
			prev_hash     TEXT NOT NULL DEFAULT '',
			event_hash    TEXT NOT NULL,
			created_at    TEXT NOT NULL
		);
		CREATE UNIQUE INDEX IF NOT EXISTS idx_memory_ledger_project_sequence
			ON memory_ledger_events(project_path, sequence);
		CREATE INDEX IF NOT EXISTS idx_memory_ledger_project_created
			ON memory_ledger_events(project_path, created_at DESC);
		CREATE INDEX IF NOT EXISTS idx_memory_ledger_project_session_created
			ON memory_ledger_events(project_path, session_id, created_at DESC);
		CREATE TABLE IF NOT EXISTS memory_residual_nodes (
			project_path           TEXT NOT NULL,
			node_key               TEXT NOT NULL,
			session_id             TEXT NOT NULL DEFAULT '',
			node_type              TEXT NOT NULL,
			label                  TEXT NOT NULL DEFAULT '',
			verification_status    TEXT NOT NULL DEFAULT 'unverified',
			confidence             REAL NOT NULL DEFAULT 0,
			novelty                REAL NOT NULL DEFAULT 0,
			surprise               REAL NOT NULL DEFAULT 0,
			verification_strength  REAL NOT NULL DEFAULT 0,
			dependency_centrality  REAL NOT NULL DEFAULT 0,
			residual_score         REAL NOT NULL DEFAULT 0,
			superseded             INTEGER NOT NULL DEFAULT 0,
			contradicted           INTEGER NOT NULL DEFAULT 0,
			last_event_sequence    INTEGER NOT NULL DEFAULT 0,
			updated_at             TEXT NOT NULL,
			PRIMARY KEY(project_path, node_key)
		);
		CREATE INDEX IF NOT EXISTS idx_memory_nodes_project_score
			ON memory_residual_nodes(project_path, residual_score DESC, updated_at DESC);
		CREATE INDEX IF NOT EXISTS idx_memory_nodes_project_session_score
			ON memory_residual_nodes(project_path, session_id, residual_score DESC, updated_at DESC);
		CREATE TABLE IF NOT EXISTS memory_residual_edges (
			id                    TEXT PRIMARY KEY,
			project_path          TEXT NOT NULL,
			from_node_key         TEXT NOT NULL,
			to_node_key           TEXT NOT NULL,
			edge_type             TEXT NOT NULL,
			weight                REAL NOT NULL DEFAULT 0,
			unresolved            INTEGER NOT NULL DEFAULT 0,
			last_event_sequence   INTEGER NOT NULL DEFAULT 0,
			updated_at            TEXT NOT NULL
		);
		CREATE UNIQUE INDEX IF NOT EXISTS idx_memory_edges_unique
			ON memory_residual_edges(project_path, from_node_key, to_node_key, edge_type);
		CREATE INDEX IF NOT EXISTS idx_memory_edges_project_from
			ON memory_residual_edges(project_path, from_node_key, updated_at DESC);
		CREATE TABLE IF NOT EXISTS memory_state_snapshots (
			id               TEXT PRIMARY KEY,
			project_path     TEXT NOT NULL,
			session_id       TEXT NOT NULL DEFAULT '',
			snapshot_type    TEXT NOT NULL,
			event_sequence   INTEGER NOT NULL DEFAULT 0,
			snapshot_json    TEXT NOT NULL DEFAULT '{}',
			created_at       TEXT NOT NULL
		);
		CREATE INDEX IF NOT EXISTS idx_memory_snapshots_lookup
			ON memory_state_snapshots(project_path, session_id, snapshot_type, event_sequence DESC, created_at DESC);
	`)
	return err
}

// Session mirrors the TypeScript Session type.
type Session struct {
	ID               string `json:"id"`
	ProjectPath      string `json:"projectPath"`
	BranchName       string `json:"branchName"`
	ProjectDirection string `json:"projectDirection,omitempty"`
	Summary          string `json:"summary"`
	CreatedAt        string `json:"createdAt"`
	UpdatedAt        string `json:"updatedAt"`
	MessageCount     int    `json:"messageCount"`
}

// Message mirrors the TypeScript Message type.
type Message struct {
	ID        string `json:"id"`
	SessionID string `json:"sessionId"`
	Role      string `json:"role"`
	Content   string `json:"content"`
	ToolCalls any    `json:"toolCalls,omitempty"`
	CreatedAt string `json:"createdAt"`
}

func now() string { return time.Now().UTC().Format(time.RFC3339) }

// InsertSessionWithTimestamps inserts a session row with explicit timestamp
// strings (may be empty or non-RFC3339 for testing edge cases).
func InsertSessionWithTimestamps(id, projectPath, branchName, createdAt, updatedAt string) error {
	_, err := globalDB.Exec(
		`INSERT INTO sessions (id, project_path, branch_name, summary, created_at, updated_at) VALUES (?,?,?,'',?,?)`,
		id, projectPath, branchName, createdAt, updatedAt,
	)
	return err
}

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

func SaveMessage(id, sessionId, role, content string, toolCalls any) error {
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
			var raw any
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

func LogToolCall(id, sessionId, name string, input any, result string, isError bool, durationMs int64) error {
	if globalDB == nil {
		return fmt.Errorf("database not initialized")
	}
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
// SaveWorkingState upserts the JSON-encoded working state for a session.
func SaveWorkingState(sessionID, stateJSON string) error {
	_, err := globalDB.Exec(
		`INSERT INTO working_state (session_id, state, updated_at) VALUES (?,?,?)
		 ON CONFLICT(session_id) DO UPDATE SET state=excluded.state, updated_at=excluded.updated_at`,
		sessionID, stateJSON, now(),
	)
	return err
}

// LoadWorkingState retrieves the JSON-encoded working state for a session.
// Returns "", nil when no state has been stored yet.
func LoadWorkingState(sessionID string) (string, error) {
	var state string
	err := globalDB.QueryRow(`SELECT state FROM working_state WHERE session_id=?`, sessionID).Scan(&state)
	if err != nil {
		return "", err
	}
	return state, nil
}

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

// MemoryLedgerEvent is an immutable event persisted in the append-only Memory OS ledger.
type MemoryLedgerEvent struct {
	ID          string `json:"id"`
	ProjectPath string `json:"projectPath"`
	SessionID   string `json:"sessionId"`
	Sequence    int64  `json:"sequence"`
	EventType   string `json:"eventType"`
	ActorModel  string `json:"actorModel"`
	PayloadJSON string `json:"payloadJson"`
	PrevHash    string `json:"prevHash"`
	EventHash   string `json:"eventHash"`
	CreatedAt   string `json:"createdAt"`
}

// MemoryResidualNode stores a weighted cognition node for deterministic retrieval.
type MemoryResidualNode struct {
	NodeKey              string  `json:"nodeKey"`
	ProjectPath          string  `json:"projectPath"`
	SessionID            string  `json:"sessionId"`
	NodeType             string  `json:"nodeType"`
	Label                string  `json:"label"`
	VerificationStatus   string  `json:"verificationStatus"`
	Confidence           float64 `json:"confidence"`
	Novelty              float64 `json:"novelty"`
	Surprise             float64 `json:"surprise"`
	VerificationStrength float64 `json:"verificationStrength"`
	DependencyCentrality float64 `json:"dependencyCentrality"`
	ResidualScore        float64 `json:"residualScore"`
	Superseded           bool    `json:"superseded"`
	Contradicted         bool    `json:"contradicted"`
	LastEventSequence    int64   `json:"lastEventSequence"`
	UpdatedAt            string  `json:"updatedAt"`
}

// MemoryResidualEdge stores graph relationships between residual nodes.
type MemoryResidualEdge struct {
	ID                string  `json:"id"`
	ProjectPath       string  `json:"projectPath"`
	FromNodeKey       string  `json:"fromNodeKey"`
	ToNodeKey         string  `json:"toNodeKey"`
	EdgeType          string  `json:"edgeType"`
	Weight            float64 `json:"weight"`
	Unresolved        bool    `json:"unresolved"`
	LastEventSequence int64   `json:"lastEventSequence"`
	UpdatedAt         string  `json:"updatedAt"`
}

// MemoryStateSnapshot stores deterministic restorable state for quick memory bootstrapping.
type MemoryStateSnapshot struct {
	ID            string `json:"id"`
	ProjectPath   string `json:"projectPath"`
	SessionID     string `json:"sessionId"`
	SnapshotType  string `json:"snapshotType"`
	EventSequence int64  `json:"eventSequence"`
	SnapshotJSON  string `json:"snapshotJson"`
	CreatedAt     string `json:"createdAt"`
}

// AppendMemoryLedgerEvent appends an immutable event with hash-chain integrity.
func AppendMemoryLedgerEvent(projectPath, sessionID, eventType, actorModel string, payload any) (*MemoryLedgerEvent, error) {
	if strings.TrimSpace(projectPath) == "" {
		return nil, fmt.Errorf("projectPath required")
	}
	if strings.TrimSpace(eventType) == "" {
		return nil, fmt.Errorf("eventType required")
	}
	if globalDB == nil {
		return nil, fmt.Errorf("db not initialized")
	}

	payloadJSON := "{}"
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		payloadJSON = string(b)
	}

	tx, err := globalDB.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback() //nolint:errcheck

	var prevSeq int64
	var prevHash string
	err = tx.QueryRow(`
		SELECT sequence, event_hash
		FROM memory_ledger_events
		WHERE project_path = ?
		ORDER BY sequence DESC
		LIMIT 1
	`, projectPath).Scan(&prevSeq, &prevHash)
	if err != nil && err != sql.ErrNoRows {
		return nil, err
	}
	if err == sql.ErrNoRows {
		prevSeq = 0
		prevHash = ""
	}

	event := &MemoryLedgerEvent{
		ID:          fmt.Sprintf("mem-%d", time.Now().UnixNano()),
		ProjectPath: projectPath,
		SessionID:   strings.TrimSpace(sessionID),
		Sequence:    prevSeq + 1,
		EventType:   strings.TrimSpace(eventType),
		ActorModel:  strings.TrimSpace(actorModel),
		PayloadJSON: payloadJSON,
		PrevHash:    prevHash,
		CreatedAt:   now(),
	}

	event.EventHash = hashMemoryLedgerEvent(event)

	if _, err := tx.Exec(`
		INSERT INTO memory_ledger_events (
			id, project_path, session_id, sequence, event_type, actor_model,
			payload_json, prev_hash, event_hash, created_at
		) VALUES (?,?,?,?,?,?,?,?,?,?)
	`,
		event.ID,
		event.ProjectPath,
		event.SessionID,
		event.Sequence,
		event.EventType,
		event.ActorModel,
		event.PayloadJSON,
		event.PrevHash,
		event.EventHash,
		event.CreatedAt,
	); err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return event, nil
}

// GetMemoryLedgerEvents returns events for a project ordered by sequence ascending.
func GetMemoryLedgerEvents(projectPath string, sinceSequence int64, limit int) ([]MemoryLedgerEvent, error) {
	if strings.TrimSpace(projectPath) == "" {
		return []MemoryLedgerEvent{}, nil
	}
	if limit <= 0 {
		limit = 200
	}

	rows, err := globalDB.Query(`
		SELECT id, project_path, session_id, sequence, event_type, actor_model, payload_json, prev_hash, event_hash, created_at
		FROM memory_ledger_events
		WHERE project_path = ? AND sequence > ?
		ORDER BY sequence ASC
		LIMIT ?
	`, projectPath, sinceSequence, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	events := make([]MemoryLedgerEvent, 0, limit)
	for rows.Next() {
		var event MemoryLedgerEvent
		if err := rows.Scan(
			&event.ID,
			&event.ProjectPath,
			&event.SessionID,
			&event.Sequence,
			&event.EventType,
			&event.ActorModel,
			&event.PayloadJSON,
			&event.PrevHash,
			&event.EventHash,
			&event.CreatedAt,
		); err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	if events == nil {
		events = []MemoryLedgerEvent{}
	}
	return events, nil
}

// VerifyMemoryLedgerChain validates the per-project hash chain for all persisted events.
func VerifyMemoryLedgerChain(projectPath string) error {
	events, err := GetMemoryLedgerEvents(projectPath, 0, 1_000_000)
	if err != nil {
		return err
	}
	prevHash := ""
	var expectedSeq int64 = 1
	for _, event := range events {
		if event.Sequence != expectedSeq {
			return fmt.Errorf("memory ledger sequence gap at %d (got %d)", expectedSeq, event.Sequence)
		}
		if event.PrevHash != prevHash {
			return fmt.Errorf("memory ledger prev_hash mismatch at sequence %d", event.Sequence)
		}
		if hashMemoryLedgerEvent(&event) != event.EventHash {
			return fmt.Errorf("memory ledger hash mismatch at sequence %d", event.Sequence)
		}
		prevHash = event.EventHash
		expectedSeq++
	}
	return nil
}

// UpsertMemoryResidualNode updates the latest weighted residual state for a node.
func UpsertMemoryResidualNode(node MemoryResidualNode) error {
	if strings.TrimSpace(node.NodeKey) == "" || strings.TrimSpace(node.ProjectPath) == "" {
		return fmt.Errorf("node_key and project_path required")
	}
	if strings.TrimSpace(node.NodeType) == "" {
		node.NodeType = "claim"
	}
	if strings.TrimSpace(node.VerificationStatus) == "" {
		node.VerificationStatus = "unverified"
	}
	ts := now()
	_, err := globalDB.Exec(`
		INSERT INTO memory_residual_nodes (
			node_key, project_path, session_id, node_type, label, verification_status,
			confidence, novelty, surprise, verification_strength, dependency_centrality,
			residual_score, superseded, contradicted, last_event_sequence, updated_at
		) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(project_path, node_key) DO UPDATE SET
			project_path = excluded.project_path,
			session_id = excluded.session_id,
			node_type = excluded.node_type,
			label = excluded.label,
			verification_status = excluded.verification_status,
			confidence = excluded.confidence,
			novelty = excluded.novelty,
			surprise = excluded.surprise,
			verification_strength = excluded.verification_strength,
			dependency_centrality = excluded.dependency_centrality,
			residual_score = excluded.residual_score,
			superseded = excluded.superseded,
			contradicted = excluded.contradicted,
			last_event_sequence = excluded.last_event_sequence,
			updated_at = excluded.updated_at
	`,
		node.NodeKey,
		node.ProjectPath,
		node.SessionID,
		node.NodeType,
		node.Label,
		node.VerificationStatus,
		node.Confidence,
		node.Novelty,
		node.Surprise,
		node.VerificationStrength,
		node.DependencyCentrality,
		node.ResidualScore,
		boolToInt(node.Superseded),
		boolToInt(node.Contradicted),
		node.LastEventSequence,
		ts,
	)
	return err
}

// UpsertMemoryResidualEdge updates/creates a graph edge between two nodes.
func UpsertMemoryResidualEdge(edge MemoryResidualEdge) error {
	if strings.TrimSpace(edge.ProjectPath) == "" || strings.TrimSpace(edge.FromNodeKey) == "" || strings.TrimSpace(edge.ToNodeKey) == "" {
		return fmt.Errorf("project_path, from_node_key, and to_node_key required")
	}
	if strings.TrimSpace(edge.EdgeType) == "" {
		edge.EdgeType = "depends_on"
	}
	if strings.TrimSpace(edge.ID) == "" {
		edge.ID = fmt.Sprintf("edge-%d", time.Now().UnixNano())
	}

	ts := now()
	_, err := globalDB.Exec(`
		INSERT INTO memory_residual_edges (
			id, project_path, from_node_key, to_node_key, edge_type, weight, unresolved, last_event_sequence, updated_at
		) VALUES (?,?,?,?,?,?,?,?,?)
		ON CONFLICT(project_path, from_node_key, to_node_key, edge_type) DO UPDATE SET
			weight = excluded.weight,
			unresolved = excluded.unresolved,
			last_event_sequence = excluded.last_event_sequence,
			updated_at = excluded.updated_at
	`,
		edge.ID,
		edge.ProjectPath,
		edge.FromNodeKey,
		edge.ToNodeKey,
		edge.EdgeType,
		edge.Weight,
		boolToInt(edge.Unresolved),
		edge.LastEventSequence,
		ts,
	)
	return err
}

// ListTopMemoryResidualNodes returns highest-scoring unresolved/supported nodes for retrieval.
func ListTopMemoryResidualNodes(projectPath, sessionID string, limit int) ([]MemoryResidualNode, error) {
	if limit <= 0 {
		limit = 20
	}

	query := `
		SELECT node_key, project_path, session_id, node_type, label, verification_status,
			confidence, novelty, surprise, verification_strength, dependency_centrality,
			residual_score, superseded, contradicted, last_event_sequence, updated_at
		FROM memory_residual_nodes
		WHERE project_path = ?`
	args := []any{projectPath}
	if strings.TrimSpace(sessionID) != "" {
		query += ` AND (session_id = ? OR session_id = '')`
		args = append(args, strings.TrimSpace(sessionID))
	}
	query += ` ORDER BY residual_score DESC, updated_at DESC LIMIT ?`
	args = append(args, limit)

	rows, err := globalDB.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	nodes := make([]MemoryResidualNode, 0, limit)
	for rows.Next() {
		var node MemoryResidualNode
		var superseded int
		var contradicted int
		if err := rows.Scan(
			&node.NodeKey,
			&node.ProjectPath,
			&node.SessionID,
			&node.NodeType,
			&node.Label,
			&node.VerificationStatus,
			&node.Confidence,
			&node.Novelty,
			&node.Surprise,
			&node.VerificationStrength,
			&node.DependencyCentrality,
			&node.ResidualScore,
			&superseded,
			&contradicted,
			&node.LastEventSequence,
			&node.UpdatedAt,
		); err != nil {
			return nil, err
		}
		node.Superseded = superseded == 1
		node.Contradicted = contradicted == 1
		nodes = append(nodes, node)
	}
	if nodes == nil {
		nodes = []MemoryResidualNode{}
	}
	return nodes, nil
}

// SaveMemoryStateSnapshot persists a deterministic replay/restoration snapshot.
func SaveMemoryStateSnapshot(snapshot MemoryStateSnapshot) error {
	if strings.TrimSpace(snapshot.ProjectPath) == "" || strings.TrimSpace(snapshot.SnapshotType) == "" {
		return fmt.Errorf("project_path and snapshot_type required")
	}
	if strings.TrimSpace(snapshot.ID) == "" {
		snapshot.ID = fmt.Sprintf("snapshot-%d", time.Now().UnixNano())
	}
	if strings.TrimSpace(snapshot.SnapshotJSON) == "" {
		snapshot.SnapshotJSON = "{}"
	}
	if strings.TrimSpace(snapshot.CreatedAt) == "" {
		snapshot.CreatedAt = now()
	}

	_, err := globalDB.Exec(`
		INSERT INTO memory_state_snapshots (
			id, project_path, session_id, snapshot_type, event_sequence, snapshot_json, created_at
		) VALUES (?,?,?,?,?,?,?)
	`,
		snapshot.ID,
		snapshot.ProjectPath,
		snapshot.SessionID,
		snapshot.SnapshotType,
		snapshot.EventSequence,
		snapshot.SnapshotJSON,
		snapshot.CreatedAt,
	)
	return err
}

// LoadLatestMemoryStateSnapshot returns the latest matching snapshot if present.
func LoadLatestMemoryStateSnapshot(projectPath, sessionID, snapshotType string) (*MemoryStateSnapshot, error) {
	query := `
		SELECT id, project_path, session_id, snapshot_type, event_sequence, snapshot_json, created_at
		FROM memory_state_snapshots
		WHERE project_path = ? AND snapshot_type = ?`
	args := []any{projectPath, snapshotType}
	if strings.TrimSpace(sessionID) != "" {
		query += ` AND session_id = ?`
		args = append(args, sessionID)
	}
	query += ` ORDER BY event_sequence DESC, created_at DESC LIMIT 1`

	row := globalDB.QueryRow(query, args...)
	var snapshot MemoryStateSnapshot
	err := row.Scan(
		&snapshot.ID,
		&snapshot.ProjectPath,
		&snapshot.SessionID,
		&snapshot.SnapshotType,
		&snapshot.EventSequence,
		&snapshot.SnapshotJSON,
		&snapshot.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &snapshot, nil
}

func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func hashMemoryLedgerEvent(event *MemoryLedgerEvent) string {
	payload := strings.TrimSpace(event.PayloadJSON)
	if payload == "" {
		payload = "{}"
	}
	material := strings.Join([]string{
		event.ProjectPath,
		event.SessionID,
		fmt.Sprintf("%d", event.Sequence),
		event.EventType,
		event.ActorModel,
		payload,
		event.PrevHash,
		event.CreatedAt,
	}, "|")
	sum := sha256.Sum256([]byte(material))
	return fmt.Sprintf("%x", sum[:])
}
