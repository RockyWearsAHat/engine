// Package github — EventsWatcher for near-real-time GitHub event detection.
//
// Uses the GitHub user events API with ETag conditional requests so that a
// 304 Not Modified response (no new events) consumes zero rate-limit quota and
// returns instantly.  GitHub's X-Poll-Interval header tells us the minimum
// inter-request interval (typically 60 s for authenticated requests).
//
// This replaces the time-based ProfilePoller: instead of downloading every
// repo's README every N minutes we only check the README when a relevant event
// (PushEvent touching README, or CreateEvent for a new repository) arrives.
// An initial full-repo scan on startup catches repos that already carry @engine.
package github

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// eventEntry is a single item from GET /users/{login}/events.
type eventEntry struct {
	ID   string `json:"id"`
	Type string `json:"type"`
	Repo struct {
		Name string `json:"name"` // "owner/repo"
	} `json:"repo"`
	Payload json.RawMessage `json:"payload"`
}

// eventsHTTPClient is used for the events API; exposed for testing.
var eventsHTTPClient = &http.Client{Timeout: 30 * time.Second}

// EventsWatcher monitors the authenticated user's GitHub event stream for
// README changes and new repositories that contain the @engine tag.
//
// Required env var: GITHUB_TOKEN (repo + read:user scopes).
type EventsWatcher struct {
	token   string
	monitor *RepoMonitor

	mu                         sync.Mutex
	startedAt                  time.Time
	etag                       string
	seen                       map[string]bool // full_name → README contained @engine last check
	processed                  map[string]bool // event.ID → already dispatched
	processedOrder             []string        // FIFO of processed IDs (bounded to ~1k)
	processedIssueComments     map[int64]bool
	processedIssueCommentOrder []int64

	// Injectable for tests.
	// Injectable for tests.
	tickFn        func(d time.Duration) <-chan time.Time // defaults to time.After
	loginFn       func(token string) (string, error)
	listReposFn   func(token string, perPage int) ([]UserRepo, error)
	fetchEventsFn func(token, login, etag string) (events []eventEntry, newEtag string, pollSecs int, unchanged bool, err error)
	fetchReadmeFn func(fullName, branch, token string) ([]byte, error)
}

// NewEventsWatcher creates a watcher that forwards @engine triggers to monitor.
func NewEventsWatcher(token string, monitor *RepoMonitor) *EventsWatcher {
	return &EventsWatcher{
		token:                  token,
		monitor:                monitor,
		startedAt:              time.Now().UTC(),
		seen:                   make(map[string]bool),
		processed:              make(map[string]bool),
		processedIssueComments: make(map[int64]bool),
		tickFn:                 time.After,
		loginFn:                defaultEventsLoginFn,
		listReposFn:            defaultEventsListReposFn,
		fetchEventsFn:          defaultFetchEventsFn,
		fetchReadmeFn:          defaultEventsReadmeFn,
	}
}

// maxProcessedEventIDs bounds the dedup set so it doesn't grow unbounded over
// a long-running watcher session. GitHub's events API returns ~30 events per
// page; keeping 1024 IDs is well past anything we'd see in one poll burst.
const maxProcessedEventIDs = 1024
const maxProcessedIssueCommentIDs = 2048
const issueCommentFreshnessSkew = 2 * time.Minute

