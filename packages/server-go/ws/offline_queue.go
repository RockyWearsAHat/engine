// Package ws — offline message queue for mobile resilience.
// When the WebSocket connection drops, outgoing AI response chunks and tool
// results are buffered here. On reconnect, the queue is replayed in order,
// deduplicating messages that the client already received.
package ws

import (
	"encoding/json"
	"sync"
	"time"
)

const (
	// maxQueueSize caps the number of buffered messages to prevent unbounded growth.
	maxQueueSize = 2_000
	// maxQueueAge is how long messages are retained in the offline queue.
	// Messages older than this are dropped on the next prune.
	maxQueueAge = 30 * time.Minute
)

// QueuedMessage is a single buffered WebSocket message.
type QueuedMessage struct {
	ID        string          `json:"id"`        // unique per message, set by sender
	SessionID string          `json:"session_id"` // which client session this belongs to
	Payload   json.RawMessage `json:"payload"`   // the raw JSON to send
	QueuedAt  time.Time       `json:"queued_at"` // when it entered the queue
}

// OfflineQueue buffers messages for clients that have temporarily disconnected.
// It is safe for concurrent use.
type OfflineQueue struct {
	mu       sync.Mutex
	messages []*QueuedMessage
	seen     map[string]struct{} // message IDs already sent
}

// NewOfflineQueue creates an empty OfflineQueue.
func NewOfflineQueue() *OfflineQueue {
	return &OfflineQueue{
		seen: make(map[string]struct{}),
	}
}

// Enqueue adds a message to the queue.
// If the queue is at capacity, the oldest message is dropped.
// If msgID is already in the seen set (already delivered), the message is skipped.
func (q *OfflineQueue) Enqueue(msg *QueuedMessage) {
	q.mu.Lock()
	defer q.mu.Unlock()

	if _, dup := q.seen[msg.ID]; dup {
		return // already delivered
	}

	if len(q.messages) >= maxQueueSize {
		// Drop oldest.
		q.messages = q.messages[1:]
	}
	q.messages = append(q.messages, msg)
}

// Drain returns all buffered messages for the given sessionID in FIFO order,
// removes them from the queue, and marks their IDs as seen to prevent re-delivery.
// Messages older than maxQueueAge are discarded during drain.
func (q *OfflineQueue) Drain(sessionID string) []*QueuedMessage {
	q.mu.Lock()
	defer q.mu.Unlock()

	now := time.Now()
	var result []*QueuedMessage
	var remaining []*QueuedMessage

	for _, m := range q.messages {
		if now.Sub(m.QueuedAt) > maxQueueAge {
			continue // expired — drop silently
		}
		if m.SessionID != sessionID {
			remaining = append(remaining, m)
			continue
		}
		result = append(result, m)
		q.seen[m.ID] = struct{}{} // mark as delivered
	}

	q.messages = remaining
	return result
}

// Prune removes expired messages from the queue.
// Call periodically (e.g., every 5 minutes) to prevent unbounded growth.
func (q *OfflineQueue) Prune() {
	q.mu.Lock()
	defer q.mu.Unlock()

	cutoff := time.Now().Add(-maxQueueAge)
	var kept []*QueuedMessage
	for _, m := range q.messages {
		if m.QueuedAt.After(cutoff) {
			kept = append(kept, m)
		}
	}
	q.messages = kept
}

// Len returns the current number of buffered messages (all sessions combined).
func (q *OfflineQueue) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.messages)
}
