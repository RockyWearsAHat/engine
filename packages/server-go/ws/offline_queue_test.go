package ws

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"
)

// makeMsg is a helper that builds a QueuedMessage with the given session and id.
func makeMsg(id, sessionID string) *QueuedMessage {
	payload, _ := json.Marshal(map[string]string{"id": id})
	return &QueuedMessage{
		ID:        id,
		SessionID: sessionID,
		Payload:   payload,
		QueuedAt:  time.Now(),
	}
}

func TestOfflineQueue_Enqueue_Basic(t *testing.T) {
	q := NewOfflineQueue()
	q.Enqueue(makeMsg("m1", "sess1"))
	if q.Len() != 1 {
		t.Fatalf("expected 1 message, got %d", q.Len())
	}
}

func TestOfflineQueue_Enqueue_SkipsDuplicateAlreadySeen(t *testing.T) {
	q := NewOfflineQueue()
	q.Enqueue(makeMsg("m1", "sess1"))
	// Drain to mark m1 as seen.
	q.Drain("sess1")
	// Re-enqueue the same message id — should be rejected because it's already in seen.
	q.Enqueue(makeMsg("m1", "sess1"))
	// Drain again — should return nothing.
	result := q.Drain("sess1")
	if len(result) != 0 {
		t.Fatalf("expected no messages for already-seen id, got %d", len(result))
	}
}

func TestOfflineQueue_Drain_ReturnsOnlyMatchingSession(t *testing.T) {
	q := NewOfflineQueue()
	q.Enqueue(makeMsg("m1", "sess1"))
	q.Enqueue(makeMsg("m2", "sess2"))
	q.Enqueue(makeMsg("m3", "sess1"))

	result := q.Drain("sess1")
	if len(result) != 2 {
		t.Fatalf("expected 2 messages for sess1, got %d", len(result))
	}
	for _, m := range result {
		if m.SessionID != "sess1" {
			t.Errorf("expected only sess1 messages, got session %q", m.SessionID)
		}
	}
	// sess2's message must still be in the queue.
	if q.Len() != 1 {
		t.Fatalf("expected 1 remaining message after draining sess1, got %d", q.Len())
	}
}

func TestOfflineQueue_Drain_FIFOOrder(t *testing.T) {
	q := NewOfflineQueue()
	q.Enqueue(makeMsg("first", "sess1"))
	q.Enqueue(makeMsg("second", "sess1"))
	q.Enqueue(makeMsg("third", "sess1"))

	result := q.Drain("sess1")
	if len(result) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(result))
	}
	if result[0].ID != "first" || result[1].ID != "second" || result[2].ID != "third" {
		t.Errorf("expected FIFO order, got %q %q %q", result[0].ID, result[1].ID, result[2].ID)
	}
}

func TestOfflineQueue_Drain_MarksAsSeenPreventsRedelivery(t *testing.T) {
	q := NewOfflineQueue()
	q.Enqueue(makeMsg("m1", "sess1"))
	first := q.Drain("sess1")
	if len(first) != 1 {
		t.Fatalf("expected 1 message on first drain, got %d", len(first))
	}
	// Re-enqueue same message — drain should not return it again.
	q.Enqueue(makeMsg("m1", "sess1"))
	second := q.Drain("sess1")
	if len(second) != 0 {
		t.Fatalf("expected 0 messages after re-enqueue of seen id, got %d", len(second))
	}
}

func TestOfflineQueue_Drain_DiscardsExpiredMessages(t *testing.T) {
	q := NewOfflineQueue()
	old := makeMsg("old", "sess1")
	old.QueuedAt = time.Now().Add(-(maxQueueAge + time.Second))
	q.Enqueue(old)

	result := q.Drain("sess1")
	if len(result) != 0 {
		t.Fatalf("expected expired message to be discarded, got %d messages", len(result))
	}
	// Queue should be empty (the old message was the only one).
	if q.Len() != 0 {
		t.Fatalf("expected queue length 0 after draining expired, got %d", q.Len())
	}
}

func TestOfflineQueue_Prune_DropsExpiredMessages(t *testing.T) {
	q := NewOfflineQueue()
	old := makeMsg("old", "sess1")
	old.QueuedAt = time.Now().Add(-(maxQueueAge + time.Second))
	q.Enqueue(old)

	fresh := makeMsg("fresh", "sess1")
	q.Enqueue(fresh)

	q.Prune()

	if q.Len() != 1 {
		t.Fatalf("expected 1 message after prune (only fresh), got %d", q.Len())
	}
	// The remaining message must be the fresh one.
	result := q.Drain("sess1")
	if len(result) != 1 || result[0].ID != "fresh" {
		t.Errorf("expected fresh message to survive prune, got %+v", result)
	}
}

func TestOfflineQueue_Enqueue_CapsAtMaxQueueSize(t *testing.T) {
	q := NewOfflineQueue()
	for i := range maxQueueSize + 10 {
		q.Enqueue(makeMsg(fmt.Sprintf("m%d", i), "sess1"))
	}
	if q.Len() != maxQueueSize {
		t.Fatalf("expected queue capped at %d, got %d", maxQueueSize, q.Len())
	}
}

func TestOfflineQueue_Enqueue_DropsOldestWhenAtCapacity(t *testing.T) {
	q := NewOfflineQueue()
	for i := range maxQueueSize {
		q.Enqueue(makeMsg(fmt.Sprintf("m%d", i), "sess1"))
	}
	// This message should evict m0 (the oldest).
	q.Enqueue(makeMsg("new-tail", "sess1"))

	result := q.Drain("sess1")
	// m0 should be gone.
	for _, m := range result {
		if m.ID == "m0" {
			t.Fatal("m0 (oldest) should have been evicted when queue was at capacity")
		}
	}
	// new-tail should be present.
	var found bool
	for _, m := range result {
		if m.ID == "new-tail" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("new-tail should be in the queue after capacity eviction")
	}
}

func TestOfflineQueue_Len_ReturnsAllSessions(t *testing.T) {
	q := NewOfflineQueue()
	q.Enqueue(makeMsg("a", "sess1"))
	q.Enqueue(makeMsg("b", "sess2"))
	q.Enqueue(makeMsg("c", "sess3"))
	if q.Len() != 3 {
		t.Fatalf("expected Len() = 3, got %d", q.Len())
	}
}