// markEventProcessed atomically reserves an event.ID. Returns false if the ID
// was already dispatched (so the caller skips the event), true on first sight.
func (w *EventsWatcher) markEventProcessed(id string) bool {
	if strings.TrimSpace(id) == "" {
		return true // events without IDs are processed every time (best-effort)
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.processed[id] {
		return false
	}
	w.processed[id] = true
	w.processedOrder = append(w.processedOrder, id)
	if len(w.processedOrder) > maxProcessedEventIDs {
		oldest := w.processedOrder[0]
		w.processedOrder = w.processedOrder[1:]
		delete(w.processed, oldest)
	}
	return true
}

// markIssueCommentProcessed atomically reserves a comment ID. Returns false if
// this comment was already dispatched during the current watcher lifetime.
func (w *EventsWatcher) markIssueCommentProcessed(id int64) bool {
	if id <= 0 {
		return true
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.processedIssueComments == nil {
		w.processedIssueComments = make(map[int64]bool)
	}
	if w.processedIssueComments[id] {
		return false
	}
	w.processedIssueComments[id] = true
	w.processedIssueCommentOrder = append(w.processedIssueCommentOrder, id)
	if len(w.processedIssueCommentOrder) > maxProcessedIssueCommentIDs {
		oldest := w.processedIssueCommentOrder[0]
		w.processedIssueCommentOrder = w.processedIssueCommentOrder[1:]
		delete(w.processedIssueComments, oldest)
	}
	return true
}

func (w *EventsWatcher) isFreshIssueComment(createdAt string) bool {
	if strings.TrimSpace(createdAt) == "" {
		return true
	}
	w.mu.Lock()
	startedAt := w.startedAt
	w.mu.Unlock()
	if startedAt.IsZero() {
		return true
	}
	parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(createdAt))
	if err != nil {
		return true
	}
	return !parsed.Before(startedAt.Add(-issueCommentFreshnessSkew))
}

// ghCLITokenFn is injectable for tests.
var ghCLITokenFn = ghTokenFromCLI

// ghCandidatePaths lists well-known locations for the gh binary in order of
// preference. launchd processes run with a bare PATH (/usr/bin:/bin) so the
// Homebrew and MacPorts locations would not be found via plain PATH lookup.
var ghCandidatePaths = []string{
	"gh",                   // works when PATH is extended (e.g. from a shell or with EnvironmentVariables)
	"/opt/homebrew/bin/gh", // Apple-Silicon Homebrew
	"/usr/local/bin/gh",    // Intel Homebrew / manual install
	"/opt/local/bin/gh",    // MacPorts
	"/usr/bin/gh",          // system install
}

// ghTokenFromCLI tries `gh auth token` at each candidate path and returns the
// first non-empty trimmed token, or "" when gh is not found / not authenticated.
func ghTokenFromCLI() string {
	for _, candidate := range ghCandidatePaths {
		out, err := exec.Command(candidate, "auth", "token").Output()
		if err != nil {
			continue
		}
		if tok := strings.TrimSpace(string(out)); tok != "" {
			return tok
		}
	}
	return ""
}

// NewEventsWatcherFromEnv creates an EventsWatcher from GITHUB_TOKEN env var,
// falling back to `gh auth token` when the env var is absent so that users who
// are already logged in via the gh CLI do not need to set anything extra.
// Returns nil when no token can be resolved (watcher disabled).
func NewEventsWatcherFromEnv(monitor *RepoMonitor) *EventsWatcher {
	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		token = ghCLITokenFn()
	}
	if token == "" {
		return nil
	}
	return NewEventsWatcher(token, monitor)
}

// Start begins the event-watching loop. The first poll runs after login.
// The goroutine exits when ctx is cancelled.
func (w *EventsWatcher) Start(ctx context.Context) {
	go w.run(ctx)
}

// ── default implementations ──────────────────────────────────────────────────
// Injectable functions that implement the core GitHub API calls for testing.

func defaultEventsLoginFn(token string) (string, error) {
	return NewProfileClient(token).GetAuthenticatedLogin()
}

func defaultEventsListReposFn(token string, perPage int) ([]UserRepo, error) {
	return NewProfileClient(token).ListUserRepos(perPage)
}

