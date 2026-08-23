package quota

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// Claude Code keeps ALL of its per-account state — credentials, settings,
// history — under one directory chosen by CLAUDE_CONFIG_DIR (default ~/.claude).
// Point the variable at a different directory and you get a fully independent
// login. That is the whole mechanism behind multi-account support here: an
// Account is a name plus a config directory, and running any `claude` command
// with that directory exported runs it as that account.
//
// The subtlety worth stating, because getting it wrong silently doubles the
// headroom Engine thinks it has: QUOTA IS POOLED PER ANTHROPIC ACCOUNT, NOT PER
// CONFIG DIRECTORY. Two directories logged into the same email share one 5-hour
// window. So the registry resolves each directory's identity and collapses
// duplicates, and the governor schedules against distinct identities.

// Identity is who a config directory is logged in as, from `claude auth status`.
type Identity struct {
	LoggedIn         bool   `json:"loggedIn"`
	AuthMethod       string `json:"authMethod"`
	APIProvider      string `json:"apiProvider"`
	Email            string `json:"email,omitempty"`
	OrgID            string `json:"orgId,omitempty"`
	OrgName          string `json:"orgName,omitempty"`
	SubscriptionType string `json:"subscriptionType,omitempty"`
}

// Key is the identity's pooling key: the unit that actually shares a quota
// window. Falls back to the email, then to the auth method, so two directories
// that cannot be told apart are conservatively treated as the SAME pool rather
// than as two independent ones.
func (i Identity) Key() string {
	switch {
	case i.OrgID != "":
		return "org:" + i.OrgID
	case i.Email != "":
		return "email:" + strings.ToLower(i.Email)
	default:
		return "unknown"
	}
}

// Account is one usable Claude login.
type Account struct {
	// Name is the local handle used in config, logs and routing.
	Name string `json:"name"`
	// ConfigDir is the CLAUDE_CONFIG_DIR for this account. Empty means "inherit
	// whatever the process already has", i.e. the default ~/.claude login.
	ConfigDir string `json:"configDir,omitempty"`
	// Identity is filled in by Resolve.
	Identity Identity `json:"identity"`
	// Disabled accounts are kept in the registry (so their absence is visible)
	// but never scheduled.
	Disabled bool `json:"disabled,omitempty"`
	// DisabledReason explains why, e.g. "not logged in".
	DisabledReason string `json:"disabledReason,omitempty"`
}

// Env returns the environment for running `claude` as this account: the parent
// environment with CLAUDE_CONFIG_DIR replaced (not appended — a duplicate key
// would leave the resolution order up to the OS).
func (a Account) Env(parent []string) []string {
	if strings.TrimSpace(a.ConfigDir) == "" {
		return parent
	}
	out := make([]string, 0, len(parent)+1)
	for _, kv := range parent {
		if strings.HasPrefix(kv, "CLAUDE_CONFIG_DIR=") {
			continue
		}
		out = append(out, kv)
	}
	return append(out, "CLAUDE_CONFIG_DIR="+a.ConfigDir)
}

// Registry is the set of accounts Engine may draw on.
type Registry struct {
	mu       sync.RWMutex
	accounts []Account
	runner   Runner
}

// NewRegistry builds a registry over the given accounts.
func NewRegistry(runner Runner, accounts ...Account) *Registry {
	if runner == nil {
		runner = DefaultRunner()
	}
	return &Registry{accounts: accounts, runner: runner}
}

// DefaultAccountName is the handle for the ambient ~/.claude login.
const DefaultAccountName = "default"

// AccountsFromEnv reads the account list from ENGINE_CLAUDE_ACCOUNTS.
//
// Format is a comma-separated list of `name=/path/to/config/dir`, e.g.
//
//	ENGINE_CLAUDE_ACCOUNTS="work=/Users/me/.claude,side=/Users/me/.claude-side"
//
// A bare path with no `name=` is named after its directory. When the variable
// is unset or empty the result is the single ambient account, so the zero-config
// case is exactly today's behaviour.
func AccountsFromEnv(env string) []Account {
	env = strings.TrimSpace(env)
	if env == "" {
		return []Account{{Name: DefaultAccountName}}
	}
	var out []Account
	seen := map[string]bool{}
	for _, part := range strings.Split(env, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		name, dir := "", part
		if i := strings.Index(part, "="); i > 0 {
			name = strings.TrimSpace(part[:i])
			dir = strings.TrimSpace(part[i+1:])
		}
		dir = expandHome(dir)
		if name == "" {
			name = filepath.Base(strings.TrimSuffix(dir, string(filepath.Separator)))
		}
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, Account{Name: name, ConfigDir: dir})
	}
	if len(out) == 0 {
		return []Account{{Name: DefaultAccountName}}
	}
	return out
}

func expandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(p, "~"), "/"))
		}
	}
	return p
}

// Resolve fills in each account's identity by running `claude auth status`, and
// disables the ones that cannot be used.
//
// Two accounts resolving to the same pooling Key are NOT merged away — both stay
// visible, because a user who configured two directories expects to see two — but
// all but the first are disabled with a reason that says so. Scheduling against
// them as if they were independent is the failure this prevents.
func (r *Registry) Resolve(ctx context.Context) {
	r.mu.Lock()
	accounts := append([]Account(nil), r.accounts...)
	runner := r.runner
	r.mu.Unlock()

	seenPool := map[string]string{} // pooling key -> first account name
	for i := range accounts {
		a := &accounts[i]
		id, err := fetchIdentity(ctx, runner, *a)
		if err != nil {
			a.Disabled, a.DisabledReason = true, "auth status failed: "+err.Error()
			continue
		}
		a.Identity = id
		if !id.LoggedIn {
			a.Disabled, a.DisabledReason = true, "not logged in"
			continue
		}
		key := id.Key()
		if key == "unknown" {
			// Cannot prove independence, so do not assume it.
			a.Disabled, a.DisabledReason = true, "identity could not be determined"
			continue
		}
		if first, dup := seenPool[key]; dup {
			a.Disabled = true
			a.DisabledReason = fmt.Sprintf("shares a quota pool with %q (%s)", first, key)
			continue
		}
		seenPool[key] = a.Name
		a.Disabled, a.DisabledReason = false, ""
	}

	r.mu.Lock()
	r.accounts = accounts
	r.mu.Unlock()
}

// fetchIdentity runs `claude auth status` for one account.
func fetchIdentity(ctx context.Context, runner Runner, a Account) (Identity, error) {
	out, err := runner.Run(ctx, a, "auth", "status")
	if err != nil {
		return Identity{}, err
	}
	var id Identity
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &id); err != nil {
		return Identity{}, fmt.Errorf("parsing auth status: %w", err)
	}
	return id, nil
}

// All returns every account, enabled or not, in registration order.
func (r *Registry) All() []Account {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return append([]Account(nil), r.accounts...)
}

// Usable returns the accounts that may be scheduled.
func (r *Registry) Usable() []Account {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []Account
	for _, a := range r.accounts {
		if !a.Disabled {
			out = append(out, a)
		}
	}
	return out
}

// Get returns an account by name.
func (r *Registry) Get(name string) (Account, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, a := range r.accounts {
		if a.Name == name {
			return a, true
		}
	}
	return Account{}, false
}

// Names returns usable account names, sorted, for stable logging.
func (r *Registry) Names() []string {
	var out []string
	for _, a := range r.Usable() {
		out = append(out, a.Name)
	}
	sort.Strings(out)
	return out
}
