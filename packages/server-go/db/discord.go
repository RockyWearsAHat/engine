package db

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// DiscordMessage represents a single archived Discord message.
type DiscordMessage struct {
	ID          string `json:"id"`
	ProjectPath string `json:"projectPath"`
	ChannelID   string `json:"channelId"`
	ThreadID    string `json:"threadId"`
	SessionID   string `json:"sessionId"`
	AuthorID    string `json:"authorId"`
	AuthorName  string `json:"authorName"`
	Direction   string `json:"direction"` // "in" | "out"
	Kind        string `json:"kind"`      // "message" | "command" | "agent"
	Content     string `json:"content"`
	CreatedAt   string `json:"createdAt"`
}

// DiscordSessionThread maps an AI session to its Discord thread.
type DiscordSessionThread struct {
	SessionID   string `json:"sessionId"`
	ProjectPath string `json:"projectPath"`
	ThreadID    string `json:"threadId"`
	ChannelID   string `json:"channelId"`
	CreatedAt   string `json:"createdAt"`
}

// DiscordSearchHit is a search result row with FTS snippet.
type DiscordSearchHit struct {
	DiscordMessage
	Snippet string `json:"snippet"`
}

// DiscordRecordMessage archives one Discord message for later search.
// Content length is capped to 8 KiB to keep the FTS index compact; the caller
// is expected to persist any larger context elsewhere.
func DiscordRecordMessage(m DiscordMessage) error {
	if globalDB == nil {
		return errors.New("db not initialized")
	}
	if strings.TrimSpace(m.ID) == "" {
		m.ID = fmt.Sprintf("dm-%d", time.Now().UnixNano())
	}
	if strings.TrimSpace(m.CreatedAt) == "" {
		m.CreatedAt = now()
	}
	if strings.TrimSpace(m.Direction) == "" {
		m.Direction = "in"
	}
	if strings.TrimSpace(m.Kind) == "" {
		m.Kind = "message"
	}
	const maxContent = 8 * 1024
	content := m.Content
	if len(content) > maxContent {
		content = content[:maxContent]
	}
	_, err := globalDB.Exec(
		`INSERT INTO discord_messages
			(id, project_path, channel_id, thread_id, session_id, author_id, author_name, direction, kind, content, created_at)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		m.ID, m.ProjectPath, m.ChannelID, m.ThreadID, m.SessionID, m.AuthorID, m.AuthorName, m.Direction, m.Kind, content, m.CreatedAt,
	)
	return err
}

// DiscordBindSessionThread records a stable session ↔ thread mapping.
// Upserts so reopening a thread reuses the same session.
func DiscordBindSessionThread(sessionID, projectPath, threadID, channelID string) error {
	if globalDB == nil {
		return errors.New("db not initialized")
	}
	_, err := globalDB.Exec(
		`INSERT INTO discord_session_threads (session_id, project_path, thread_id, channel_id, created_at)
		 VALUES (?,?,?,?,?)
		 ON CONFLICT(session_id) DO UPDATE SET
			project_path = excluded.project_path,
			thread_id    = excluded.thread_id,
			channel_id   = excluded.channel_id`,
		sessionID, projectPath, threadID, channelID, now(),
	)
	return err
}

// DiscordGetSessionByThread returns the session bound to a given thread, if any.
func DiscordGetSessionByThread(threadID string) (*DiscordSessionThread, error) {
	if globalDB == nil {
		return nil, errors.New("db not initialized")
	}
	row := globalDB.QueryRow(
		`SELECT session_id, project_path, thread_id, channel_id, created_at
		   FROM discord_session_threads WHERE thread_id = ?`,
		threadID,
	)
	var r DiscordSessionThread
	if err := row.Scan(&r.SessionID, &r.ProjectPath, &r.ThreadID, &r.ChannelID, &r.CreatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &r, nil
}

// DiscordSearchMessages runs a bounded full-text query. `limit` is capped
// server-side to prevent dumping unbounded history into a caller.
//
// The `since` argument, if non-empty, filters results to after that RFC3339
// timestamp. Pass an empty string to search across all history.
func DiscordSearchMessages(projectPath, query, since string, limit int) ([]DiscordSearchHit, error) {
	if globalDB == nil {
		return nil, errors.New("db not initialized")
	}
	if limit <= 0 || limit > 50 {
		limit = 25
	}
	q := strings.TrimSpace(query)
	if q == "" {
		return nil, errors.New("query is required")
	}

	args := []any{q}
	sqlStr := strings.Builder{}
	sqlStr.WriteString(`
		SELECT m.id, m.project_path, m.channel_id, m.thread_id, m.session_id,
		       m.author_id, m.author_name, m.direction, m.kind, m.content, m.created_at,
		       snippet(discord_messages_fts, 0, '[[', ']]', '…', 12) AS snippet
		  FROM discord_messages_fts f
		  JOIN discord_messages m ON m.rowid = f.rowid
		 WHERE discord_messages_fts MATCH ?`)
	if strings.TrimSpace(projectPath) != "" {
		sqlStr.WriteString(" AND m.project_path = ?")
		args = append(args, projectPath)
	}
	if strings.TrimSpace(since) != "" {
		sqlStr.WriteString(" AND m.created_at >= ?")
		args = append(args, since)
	}
	sqlStr.WriteString(" ORDER BY m.created_at DESC LIMIT ?")
	args = append(args, limit)

	rows, err := globalDB.Query(sqlStr.String(), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []DiscordSearchHit
	for rows.Next() {
		var h DiscordSearchHit
		if err := rows.Scan(
			&h.ID, &h.ProjectPath, &h.ChannelID, &h.ThreadID, &h.SessionID,
			&h.AuthorID, &h.AuthorName, &h.Direction, &h.Kind, &h.Content, &h.CreatedAt,
			&h.Snippet,
		); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	if out == nil {
		out = []DiscordSearchHit{}
	}
	return out, nil
}

// DiscordListRecentMessages returns recent archived messages for a project or
// thread, optionally filtered by a since timestamp. Callers that need more
// than `limit` results must page by created_at.
func DiscordListRecentMessages(projectPath, threadID, since string, limit int) ([]DiscordMessage, error) {
	if globalDB == nil {
		return nil, errors.New("db not initialized")
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}

	args := []any{}
	sb := strings.Builder{}
	sb.WriteString(`
		SELECT id, project_path, channel_id, thread_id, session_id,
		       author_id, author_name, direction, kind, content, created_at
		  FROM discord_messages
		 WHERE 1=1`)
	if strings.TrimSpace(projectPath) != "" {
		sb.WriteString(" AND project_path = ?")
		args = append(args, projectPath)
	}
	if strings.TrimSpace(threadID) != "" {
		sb.WriteString(" AND thread_id = ?")
		args = append(args, threadID)
	}
	if strings.TrimSpace(since) != "" {
		sb.WriteString(" AND created_at >= ?")
		args = append(args, since)
	}
	sb.WriteString(" ORDER BY created_at DESC LIMIT ?")
	args = append(args, limit)

	rows, err := globalDB.Query(sb.String(), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []DiscordMessage
	for rows.Next() {
		var m DiscordMessage
		if err := rows.Scan(
			&m.ID, &m.ProjectPath, &m.ChannelID, &m.ThreadID, &m.SessionID,
			&m.AuthorID, &m.AuthorName, &m.Direction, &m.Kind, &m.Content, &m.CreatedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	if out == nil {
		out = []DiscordMessage{}
	}
	return out, nil
}
