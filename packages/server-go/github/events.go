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

	mu   sync.Mutex
	etag string
	seen map[string]bool // full_name → README contained @engine last check

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
		token:         token,
		monitor:       monitor,
		seen:          make(map[string]bool),
		tickFn:        time.After,
		loginFn:       defaultEventsLoginFn,
		listReposFn:   defaultEventsListReposFn,
		fetchEventsFn: defaultFetchEventsFn,
		fetchReadmeFn: defaultEventsReadmeFn,
	}
}

// ghCLITokenFn is injectable for tests.
var ghCLITokenFn = ghTokenFromCLI

// ghCandidatePaths lists well-known locations for the gh binary in order of
// preference. launchd processes run with a bare PATH (/usr/bin:/bin) so the
// Homebrew and MacPorts locations would not be found via plain PATH lookup.
var ghCandidatePaths = []string{
	"gh", // works when PATH is extended (e.g. from a shell or with EnvironmentVariables)
	"/opt/homebrew/bin/gh",  // Apple-Silicon Homebrew
	"/usr/local/bin/gh",     // Intel Homebrew / manual install
	"/opt/local/bin/gh",     // MacPorts
	"/usr/bin/gh",           // system install
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

func defaultEventsLoginFn(token string) (string, error) {
	return NewProfileClient(token).GetAuthenticatedLogin()
}

func defaultEventsListReposFn(token string, perPage int) ([]UserRepo, error) {
	return NewProfileClient(token).ListUserRepos(perPage)
}

// defaultFetchEventsFn calls GET /users/{login}/events with ETag.
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

func (w *EventsWatcher) processEvents(events []eventEntry) {
	// Collect unique repos that need a README check, along with a branch hint.
	type target struct{ fullName, branch string }
	seen := map[string]target{}

	for _, ev := range events {
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
		}
	}

	for _, t := range seen {
		w.checkRepo(t.fullName, t.branch)
	}
}

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

// checkRepo fetches the README for fullName/branch and fires OnReadmeChange
// the first time the @engine tag appears (edge-triggered; resets when removed).
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
