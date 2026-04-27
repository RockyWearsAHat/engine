package github

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
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

// ── ProfilePoller ─────────────────────────────────────────────────────────────

// profileRepoLister abstracts the GitHub API call so tests can stub it.
type profileRepoLister interface {
	ListUserRepos(perPage int) ([]UserRepo, error)
	GetAuthenticatedLogin() (string, error)
}

// profileHTTPGet abstracts fetching a raw URL (used for README reads).
var profileHTTPGet = func(url, token string) ([]byte, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github.raw+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, nil // no README is fine
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("fetch README returned %d", resp.StatusCode)
	}
	buf := make([]byte, 1<<20) // 1 MB cap
	n, _ := resp.Body.Read(buf)
	return buf[:n], nil
}

// ProfilePoller polls the authenticated user's GitHub repositories for changes
// to README files that contain the @engine tag.  It synthesises webhook-style
// push payloads and forwards them to a RepoMonitor so the existing trigger
// pipeline is reused without modification.
//
// Required environment variable: GITHUB_TOKEN.
// Optional: ENGINE_PROFILE_POLL_INTERVAL — duration string (default "5m").
type ProfilePoller struct {
	token    string
	interval time.Duration
	lister   profileRepoLister
	monitor  *RepoMonitor

	mu      sync.Mutex
	seen    map[string]bool // full_name → README contained @engine last poll
}

// NewProfilePoller creates a ProfilePoller that forwards events to monitor.
// The token must be a valid GitHub personal access token with repo scope.
// Pass interval ≤ 0 to use the default (5 minutes).
func NewProfilePoller(token string, interval time.Duration, monitor *RepoMonitor) *ProfilePoller {
	if interval <= 0 {
		interval = 5 * time.Minute
	}
	return &ProfilePoller{
		token:    token,
		interval: interval,
		lister:   NewProfileClient(token),
		monitor:  monitor,
		seen:     make(map[string]bool),
	}
}

// NewProfilePollerFromEnv creates a ProfilePoller driven by environment variables.
// Returns nil (and logs) when GITHUB_TOKEN is absent.
func NewProfilePollerFromEnv(monitor *RepoMonitor) *ProfilePoller {
	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		return nil
	}
	interval := 5 * time.Minute
	if raw := os.Getenv("ENGINE_PROFILE_POLL_INTERVAL"); raw != "" {
		if d, err := time.ParseDuration(raw); err == nil {
			interval = d
		} else {
			log.Printf("profile-poller: invalid ENGINE_PROFILE_POLL_INTERVAL %q, using 5m", raw)
		}
	}
	return NewProfilePoller(token, interval, monitor)
}

// Start begins the polling loop.  The first poll runs immediately.
func (p *ProfilePoller) Start(ctx context.Context) {
	go func() {
		p.poll()
		ticker := time.NewTicker(p.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				p.poll()
			}
		}
	}()
}

// poll fetches the user's repo list and fires OnReadmeChange for any repo whose
// README contains @engine for the first time (or begins containing it again).
func (p *ProfilePoller) poll() {
	repos, err := p.lister.ListUserRepos(100)
	if err != nil {
		log.Printf("profile-poller: list repos: %v", err)
		return
	}

	for _, repo := range repos {
		hasTag, err := p.readmeHasEngineTag(repo)
		if err != nil {
			log.Printf("profile-poller: check README %s: %v", repo.FullName, err)
			continue
		}

		p.mu.Lock()
		wasTagged := p.seen[repo.FullName]
		p.seen[repo.FullName] = hasTag
		p.mu.Unlock()

		if hasTag && !wasTagged {
			// Synthesise a minimal push payload so the existing pipeline fires.
			log.Printf("profile-poller: @engine tag detected in %s — triggering scaffold", repo.FullName)
			payload, _ := json.Marshal(map[string]any{
				"ref": "refs/heads/" + repo.DefaultBranch,
				"repository": map[string]any{
					"full_name":      repo.FullName,
					"default_branch": repo.DefaultBranch,
				},
				"commits": []map[string]any{
					{"id": "poll", "message": "profile-poll", "added": []string{"README.md"}, "modified": []string{}, "removed": []string{}},
				},
			})
			if p.monitor.OnReadmeChange != nil {
				p.monitor.OnReadmeChange(json.RawMessage(payload))
			}
		}
	}
}

// readmeHasEngineTag fetches the README for repo and reports whether it
// contains the @engine tag.
func (p *ProfilePoller) readmeHasEngineTag(repo UserRepo) (bool, error) {
	branch := repo.DefaultBranch
	if branch == "" {
		branch = "HEAD"
	}
	url := fmt.Sprintf("https://raw.githubusercontent.com/%s/%s/README.md",
		repo.FullName, branch)
	data, err := profileHTTPGet(url, p.token)
	if err != nil {
		return false, err
	}
	return strings.Contains(string(data), "@engine"), nil
}
