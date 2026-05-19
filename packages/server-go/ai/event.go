package ai

import (
	"sync"
	"time"
)

// EventType identifies the kind of event.
type EventType string

const (
	// Intake phase events
	EventDesignReady EventType = "design_ready"
	EventPRDReady    EventType = "prd_ready"

	// Planning events
	EventPlanReady    EventType = "plan_ready"
	EventTeamStarted  EventType = "team_started"
	EventTeamUpdated  EventType = "team_updated"
	EventTeamDone     EventType = "team_done"
	EventTeamBlocked  EventType = "team_blocked"
	EventTeamFailed   EventType = "team_failed"

	// Validation events
	EventValidationStarted EventType = "validation_started"
	EventValidationPassed  EventType = "validation_passed"
	EventValidationFailed  EventType = "validation_failed"

	// Control events
	EventUserRedirect EventType = "user_redirect"
	EventCancel       EventType = "cancel"
	EventPause        EventType = "pause"
	EventResume       EventType = "resume"

	// Terminal events
	EventProjectDone     EventType = "project_done"
	EventProjectFailed   EventType = "project_failed"
	EventProjectCanceled EventType = "project_canceled"
)

// Event is a change notification flowing through the orchestrator.
type Event struct {
	Type      EventType              `json:"type"`
	Timestamp time.Time              `json:"timestamp"`
	Payload   map[string]interface{} `json:"payload,omitempty"`
	TeamID    string                 `json:"team_id,omitempty"`
}

// EventBus is a simple pub/sub for orchestrator events.
type EventBus struct {
	mu        sync.RWMutex
	listeners map[EventType][]chan Event
	closed    bool
}

func NewEventBus() *EventBus {
	return &EventBus{
		listeners: make(map[EventType][]chan Event),
	}
}

// Subscribe registers a listener for events of a specific type.
// The returned channel must be consumed; sender will block if not.
// Close the channel to unsubscribe.
func (b *EventBus) Subscribe(eventType EventType, bufSize int) <-chan Event {
	b.mu.Lock()
	defer b.mu.Unlock()

	ch := make(chan Event, bufSize)
	b.listeners[eventType] = append(b.listeners[eventType], ch)
	return ch
}

// Emit publishes an event to all subscribers.
func (b *EventBus) Emit(ev Event) {
	b.mu.RLock()
	listeners := b.listeners[ev.Type]
	b.mu.RUnlock()

	// Non-blocking emit: drop if subscriber is slow.
	for _, ch := range listeners {
		select {
		case ch <- ev:
		default:
		}
	}
}

// Close closes the bus (optional; prevents further emissions).
func (b *EventBus) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.closed = true
}

// Helper to build event payload easily.
func EventPayload(pairs ...interface{}) map[string]interface{} {
	m := make(map[string]interface{})
	for i := 0; i < len(pairs); i += 2 {
		if i+1 < len(pairs) {
			m[pairs[i].(string)] = pairs[i+1]
		}
	}
	return m
}
