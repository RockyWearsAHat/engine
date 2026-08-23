package quota

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Runner executes a `claude` subcommand for one account and returns stdout.
// Injected so the whole package is testable without the CLI installed.
//
// Implementations MUST be safe for concurrent use: ProbeAll and Registry.Resolve
// both call Run for several accounts at once.
type Runner interface {
	Run(ctx context.Context, a Account, args ...string) (string, error)
}

// execRunner runs the real CLI.
type execRunner struct {
	binary  string
	timeout time.Duration
}

// DefaultRunner runs the `claude` binary found on PATH, with a bounded timeout.
//
// The timeout matters more than it looks: `/usage` resolves locally and returns
// in about a second, but the binary still performs startup work (config load,
// plugin sync) that can hang on a bad network. A quota probe that blocks is
// worse than one that fails — the caller can proceed on a stale reading, but it
// cannot proceed on a promise that never resolves. Override the timeout with
// ENGINE_QUOTA_PROBE_TIMEOUT_SEC.
func DefaultRunner() Runner {
	return &execRunner{binary: claudeBinary(), timeout: envDuration("ENGINE_QUOTA_PROBE_TIMEOUT_SEC", 45*time.Second)}
}

func claudeBinary() string {
	if v := strings.TrimSpace(os.Getenv("ENGINE_CLAUDE_BINARY")); v != "" {
		return v
	}
	return "claude"
}

func envDuration(key string, def time.Duration) time.Duration {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return time.Duration(n) * time.Second
		}
	}
	return def
}

func (e *execRunner) Run(ctx context.Context, a Account, args ...string) (string, error) {
	if e.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, e.timeout)
		defer cancel()
	}
	cmd := exec.CommandContext(ctx, e.binary, args...)
	cmd.Env = a.Env(os.Environ())
	var stdout, stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if ctx.Err() == context.DeadlineExceeded {
			return "", fmt.Errorf("`claude %s` timed out after %s", strings.Join(args, " "), e.timeout)
		}
		if msg != "" {
			return "", fmt.Errorf("`claude %s`: %w: %s", strings.Join(args, " "), err, truncate(msg, 300))
		}
		return "", fmt.Errorf("`claude %s`: %w", strings.Join(args, " "), err)
	}
	return stdout.String(), nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// cliResult is the subset of `claude -p --output-format json` we read.
type cliResult struct {
	IsError bool   `json:"is_error"`
	Result  string `json:"result"`
	Subtype string `json:"subtype"`
	// CostUSD is asserted to be zero for /usage; see Prober.Probe.
	CostUSD  float64 `json:"total_cost_usd"`
	NumTurns int     `json:"num_turns"`
}

// Prober reads limit state per account, with a TTL cache and single-flighting.
//
// The read itself is free — `/usage` is a local slash command that resolves with
// no inference turn — but it still costs a process spawn of roughly a second, so
// a busy orchestrator asking before every dispatch would add real latency for a
// number that moves slowly. Hence the TTL. The cache is also what makes the
// governor cheap to consult: callers can ask on every decision.
type Prober struct {
	runner Runner
	ttl    time.Duration
	now    func() time.Time

	mu       sync.Mutex
	cache    map[string]Snapshot
	inflight map[string]chan struct{}
}

// NewProber builds a Prober. A zero ttl uses the default.
func NewProber(runner Runner, ttl time.Duration) *Prober {
	if runner == nil {
		runner = DefaultRunner()
	}
	if ttl <= 0 {
		ttl = envDuration("ENGINE_QUOTA_TTL_SEC", 90*time.Second)
	}
	return &Prober{
		runner:   runner,
		ttl:      ttl,
		now:      time.Now,
		cache:    map[string]Snapshot{},
		inflight: map[string]chan struct{}{},
	}
}

// SetClock overrides the clock, for tests.
func (p *Prober) SetClock(now func() time.Time) { p.now = now }

// Cached returns the last snapshot for an account without probing, and whether
// it is still within the TTL. A stale snapshot is still returned — knowing the
// balance as of four minutes ago beats knowing nothing.
func (p *Prober) Cached(account string) (Snapshot, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	s, ok := p.cache[account]
	if !ok {
		return Snapshot{Account: account}, false
	}
	return s, p.now().Sub(s.FetchedAt) < p.ttl
}

// Invalidate drops the cached snapshot for an account, forcing the next Probe to
// re-read. Called when a rate_limit_event says the state just changed under us.
func (p *Prober) Invalidate(account string) {
	p.mu.Lock()
	delete(p.cache, account)
	p.mu.Unlock()
}

// Probe returns the account's current limit state, using the cache when fresh.
//
// Never returns an error: a failed probe becomes an Unknown snapshot (Ok=false)
// carrying the reason. Callers must branch on Ok. This is deliberate — quota
// awareness is an optimisation, and an engine that refuses to build because it
// could not read its fuel gauge is strictly worse than one that builds
// conservatively.
func (p *Prober) Probe(ctx context.Context, a Account) Snapshot {
	if s, fresh := p.Cached(a.Name); fresh {
		return s
	}

	// Single-flight: many roles may ask at once at the start of a phase, and
	// spawning a dozen identical CLI processes to learn one number is exactly
	// the kind of waste this package exists to prevent.
	p.mu.Lock()
	if ch, running := p.inflight[a.Name]; running {
		p.mu.Unlock()
		select {
		case <-ch:
			s, _ := p.Cached(a.Name)
			return s
		case <-ctx.Done():
			s, _ := p.Cached(a.Name)
			return s
		}
	}
	ch := make(chan struct{})
	p.inflight[a.Name] = ch
	p.mu.Unlock()

	s := p.fetch(ctx, a)

	p.mu.Lock()
	p.cache[a.Name] = s
	delete(p.inflight, a.Name)
	p.mu.Unlock()
	close(ch)
	return s
}

// fetch performs the actual read and parse.
func (p *Prober) fetch(ctx context.Context, a Account) Snapshot {
	now := p.now()
	fail := func(format string, args ...any) Snapshot {
		return Snapshot{Account: a.Name, Ok: false, Err: fmt.Sprintf(format, args...), FetchedAt: now}
	}

	// `-p "/usage"` with JSON output. No --model: the command never reaches a
	// model, so pinning one would only risk an error on an account whose plan
	// does not carry that model.
	out, err := p.runner.Run(ctx, a, "-p", "/usage", "--output-format", "json")
	if err != nil {
		return fail("probe failed: %v", err)
	}

	var res cliResult
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &res); err != nil {
		return fail("probe returned unparseable JSON: %v", err)
	}
	if res.IsError {
		return fail("probe reported an error (%s): %s", res.Subtype, truncate(res.Result, 200))
	}

	snap, err := ParseUsage(res.Result, now)
	if err != nil {
		return fail("%v", err)
	}
	snap.Account = a.Name
	return snap
}

// ProbeAll reads every usable account concurrently and returns snapshots keyed
// by account name.
func (p *Prober) ProbeAll(ctx context.Context, accounts []Account) map[string]Snapshot {
	out := make(map[string]Snapshot, len(accounts))
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, a := range accounts {
		wg.Add(1)
		go func(a Account) {
			defer wg.Done()
			s := p.Probe(ctx, a)
			mu.Lock()
			out[a.Name] = s
			mu.Unlock()
		}(a)
	}
	wg.Wait()
	return out
}