// defaultFetchEventsFn calls GET /users/{login}/events with ETag conditional request.
// Returns new ETag, poll interval in seconds, and whether the response was unchanged (304).
func defaultFetchEventsFn(token, login, etag string) (events []eventEntry, newEtag string, pollSecs int, unchanged bool, err error) {
	url := fmt.Sprintf("%s/users/%s/events?per_page=100", apiBase(), login)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, "", 60, false, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}

	resp, err := eventsHTTPClient.Do(req)
	if err != nil {
		return nil, "", 60, false, err
	}
	defer resp.Body.Close()

	pollSecs = 60
	if v := resp.Header.Get("X-Poll-Interval"); v != "" {
		fmt.Sscanf(v, "%d", &pollSecs) //nolint:errcheck
	}
	newEtag = resp.Header.Get("ETag")

	if resp.StatusCode == http.StatusNotModified {
		return nil, etag, pollSecs, true, nil // 304 — no new events
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, newEtag, pollSecs, false, fmt.Errorf("events API %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	body, _ := io.ReadAll(resp.Body)
	if err := json.Unmarshal(body, &events); err != nil {
		return nil, newEtag, pollSecs, false, fmt.Errorf("parse events: %w", err)
	}
	return events, newEtag, pollSecs, false, nil
}

func defaultEventsReadmeFn(fullName, branch, token string) ([]byte, error) {
	if branch == "" {
		branch = "HEAD"
	}
	url := fmt.Sprintf("https://raw.githubusercontent.com/%s/%s/README.md", fullName, branch)
	return profileHTTPGet(url, token)
}

// ── internal loop ─────────────────────────────────────────────────────────────
// Core event polling and dispatch logic.

func (w *EventsWatcher) run(ctx context.Context) {
	login, err := w.loginFn(w.token)
	if err != nil {
		log.Printf("events-watcher: get login: %v", err)
		return
	}
	log.Printf("events-watcher: watching %s", login)

	// Catch any repos that already have @engine before we start streaming.
	w.initialScan()

	pollInterval := 60 * time.Second
	for {
		select {
		case <-ctx.Done():
			return
		case <-w.tickFn(pollInterval):
		}

		w.mu.Lock()
		etag := w.etag
		w.mu.Unlock()

		evts, newEtag, pollSecs, unchanged, err := w.fetchEventsFn(w.token, login, etag)
		if err != nil {
			log.Printf("events-watcher: fetch events: %v", err)
			continue
		}

		pollInterval = time.Duration(pollSecs) * time.Second

		if unchanged {
			continue // 304 — nothing to do
		}

		w.mu.Lock()
		w.etag = newEtag
		w.mu.Unlock()

		w.processEvents(evts)
	}
}

// initialScan runs a full scan of user repos at startup to catch any that already carry @engine.
func (w *EventsWatcher) initialScan() {
	repos, err := w.listReposFn(w.token, 100)
	if err != nil {
		log.Printf("events-watcher: initial scan list repos: %v", err)
		return
	}
	for _, r := range repos {
		w.checkRepo(r.FullName, r.DefaultBranch)
	}
}

// processEvents filters and routes events: collects unique repos that need README checks,
// and dispatches issue/issue_comment events for already-tagged repos.
func (w *EventsWatcher) processEvents(events []eventEntry) {
	// Collect unique repos that need a README check, along with a branch hint.
	type target struct{ fullName, branch string }
	seen := map[string]target{}

	for _, ev := range events {
		// Skip events we've already dispatched in a prior poll. The events API
		// returns the same events repeatedly until ETag stabilises.
		if !w.markEventProcessed(ev.ID) {
			continue
		}
		name := ev.Repo.Name
		switch ev.Type {
		case "PushEvent":
			var p struct {
				Ref     string `json:"ref"`
				Commits []struct {
					Added    []string `json:"added"`
					Modified []string `json:"modified"`
				} `json:"commits"`
			}
			if json.Unmarshal(ev.Payload, &p) != nil {
				continue
			}
			if !eventPushTouchesReadme(p.Commits) {
				continue
			}
			branch := strings.TrimPrefix(p.Ref, "refs/heads/")
			seen[name] = target{name, branch}

		case "CreateEvent":
			var p struct {
				RefType string `json:"ref_type"`
			}
			if json.Unmarshal(ev.Payload, &p) != nil {
				continue
			}
			if p.RefType == "repository" {
				seen[name] = target{name, "HEAD"}
			}

		case "IssuesEvent":
			// Only act on issues for repos we already know carry @engine.
			// This keeps Engine focused on its tracked surface and avoids
			// firing on every issue across the user's entire account.
			if !w.repoIsTagged(name) {
				continue
			}
			w.dispatchIssueEvent(name, ev.Payload)

		case "IssueCommentEvent":
			if !w.repoIsTagged(name) {
				continue
			}
			w.dispatchIssueCommentEvent(name, ev.Payload)
		}
	}

	for _, t := range seen {
		w.checkRepo(t.fullName, t.branch)
	}
}

// eventPushTouchesReadme checks if a push event's commits modified README file (case-insensitive).
func eventPushTouchesReadme(commits []struct {
	Added    []string `json:"added"`
	Modified []string `json:"modified"`
}) bool {
	for _, c := range commits {
		for _, f := range append(c.Added, c.Modified...) {
			low := strings.ToLower(f)
			if low == "readme.md" || low == "readme" {
				return true
			}
		}
	}
	return false
}

// checkRepo fetches the README for fullName/branch and fires OnReadmeChange when @engine tag
// is first detected (edge-triggered on 0→1 transition; resets when tag is removed).
func (w *EventsWatcher) checkRepo(fullName, branch string) {
	if branch == "" {
		branch = "HEAD"
	}

	content, err := w.fetchReadmeFn(fullName, branch, w.token)
	if err != nil {
		log.Printf("events-watcher: fetch README %s: %v", fullName, err)
		return
	}

	hasTag := strings.Contains(string(content), "@engine")

	w.mu.Lock()
	wasTagged := w.seen[fullName]
	w.seen[fullName] = hasTag
	w.mu.Unlock()

	if hasTag && !wasTagged {
		log.Printf("events-watcher: @engine tag in %s — triggering scaffold", fullName)
		parts := strings.SplitN(fullName, "/", 2)
		repoName := fullName
		if len(parts) == 2 {
			repoName = parts[1]
		}
		defaultBranch := branch
		if defaultBranch == "HEAD" {
			defaultBranch = "main"
		}
		payload, _ := json.Marshal(map[string]any{
			"ref": "refs/heads/" + defaultBranch,
			"repository": map[string]any{
				"full_name":      fullName,
				"name":           repoName,
				"default_branch": defaultBranch,
			},
			"commits": []map[string]any{
				{
					"id": "events-watcher", "message": "events-watcher",
					"added": []string{"README.md"}, "modified": []string{}, "removed": []string{},
				},
			},
		})
		if w.monitor.OnReadmeChange != nil {
			w.monitor.OnReadmeChange(json.RawMessage(payload))
		}
	}
}

// repoIsTagged reports whether the events watcher has previously seen the
// given full repository name ("owner/name") as carrying the @engine tag in
// its README. Used to gate issue/comment dispatch so Engine only acts on
// issues for repos it is already responsible for.
func (w *EventsWatcher) repoIsTagged(fullName string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.seen[fullName]
}

// dispatchIssueEvent reshapes a GitHub Events API IssuesEvent payload into
// the webhook-shaped payload the RepoMonitor's OnIssueOpened expects, and
// fires the callback when the action is "opened".
func (w *EventsWatcher) dispatchIssueEvent(fullName string, payload json.RawMessage) {
	if w.monitor == nil || w.monitor.OnIssueOpened == nil {
		return
	}
	var p struct {
		Action string `json:"action"`
		Issue  struct {
			User struct {
				Login string `json:"login"`
			} `json:"user"`
		} `json:"issue"`
	}
	// Decode twice: once for the user filter, once for raw passthrough.
	var rawProbe struct {
		Action string          `json:"action"`
		Issue  json.RawMessage `json:"issue"`
	}
	if err := json.Unmarshal(payload, &rawProbe); err != nil {
		log.Printf("events-watcher: parse IssuesEvent payload: %v", err)
		return
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		log.Printf("events-watcher: parse IssuesEvent payload (user filter): %v", err)
		return
	}
	if p.Action != "opened" {
		return
	}
	// Don't react to issues we opened ourselves — same feedback-loop concern as comments.
	if engineLogin := EngineLogin(); engineLogin != "" && strings.EqualFold(p.Issue.User.Login, engineLogin) {
		return
	}
	repoName := fullName
	if parts := strings.SplitN(fullName, "/", 2); len(parts) == 2 {
		repoName = parts[1]
	}
	out, _ := json.Marshal(map[string]any{
		"action": p.Action,
		"issue":  rawProbe.Issue,
		"repository": map[string]any{
			"full_name": fullName,
			"name":      repoName,
		},
	})
	log.Printf("events-watcher: issue opened in %s — dispatching to monitor", fullName)
	w.monitor.OnIssueOpened(json.RawMessage(out))
}

// dispatchIssueCommentEvent reshapes a GitHub Events API IssueCommentEvent
// payload into the webhook-shaped payload the RepoMonitor's OnIssueComment
// expects, and fires the callback for newly created comments.
func (w *EventsWatcher) dispatchIssueCommentEvent(fullName string, payload json.RawMessage) {
	if w.monitor == nil || w.monitor.OnIssueComment == nil {
		return
	}
	var p struct {
		Action  string          `json:"action"`
		Issue   json.RawMessage `json:"issue"`
		Comment struct {
			ID        int64  `json:"id"`
			Body      string `json:"body"`
			CreatedAt string `json:"created_at"`
			User      struct {
				Login string `json:"login"`
			} `json:"user"`
		} `json:"comment"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		log.Printf("events-watcher: parse IssueCommentEvent payload: %v", err)
		return
	}
	if p.Action != "created" {
		return
	}
	if !w.markIssueCommentProcessed(p.Comment.ID) {
		return
	}
	if !w.isFreshIssueComment(p.Comment.CreatedAt) {
		return
	}
	// Don't react to our own comments — that creates an infinite feedback loop
	// where bot narration becomes new events that spawn more bot narration.
	if engineLogin := EngineLogin(); engineLogin != "" && strings.EqualFold(p.Comment.User.Login, engineLogin) {
		return
	}
	// Re-serialize as the webhook-shaped payload (with body+user inside comment).
	rawComment, _ := json.Marshal(p.Comment)
	repoName := fullName
	if parts := strings.SplitN(fullName, "/", 2); len(parts) == 2 {
		repoName = parts[1]
	}
	out, _ := json.Marshal(map[string]any{
		"action":  p.Action,
		"issue":   p.Issue,
		"comment": json.RawMessage(rawComment),
		"repository": map[string]any{
			"full_name": fullName,
			"name":      repoName,
		},
	})
	log.Printf("events-watcher: issue comment in %s — dispatching to monitor", fullName)
	w.monitor.OnIssueComment(json.RawMessage(out))
}
