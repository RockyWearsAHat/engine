package github

import (
	"context"
	"encoding/json"
	"log"
	"strings"
	"sync"
	"time"
)

// MonitoredEvent is a de-serialized event stored for processing.
type MonitoredEvent struct {
	Type      string
	Delivery  string
	Payload   json.RawMessage
	Received  time.Time
	Processed bool
}

// RepoMonitor processes webhook events from a background queue.
// It is the single consumer of events enqueued by WebhookReceiver.
type RepoMonitor struct {
	mu           sync.Mutex
	queue        []*MonitoredEvent
	notifyCh     chan struct{}
	tickInterval time.Duration // 0 means default (10s)

	// OnReadmeChange is called when a push touches README.md.
	// Receives the full push payload JSON.
	OnReadmeChange func(payload json.RawMessage)
	// OnCIFailure is called when a workflow run completes with "failure".
	OnCIFailure func(payload json.RawMessage)
	// OnIssueComment is called for new issue comments.
	OnIssueComment func(payload json.RawMessage)
	// OnIssueOpened is called when a new issue is opened.
	OnIssueOpened func(payload json.RawMessage)
}

// NewRepoMonitor creates a RepoMonitor. Call Start() to begin processing.
func NewRepoMonitor() *RepoMonitor {
	return &RepoMonitor{
		notifyCh: make(chan struct{}, 1),
	}
}

// Enqueue is called by the WebhookReceiver to add an incoming event.
func (m *RepoMonitor) Enqueue(event *WebhookEvent) {
	m.mu.Lock()
	m.queue = append(m.queue, &MonitoredEvent{
		Type:     event.Type,
		Delivery: event.Delivery,
		Payload:  event.Payload,
		Received: time.Now(),
	})
	m.mu.Unlock()

	// Non-blocking notify.
	select {
	case m.notifyCh <- struct{}{}:
	default:
	}
}

// Start begins the background event processing loop.
// Call with a context that is cancelled on shutdown.
func (m *RepoMonitor) Start(ctx context.Context) {
	interval := m.tickInterval
	if interval <= 0 {
		interval = 10 * time.Second
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-m.notifyCh:
				m.process()
			case <-ticker.C:
				m.process() // periodic drain in case notify was missed
			}
		}
	}()
}

// process drains the queue and dispatches events to registered handlers.
func (m *RepoMonitor) process() {
	m.mu.Lock()
	pending := m.queue
	m.queue = nil
	m.mu.Unlock()

	for _, ev := range pending {
		m.dispatch(ev)
		ev.Processed = true
	}
}

// dispatch routes an event to the appropriate handler.
func (m *RepoMonitor) dispatch(ev *MonitoredEvent) {
	switch strings.ToLower(ev.Type) {
	case "push":
		p, err := ParsePush(&WebhookEvent{Type: ev.Type, Delivery: ev.Delivery, Payload: ev.Payload})
		if err != nil {
			log.Printf("monitor: parse push: %v", err)
			return
		}
		if p.TouchesReadme() && m.OnReadmeChange != nil {
			m.OnReadmeChange(ev.Payload)
		}

	case "workflow_run":
		p, err := ParseWorkflowRun(&WebhookEvent{Type: ev.Type, Delivery: ev.Delivery, Payload: ev.Payload})
		if err != nil {
			log.Printf("monitor: parse workflow_run: %v", err)
			return
		}
		if p.Action == "completed" &&
			p.Conclusion == "failure" &&
			m.OnCIFailure != nil {
			m.OnCIFailure(ev.Payload)
		}

	case "issue_comment":
		if m.OnIssueComment != nil {
			m.OnIssueComment(ev.Payload)
		}

	case "issues":
		p, err := ParseIssue(&WebhookEvent{Type: ev.Type, Delivery: ev.Delivery, Payload: ev.Payload})
		if err != nil {
			log.Printf("monitor: parse issues: %v", err)
			return
		}
		if p.Action == "opened" && m.OnIssueOpened != nil {
			m.OnIssueOpened(ev.Payload)
		}

	default:
		// Unhandled event type — log and ignore.
		log.Printf("monitor: unhandled event type %q (delivery %s)", ev.Type, ev.Delivery)
	}
}
