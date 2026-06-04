package github

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Engine bot identity uses ENGINE_GITHUB_BOT_TOKEN (preferred) or GITHUB_TOKEN.
// Set ENGINE_GITHUB_LOGIN to skip the API lookup for the bot's username.
// Set ENGINE_GITHUB_BOT_EMAIL to configure git commit authorship in cloned repos.

var (
	engineLoginMu     sync.Mutex
	engineLoginCached string
	engineLoginAt     time.Time
	engineLoginTTL    = time.Hour
)

// EngineToken returns the GitHub token for Engine's bot identity.
// Prefers ENGINE_GITHUB_BOT_TOKEN over GITHUB_TOKEN environment variable.
func EngineToken() string {
	if tok := strings.TrimSpace(os.Getenv("ENGINE_GITHUB_BOT_TOKEN")); tok != "" {
		return tok
	}
	return strings.TrimSpace(os.Getenv("GITHUB_TOKEN"))
}

// EngineClient returns a GitHub client authenticated as Engine's bot identity.
// Reads token from EngineToken() and creates a client for the given owner/repo.
func EngineClient(owner, repo string) (*Client, error) {
	tok := EngineToken()
	if tok == "" {
		return nil, fmt.Errorf("no GitHub token (set ENGINE_GITHUB_BOT_TOKEN or GITHUB_TOKEN)")
	}
	return NewClientWithToken(owner, repo, tok), nil
}

// EngineLogin returns the GitHub login for Engine's bot identity.
// Checks ENGINE_GITHUB_LOGIN first, then resolves via API (cached 1 h).
func EngineLogin() string {
	if login := strings.TrimSpace(os.Getenv("ENGINE_GITHUB_LOGIN")); login != "" {
		return login
	}
	engineLoginMu.Lock()
	defer engineLoginMu.Unlock()
	if engineLoginCached != "" && time.Since(engineLoginAt) < engineLoginTTL {
		return engineLoginCached
	}
	tok := EngineToken()
	if tok == "" {
		return ""
	}
	login, err := NewProfileClient(tok).GetAuthenticatedLogin()
	if err != nil {
		log.Printf("[engine-identity] resolve login: %v", err)
		return ""
	}
	engineLoginCached = login
	engineLoginAt = time.Now()
	return login
}

// EngineDisplayName is the human-readable name used in comments.
// Reads ENGINE_GITHUB_DISPLAY_NAME; defaults to "Engine".
func EngineDisplayName() string {
	if name := strings.TrimSpace(os.Getenv("ENGINE_GITHUB_DISPLAY_NAME")); name != "" {
		return name
	}
	return "Engine"
}

// AssignEngine assigns Engine's bot account to the given issue (best-effort).
func AssignEngine(owner, repo string, issueNumber int) error {
	login := EngineLogin()
	if login == "" {
		return nil
	}
	c, err := EngineClient(owner, repo)
	if err != nil {
		return nil
	}
	return c.AddAssignees(issueNumber, []string{login})
}

// UnassignEngine removes Engine's bot from the given issue (best-effort).
func UnassignEngine(owner, repo string, issueNumber int) error {
	login := EngineLogin()
	if login == "" {
		return nil
	}
	c, err := EngineClient(owner, repo)
	if err != nil {
		return nil
	}
	return c.RemoveAssignees(issueNumber, []string{login})
}

// ConfigureRepoIdentity sets git user.name and user.email inside the cloned
// repo so Engine's commits appear under its own identity on GitHub.
// Silently no-ops when the bot login/email is unconfigured.
func ConfigureRepoIdentity(repoPath string) {
	name := EngineDisplayName()
	email := strings.TrimSpace(os.Getenv("ENGINE_GITHUB_BOT_EMAIL"))
	if email == "" {
		login := EngineLogin()
		if login == "" {
			return
		}
		// GitHub's noreply format; commits appear attributed to the bot account.
		email = login + "@users.noreply.github.com"
	}
	for _, pair := range [][2]string{
		{"user.name", name},
		{"user.email", email},
	} {
		if err := gitLocalConfigFn(repoPath, pair[0], pair[1]); err != nil {
			log.Printf("[engine-identity] %v", err)
		}
	}
}

// gitLocalConfigFn is injectable so tests can stub git without forking.
var gitLocalConfigFn = func(repoPath, key, value string) error {
	out, err := exec.Command("git", "-C", repoPath, "config", "--local", key, value).CombinedOutput()
	if err != nil {
		return fmt.Errorf("git config %s: %v: %s", key, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// ── Issue comment store ───────────────────────────────────────────────────────

// IssueCommentStore persists the comment IDs Engine pinned to each issue so we
// can edit-in-place instead of spam-posting.
type IssueCommentStore struct {
	mu   sync.Mutex
	path string
	data map[string]int // "owner/repo#N" → comment ID
}

// IssueCommentStoreFor returns a comment store backed by the project's .engine dir.
func IssueCommentStoreFor(projectPath string) *IssueCommentStore {
	storePath := filepath.Join(projectPath, ".engine", "issue_comments.json")
	s := &IssueCommentStore{path: storePath, data: map[string]int{}}
	if raw, err := os.ReadFile(storePath); err == nil {
		_ = json.Unmarshal(raw, &s.data)
	}
	return s
}

func (s *IssueCommentStore) key(owner, repo string, n int) string {
	return fmt.Sprintf("%s/%s#%d", owner, repo, n)
}

// Get returns (commentID, true) if Engine has a pinned comment for this issue.
func (s *IssueCommentStore) Get(owner, repo string, n int) (int, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.data[s.key(owner, repo, n)]
	return v, ok
}

// Set records the comment ID and flushes to disk.
func (s *IssueCommentStore) Set(owner, repo string, n, commentID int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data[s.key(owner, repo, n)] = commentID
	s.persistData()
}

// persistData marshals the store data and writes it to disk.
// Errors are logged but not returned, following the parent function's pattern.
func (s *IssueCommentStore) persistData() {
	if raw, err := json.Marshal(s.data); err == nil {
		if err := os.MkdirAll(filepath.Dir(s.path), 0700); err != nil {
			log.Printf("issue comment store mkdir failed: %v", err)
			return
		}
		if err := os.WriteFile(s.path, raw, 0600); err != nil {
			log.Printf("issue comment store write failed: %v", err)
		}
	}
}
