package main

// quality:allow-long-file

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/engine/server/ai"
	"github.com/engine/server/db"
	"github.com/engine/server/discord"
	gogit "github.com/engine/server/git"
	"github.com/engine/server/github"
	"github.com/engine/server/mesh"
	"github.com/engine/server/quota"
	"github.com/engine/server/remote"
	"github.com/engine/server/runtimecfg"
	"github.com/engine/server/vpn"
	"github.com/engine/server/ws"
)

type discordRuntime interface {
	Start() error
	Close() error
	CurrentConfig() discord.Config
	Reload(cfg discord.Config) error
	SearchHistory(projectPath, query, since string, limit int) ([]db.DiscordSearchHit, error)
	RecentHistory(projectPath, threadID, since string, limit int) ([]db.DiscordMessage, error)
	SendDMToOwner(message string) error
	NotifyProjectProgress(projectPath, message string)
}

// repoSyncMu serialises git fetch+reset per repo path so concurrent issue/scaffold
// sessions don't race on .git/index.lock.
var repoSyncMu sync.Map // key: dest path → *sync.Mutex

func repoSyncLock(dest string) func() {
	v, _ := repoSyncMu.LoadOrStore(dest, &sync.Mutex{})
	mu := v.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

var (
	runFn               = run
	logFatalFn          = log.Fatal
	dbInitFn            = db.Init
	createSessionFn     = db.CreateSession
	saveMessageFn       = db.SaveMessage
	newHubFn            = ws.NewHub
	loadDiscordConfigFn = discord.LoadConfig
	newDiscordServiceFn = func(cfg discord.Config, projectPath string) (discordRuntime, error) {
		return discord.NewService(cfg, projectPath)
	}
	setDiscordBridgeFn          = ws.SetDiscordBridge
	newWebhookReceiverFn        = github.NewWebhookReceiver
	newRepoMonitorFn            = github.NewRepoMonitor
	repoMonitorStartFn          = func(rm *github.RepoMonitor) { rm.Start(context.Background()) }
	newEventsWatcherFn          = github.NewEventsWatcherFromEnv
	eventsWatcherStartFn        = (*github.EventsWatcher).Start
	newVPNTunnelFn              = vpn.NewTunnel
	vpnRegisterRoutesFn         = (*vpn.Tunnel).RegisterRoutes
	vpnListenTLSFn              = (*vpn.Tunnel).ListenAndServeTLS
	newRemoteServerFn           = remote.NewServer
	setPairingManagerFn         = ws.SetPairingManager
	remoteListenTLSFn           = (*remote.Server).ListenAndServeTLS
	aiChatFn                    = ai.Chat
	httpHandleFuncFn            = http.HandleFunc
	httpHandleFn                = http.Handle
	httpListenAndServeFn        = http.ListenAndServe
	runAsyncFn                  = func(fn func()) { go fn() }
	triggerScaffoldSessionFn    func(string, json.RawMessage)
	triggerCIAnalysisSessionFn  func(string, json.RawMessage)
	triggerIssueSessionFn       func(string, json.RawMessage)
	triggerIssueOpenedSessionFn func(string, json.RawMessage)
	runCommandCombinedOutputFn  = func(name string, args ...string) ([]byte, error) {
		cmd := exec.Command(name, args...)
		return cmd.CombinedOutput()
	}
	dbListSessionsForScaffoldFn = db.ListSessions
	scaffoldTriggerMu           sync.Mutex
	scaffoldTriggerRunning      = make(map[string]bool)
	scaffoldTriggerLastStart    = make(map[string]time.Time)
	issueCommentTriggerMu       sync.Mutex
	issueCommentLastDispatch    = make(map[string]time.Time)
)

func init() {
	triggerScaffoldSessionFn = triggerScaffoldSession
	triggerCIAnalysisSessionFn = triggerCIAnalysisSession
	triggerIssueSessionFn = triggerIssueSession
	triggerIssueOpenedSessionFn = triggerIssueOpenedSession
}

const scaffoldTriggerCooldown = 2 * time.Minute
const issueCommentTriggerCooldown = 90 * time.Second
const autonomousIssueMaxTurns = 20
const autonomousIssueMaxAttempts = 3

// scaffoldRetryDelay is how long to wait before automatically re-triggering a
// scaffold session that ended without completing the project (i.e., no signal_done).
var scaffoldRetryDelay = 5 * time.Minute

// scaffoldMaxRetries is the total number of outer retry cycles allowed before
// Engine gives up and waits for the next GitHub webhook to re-trigger.
const scaffoldMaxRetries = 5

var scaffoldAttemptTimeout = 15 * time.Minute

// osGetwdFn and osUserHomeDirFn are injectable for tests.
var (
	osGetwdFn       = os.Getwd
	osUserHomeDirFn = os.UserHomeDir
)

func defaultProjectPath() string {
	if cwd, err := osGetwdFn(); err == nil && cwd != "" {
		return cwd
	}
	if home, err := osUserHomeDirFn(); err == nil && home != "" {
		return home
	}
	return "."
}

func main() {
	if err := runFn(); err != nil {
		logFatalFn(err)
	}
}

func run() error {
	projectPath := os.Getenv("PROJECT_PATH")
	if projectPath == "" {
		projectPath = defaultProjectPath()
	}

	if err := dbInitFn(projectPath); err != nil {
		return fmt.Errorf("failed to initialize database: %w", err)
	}

	hub := newHubFn(projectPath)

	if cfg, err := loadDiscordConfigFn(projectPath); err != nil {
		return fmt.Errorf("invalid discord config: %w", err)
	} else if cfg.Enabled {
		discordService, err := newDiscordServiceFn(cfg, projectPath)
		if err != nil {
			log.Printf("[engine-discord] disabled due to init error: %v", err)
		} else if err := discordService.Start(); err != nil {
			log.Printf("[engine-discord] disabled due to start error: %v", err)
		} else {
			setDiscordBridgeFn(discordService)
			defer discordService.Close() //nolint:errcheck
		}
	} else {
		// Even when disabled, allow the UI to save/validate config via WS by
		// wiring a stub that proxies only the bridge methods relying on the
		// config + archive. We construct a non-started service so CurrentConfig
		// and history queries work.
		if stub, err := newDiscordServiceFn(cfg, projectPath); err == nil {
			setDiscordBridgeFn(stub)
		}
	}

	// VPN tunnel mode: ENGINE_VPN=1 starts Ed25519-authenticated tunnel on top of TLS
	if os.Getenv("ENGINE_VPN") == "1" {
		vpnCfg := vpn.DefaultConfig()
		if port := os.Getenv("VPN_PORT"); port != "" {
			vpnCfg.Port = port
		}
		vpnCfg.Enabled = true

		tunnel, err := newVPNTunnelFn(vpnCfg)
		if err != nil {
			return fmt.Errorf("failed to start vpn tunnel: %w", err)
		}

		mux := http.NewServeMux()
		vpnRegisterRoutesFn(tunnel, mux, hub.ServeWS)
		return vpnListenTLSFn(tunnel, mux)
	}

	// Remote mode: ENGINE_REMOTE=1 starts a TLS-secured server with pairing and auth
	if os.Getenv("ENGINE_REMOTE") == "1" {
		cfg := remote.DefaultConfig()
		if port := os.Getenv("REMOTE_PORT"); port != "" {
			cfg.Port = port
		}
		cfg.Enabled = true

		srv, err := newRemoteServerFn(cfg, hub.ServeWS)
		if err != nil {
			return fmt.Errorf("failed to start remote server: %w", err)
		}

		setPairingManagerFn(srv.Pairing)
		return remoteListenTLSFn(srv)
	}

	// LAN compute mesh: ENGINE_MESH=1 starts the signed HTTP listener that lets
	// paired Mac/PC peers dispatch shell exec and inference to each other.
	if os.Getenv("ENGINE_MESH") == "1" {
		startMeshServer()
	}

	// Local mode: plain HTTP, no authentication needed
	port := os.Getenv("PORT")
	if port == "" {
		port = "24444"
	}
	httpHandleFuncFn("/ws", hub.ServeWS)
	httpHandleFuncFn("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"status":"ok","projectPath":%q}`, projectPath)
	})

	// Quota gauge. An external supervisor polls this to see how much of the
	// rolling 5-hour and 7-day subscription windows is left, and what the engine
	// has decided to do about it, without shelling out to the CLI itself.
	//
	// ?format=text returns the human report instead of JSON.
	httpHandleFuncFn("/quota", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		// The gauge is read from a cache, but a cold read shells out to the CLI.
		// Bound it so a hung probe cannot hold the connection open indefinitely.
		ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
		defer cancel()

		if r.URL.Query().Get("format") == "text" {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			fmt.Fprintln(w, ai.QuotaReport(ctx))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		st, ok := ai.QuotaStatus(ctx)
		if !ok {
			// Not an error: governance is simply switched off. Say so explicitly
			// rather than returning an empty gauge a caller could read as "0% used".
			fmt.Fprint(w, `{"enabled":false,"reason":"quota governance is disabled (ENGINE_QUOTA=0)"}`)
			return
		}
		payload := struct {
			Enabled bool `json:"enabled"`
			quota.Status
		}{Enabled: true, Status: st}
		if err := json.NewEncoder(w).Encode(payload); err != nil {
			log.Printf("quota: encode status: %v", err)
		}
	})

	// GitHub webhook receiver for repo monitoring.
	webhookSecret := os.Getenv("GITHUB_WEBHOOK_SECRET")
	webhookReceiver := newWebhookReceiverFn(webhookSecret)
	repoMonitor := newRepoMonitorFn()
	var eventsWatcherOnce sync.Once
	startEventsWatcher := func() {
		eventsWatcherOnce.Do(func() {
			if ew := newEventsWatcherFn(repoMonitor); ew != nil {
				eventsWatcherStartFn(ew, context.Background())
				log.Printf("events-watcher: started (requires GITHUB_TOKEN)")
			}
		})
	}
	ws.SetGitHubAuthSuccessHook(func(token, webhookSecret string) {
		if strings.TrimSpace(token) == "" {
			return
		}
		webhookReceiver.SetSecret(webhookSecret)
		startEventsWatcher()
	})
	defer ws.SetGitHubAuthSuccessHook(nil)
	repoMonitor.OnReadmeChange = func(payload json.RawMessage) {
		log.Printf("README changed: launching AI scaffold session (payload %d bytes)", len(payload))
		runAsyncFn(func() { triggerScaffoldSessionFn(projectPath, payload) })
	}
	repoMonitor.OnCIFailure = func(payload json.RawMessage) {
		log.Printf("CI failure: launching AI analysis session (payload %d bytes)", len(payload))
		runAsyncFn(func() { triggerCIAnalysisSessionFn(projectPath, payload) })
	}
	repoMonitor.OnIssueComment = func(payload json.RawMessage) {
		log.Printf("Issue comment received: launching AI issue session (payload %d bytes)", len(payload))
		runAsyncFn(func() { triggerIssueSessionFn(projectPath, payload) })
	}
	repoMonitor.OnIssueOpened = func(payload json.RawMessage) {
		log.Printf("Issue opened: launching AI issue session (payload %d bytes)", len(payload))
		runAsyncFn(func() { triggerIssueOpenedSessionFn(projectPath, payload) })
	}
	webhookReceiver.AddHandler(repoMonitor.Enqueue)
	repoMonitorStartFn(repoMonitor)
	// Start the events watcher — near-real-time @engine detection via GitHub Events API.
	startEventsWatcher()
	// Register the webhook route.
	httpHandleFn("/webhook/github", webhookReceiver)

	addr := ":" + port
	fmt.Printf("Server running on http://localhost%s\n", addr)
	return httpListenAndServeFn(addr, nil)
}

func buildAutonomousRepoPath(baseProjectPath, owner, repo string) string {
	clonesDir := ""
	if cfg, err := runtimecfg.Load(baseProjectPath); err == nil {
		clonesDir = strings.TrimSpace(cfg.ClonesDir)
	}
	if clonesDir == "" {
		clonesDir = strings.TrimSpace(os.Getenv("ENGINE_CLONES_DIR"))
	}
	if clonesDir == "" {
		home, err := osUserHomeDirFn()
		if err == nil && strings.TrimSpace(home) != "" {
			clonesDir = filepath.Join(home, ".engine", "projects")
		} else {
			clonesDir = filepath.Join(baseProjectPath, ".engine", "projects")
		}
	}
	repoSlug := owner + "-" + repo
	if strings.TrimSpace(owner) == "" {
		repoSlug = repo
	}
	return filepath.Join(clonesDir, repoSlug)
}

func ensureAutonomousRepoWorkspace(baseProjectPath, owner, repo string) (string, error) {
	dest := buildAutonomousRepoPath(baseProjectPath, owner, repo)
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return "", fmt.Errorf("create clone parent directory: %w", err)
	}

	if _, err := os.Stat(filepath.Join(dest, ".git")); err == nil {
		unlock := repoSyncLock(dest)
		defer unlock()
		// Remove stale lock files left by a killed git process — these block all
		// subsequent git operations and only go away if cleaned up explicitly.
		os.Remove(filepath.Join(dest, ".git", "index.lock"))       //nolint:errcheck
		os.Remove(filepath.Join(dest, ".git", "MERGE_HEAD"))       //nolint:errcheck
		os.Remove(filepath.Join(dest, ".git", "CHERRY_PICK_HEAD")) //nolint:errcheck
		if out, cmdErr := runCommandCombinedOutputFn("git", "-C", dest, "fetch", "origin", "--prune"); cmdErr != nil {
			return "", fmt.Errorf("fetch repo update: %v: %s", cmdErr, strings.TrimSpace(string(out)))
		}
		// Remove untracked/ignored files (e.g. engine state from a prior scaffold run)
		// that would block a merge now that they are .gitignore'd on origin.
		// Preserve .engine/ — it carries the orchestration plan/state that lets a
		// restart resume mid-build instead of starting over.
		runCommandCombinedOutputFn("git", "-C", dest, "clean", "-fdx", "-e", ".engine") //nolint:errcheck — best-effort
		// Hard reset to origin/HEAD — avoids ff-only failures when the builder has
		// pushed commits that diverge from the local clone's tracked branch.
		if out, cmdErr := runCommandCombinedOutputFn("git", "-C", dest, "reset", "--hard", "origin/HEAD"); cmdErr != nil {
			return "", fmt.Errorf("pull repo update: %v: %s", cmdErr, strings.TrimSpace(string(out)))
		}
		return dest, nil
	}

	repoURL := fmt.Sprintf("https://github.com/%s/%s.git", owner, repo)
	if out, cmdErr := runCommandCombinedOutputFn("git", "clone", repoURL, dest); cmdErr != nil {
		return "", fmt.Errorf("clone repo %s: %v: %s", repoURL, cmdErr, strings.TrimSpace(string(out)))
	}
	github.ConfigureRepoIdentity(dest)
	return dest, nil
}

func buildReadmeAutonomousBuildPrompt(owner, repo, localRepoPath string) string {
	readmePath := filepath.Join(localRepoPath, "README.md")
	return fmt.Sprintf(
		"The GitHub repository %s/%s needs an end-to-end autonomous build from its README.\n\n"+
			"The README idea plus the @engine tag are the entire request. Do not wait for a follow-up prompt, clarifying prompt, or manual implementation breakdown unless you are truly blocked by missing credentials, permissions, or external requirements.\n\n"+
			"Execution contract (must complete all phases):\n"+
			"1. Understand\n"+
			"- Read local README first: %s\n"+
			"- Treat the README as the source of product intent and success criteria unless newer repo evidence proves it stale\n"+
			"- If missing/stale, fetch latest README with shell: curl -s https://raw.githubusercontent.com/%s/%s/HEAD/README.md\n"+
			"- Write a concrete implementation plan to %s\n\n"+
			"2. Scaffold\n"+
			"- Create missing project structure and bootstrap files in %s\n\n"+
			"3. Implement\n"+
			"- Build the feature set described by the README, not just stubs\n\n"+
			"4. Validate\n"+
			"- Run the real build/test commands with shell in %s\n"+
			"- For Go projects: ALWAYS use `GOWORK=off go test ./...` and `GOWORK=off go build ./...` — never plain `go test` which inherits parent workspaces\n"+
			"- If no tests exist, add a minimal meaningful test and run it\n"+
			"- Iterate until checks pass or you are genuinely blocked\n\n"+
			"5. Deliver\n"+
			"- Commit all completed work with git_commit\n"+
			"- Final response must include: files created/changed, commands run, and test/build results\n\n"+
			"Environment awareness rule: this project lives in its own isolated directory. Default to that directory for build/test/edit work, use outside paths only when the task requires it, and never assume a parent go.work, package.json, or workspace exists.\n"+
			"Autonomy rule: continue without asking for input unless blocked by missing credentials/permissions/requirements. Do NOT stop and say you are ready to start, do NOT ask for a prompt, and do NOT wait for the user to restate the idea in another format — just start.",
		owner,
		repo,
		readmePath,
		owner,
		repo,
		filepath.Join(localRepoPath, "PROJECT_GOAL.md"),
		localRepoPath,
		localRepoPath,
	)
}

type repoActivitySnapshot struct {
	head      string
	staged    int
	unstaged  int
	untracked int
}

func captureRepoActivity(projectPath string) repoActivitySnapshot {
	snapshot := repoActivitySnapshot{}
	commits, _ := gogit.GetLog(projectPath, 1)
	if len(commits) > 0 {
		snapshot.head = strings.TrimSpace(commits[0].Hash)
	}
	status, _ := gogit.GetStatus(projectPath)
	if status != nil {
		snapshot.staged = len(status.Staged)
		snapshot.unstaged = len(status.Unstaged)
		snapshot.untracked = len(status.Untracked)
	}
	return snapshot
}

func hasRepoProgress(before, after repoActivitySnapshot) bool {
	if strings.TrimSpace(before.head) != strings.TrimSpace(after.head) {
		return true
	}
	if before.staged != after.staged {
		return true
	}
	if before.unstaged != after.unstaged {
		return true
	}
	if before.untracked != after.untracked {
		return true
	}
	return false
}

// hasCommitProgress returns true only when new git commits were made.
// Used for scaffold "finished" decisions so that merely creating untracked
// metadata files (e.g. .cache/, .engine/, PROJECT_GOAL.md) does not
// falsely report success before real code has been committed.
func hasCommitProgress(before, after repoActivitySnapshot) bool {
	return strings.TrimSpace(before.head) != strings.TrimSpace(after.head)
}

func scaffoldNoopRetryPrompt(owner, repo string) string {
	return fmt.Sprintf(
		"Previous attempt made no committed repository changes for %s/%s. "+
			"Writing planning documents (e.g. PROJECT_GOAL.md) is NOT sufficient — you must now implement the code described in the README. "+
			"Do not stop at planning. You must: write actual source files, run build/test commands, commit all created files with git_commit, and report the committed file list.",
		owner,
		repo,
	)
}

func scaffoldErrorRetryPrompt(owner, repo, reason string) string {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "unknown failure"
	}
	return fmt.Sprintf(
		"IMPORTANT: The previous scaffold attempt for %s/%s did not complete cleanly (%s). "+
			"Resume from current repository state, verify with build/tests, and continue iteratively until done. "+
			"Do not stop at planning or partial edits.",
		owner,
		repo,
		reason,
	)
}

func parseOwnerRepo(fullName string) (string, string, bool) {
	parts := strings.SplitN(strings.TrimSpace(fullName), "/", 2)
	if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}

func beginScaffoldTrigger(repoKey string) bool {
	if strings.TrimSpace(repoKey) == "" {
		return false
	}

	scaffoldTriggerMu.Lock()
	defer scaffoldTriggerMu.Unlock()

	if scaffoldTriggerRunning[repoKey] {
		return false
	}
	if lastStart, ok := scaffoldTriggerLastStart[repoKey]; ok {
		if time.Since(lastStart) < scaffoldTriggerCooldown {
			return false
		}
	}

	scaffoldTriggerRunning[repoKey] = true
	scaffoldTriggerLastStart[repoKey] = time.Now()
	return true
}

func finishScaffoldTrigger(repoKey string) {
	scaffoldTriggerMu.Lock()
	defer scaffoldTriggerMu.Unlock()
	delete(scaffoldTriggerRunning, repoKey)
}

func hasRecentScaffoldSession(projectPath, repo string, within time.Duration) bool {
	if strings.TrimSpace(projectPath) == "" || strings.TrimSpace(repo) == "" || within <= 0 {
		return false
	}

	recent := false
	_ = db.WithProject(projectPath, func() error {
		sessions, err := db.ListSessions(projectPath)
		if err != nil || len(sessions) == 0 {
			return nil
		}

		prefix := "scaffold-" + repo + "-"
		for i := 0; i < len(sessions) && i < 12; i++ {
			sess := sessions[i]
			if !strings.HasPrefix(sess.ID, prefix) {
				continue
			}

			ts := strings.TrimSpace(sess.UpdatedAt)
			if ts == "" {
				ts = strings.TrimSpace(sess.CreatedAt)
			}
			if ts == "" {
				continue
			}

			when, parseErr := time.Parse(time.RFC3339, ts)
			if parseErr != nil {
				continue
			}
			if time.Since(when) < within {
				recent = true
				return nil
			}
		}
		return nil
	})
	return recent
}

func hasEngineMention(text string) bool {
	return strings.Contains(strings.ToLower(text), "@engine")
}

func autoEnrollDiscordProject(projectPath, owner, repo string) {
	if bridge := ws.GetDiscordBridge(); bridge != nil {
		type autoEnroller interface {
			AutoEnrollProject(projectPath, owner, repo string) error
		}
		if enroller, ok := bridge.(autoEnroller); ok {
			if err := enroller.AutoEnrollProject(projectPath, owner, repo); err != nil {
				log.Printf("[autonomous %s/%s] discord enroll: %v", owner, repo, err)
			}
		}
	}
}

func notifyDiscordProjectProgress(projectPath, message string) {
	if bridge := ws.GetDiscordBridge(); bridge != nil {
		type progressNotifier interface {
			NotifyProjectProgress(projectPath, message string)
		}
		if notifier, ok := bridge.(progressNotifier); ok {
			notifier.NotifyProjectProgress(projectPath, message)
		}
	}
}

// primeAutonomousPublishIntentFn is the injection point for tests. Production
// binds it to primeAutonomousPublishIntent below.
var primeAutonomousPublishIntentFn = primeAutonomousPublishIntent

// primeAutonomousPublishIntent grants publish intent for any project that
// invited Engine in via @engine in the README. The decision of what "shipped"
// means — a live URL, a release artifact, a package on a registry — is left
// to the model, which reads the README and chooses. The job here is just to
// unblock the deploy gate so the model can act on what it decides.
func primeAutonomousPublishIntent(repoPath, owner, repo string) {
	readme := readRepoReadme(repoPath)
	profile := ai.BuildHeuristicProjectProfile(repoPath, readme, "")

	// @engine in the README is the opt-in. Treat that as explicit publish intent.
	// If the heuristic already detected explicit intent from deploy/publish words,
	// the evidence list already has those excerpts; otherwise append the engine-tag
	// evidence so the audit trail records why intent was granted.
	if profile.ExecutionIntent.PublishIntent != ai.PublishIntentExplicit {
		profile.ExecutionIntent.PublishIntent = ai.PublishIntentExplicit
		profile.ExecutionIntent.PublishEvidence = append(
			profile.ExecutionIntent.PublishEvidence,
			ai.PublishIntentEvidence{
				Source:     "engine-tag",
				Excerpt:    fmt.Sprintf("@engine tag in README of %s/%s; user opted into autonomous build-and-ship.", owner, repo),
				CapturedAt: time.Now().UTC().Format(time.RFC3339),
			},
		)
	}

	if data, err := json.Marshal(profile); err == nil {
		if upsertErr := db.UpsertProjectProfile(repoPath, string(data)); upsertErr != nil {
			log.Printf("primeAutonomousPublishIntent: upsert project profile failed for %s: %v", repoPath, upsertErr)
		}
	}
	if err := ai.WriteProjectProfileCache(repoPath, &profile); err != nil {
		log.Printf("primeAutonomousPublishIntent: write profile cache failed for %s: %v", repoPath, err)
	}
}

// readRepoReadme returns the README contents from the latest committed copy
// (origin/HEAD), falling back to the working tree. Empty string on any failure.
func readRepoReadme(repoPath string) string {
	for _, ref := range []string{"origin/HEAD", "origin/main", "origin/master"} {
		if out, err := runCommandCombinedOutputFn("git", "-C", repoPath, "show", ref+":README.md"); err == nil {
			return string(out)
		}
	}
	if data, err := os.ReadFile(filepath.Join(repoPath, "README.md")); err == nil {
		return string(data)
	}
	return ""
}

// readmeContainsEngineTag returns true when README.md in repoPath contains the
// @engine tag, indicating the repository owner has opted in to autonomous
// development by Engine.
func readmeContainsEngineTag(repoPath string) bool {
	remoteRefs := []string{"origin/HEAD", "origin/main", "origin/master"}
	for _, ref := range remoteRefs {
		if out, err := runCommandCombinedOutputFn("git", "-C", repoPath, "show", ref+":README.md"); err == nil {
			return strings.Contains(string(out), "@engine")
		}
	}
	data, err := os.ReadFile(filepath.Join(repoPath, "README.md"))
	if err != nil {
		return false
	}
	return strings.Contains(string(data), "@engine")
}

// triggerScaffoldSession fires an AI session when a README changes on GitHub.
// It reads the README content and asks the AI to plan and scaffold the project.
func triggerScaffoldSession(projectPath string, payload json.RawMessage) {
	var pushEvent struct {
		Repository struct {
			FullName string `json:"full_name"`
		} `json:"repository"`
	}
	if err := json.Unmarshal(payload, &pushEvent); err != nil || pushEvent.Repository.FullName == "" {
		log.Printf("scaffold: cannot parse repo from webhook payload: %v", err)
		return
	}
	owner, repo, ok := parseOwnerRepo(pushEvent.Repository.FullName)
	if !ok {
		log.Printf("scaffold: unexpected full_name format: %s", pushEvent.Repository.FullName)
		return
	}
	repoKey := strings.ToLower(strings.TrimSpace(pushEvent.Repository.FullName))
	if !beginScaffoldTrigger(repoKey) {
		log.Printf("scaffold: deduped trigger for %s", pushEvent.Repository.FullName)
		return
	}
	defer finishScaffoldTrigger(repoKey)

	targetProjectPath, err := ensureAutonomousRepoWorkspace(projectPath, owner, repo)
	if err != nil {
		log.Printf("scaffold: clone/sync failed for %s/%s: %v; skipping autonomous build", owner, repo, err)
		return
	}

	if !readmeContainsEngineTag(targetProjectPath) {
		log.Printf("scaffold: README in %s/%s does not contain @engine tag; skipping autonomous build", owner, repo)
		return
	}
	if hasRecentScaffoldSession(targetProjectPath, repo, scaffoldTriggerCooldown) {
		log.Printf("scaffold: deduped recent scaffold session for %s/%s", owner, repo)
		return
	}

	autoEnrollDiscordProject(targetProjectPath, owner, repo)
	notifyDiscordProjectProgress(targetProjectPath, fmt.Sprintf("🧭 Kickoff: reading README and planning autonomous build for **%s/%s**.", owner, repo))

	// Establish a ProjectProfile from the README so the deploy gate knows the
	// project type and publish intent. @engine in the README is the user's opt-in.
	// Rules (from product decisions, see memory):
	//   - README explicitly mentions deploy / publish / host / release → auto-grant publish intent.
	//   - Web-facing project type (web-app, REST API, service) with no explicit mention →
	//     auto-grant with an audit-trail evidence string AND post a Discord notice so
	//     the user has a window to `!stop` before the deploy step runs.
	//   - Non-web, no explicit mention → no auto-grant (orchestrator stops at working artifact).
	primeAutonomousPublishIntentFn(targetProjectPath, owner, repo)

	if dbErr := db.WithProject(targetProjectPath, func() error {
		brief := buildReadmeAutonomousBuildPrompt(owner, repo, targetProjectPath)
		state, runErr := runOrchestratorFn(ai.OrchestratorConfig{
			ProjectPath:     targetProjectPath,
			Owner:           owner,
			Repo:            repo,
			Brief:           brief,
			SessionIDPrefix: fmt.Sprintf("scaffold-%s", repo),
			ChatFn:          aiChatFn,
			OnPhase: func(phase, detail string) {
				log.Printf("[orchestrator %s/%s] phase=%s %s", owner, repo, phase, detail)
				icon := phaseIcon(phase)
				notifyDiscordProjectProgress(targetProjectPath, fmt.Sprintf("%s **%s/%s**: %s — %s", icon, owner, repo, phase, detail))
				postOrchestratorGitHubStatusFn(owner, repo, phase, detail)
			},
			OnPlanUpdate: func(state *ai.OrchestrationState) {
				log.Printf("[orchestrator %s/%s] plan updated (%d/%d done)", owner, repo, doneCount(state), len(state.Plan))
			},
			OnError: func(msg string) {
				log.Printf("[orchestrator error %s/%s] %s", owner, repo, msg)
			},
		})
		if runErr != nil {
			notifyDiscordProjectProgress(targetProjectPath, fmt.Sprintf("❌ Autonomous build for **%s/%s** halted: %s", owner, repo, runErr.Error()))
			postOrchestratorGitHubStatusFn(owner, repo, "failure", runErr.Error())
			return nil
		}
		if state != nil && strings.TrimSpace(state.CompletedAt) != "" {
			notifyDiscordProjectProgress(targetProjectPath, fmt.Sprintf("✅ Autonomous build complete for **%s/%s** (%d steps).", owner, repo, len(state.Plan)))
			postOrchestratorGitHubStatusFn(owner, repo, "success", fmt.Sprintf("%d steps complete", len(state.Plan)))
		}
		return nil
	}); dbErr != nil {
		log.Printf("[orchestrator %s/%s] db.WithProject: %v", owner, repo, dbErr)
	}
}

// runOrchestratorFn is the injectable orchestrator runner used by main.go.
// Tests replace it; production binds it to ai.RunAutonomousProject.
var runOrchestratorFn = ai.RunAutonomousProject

// postOrchestratorGitHubStatusFn is the injectable hook used to post commit
// status / PR comments back to GitHub at phase boundaries. Bound to github
// package writeback below.
var postOrchestratorGitHubStatusFn = func(owner, repo, phase, detail string) {
	if strings.TrimSpace(os.Getenv("GITHUB_TOKEN")) == "" {
		return
	}
	sha, err := github.FindHeadSHA(owner, repo, "HEAD")
	if err != nil || strings.TrimSpace(sha) == "" {
		return
	}
	if err := github.PostCommitStatus(owner, repo, sha, phase, "engine/orchestrator", phase+": "+detail, ""); err != nil {
		log.Printf("[github status %s/%s] %v", owner, repo, err)
	}
}

func phaseIcon(phase string) string {
	switch phase {
	case "plan":
		return "📋"
	case "execute":
		return "🛠️"
	case "review":
		return "🔍"
	case "validate":
		return "🧪"
	case "done":
		return "✅"
	case "failure":
		return "❌"
	default:
		return "🔁"
	}
}

// startMeshServer brings up the LAN compute mesh listener in a background
// goroutine. Failure is logged but does not block the main server start so a
// misconfigured mesh.json never knocks out core engine.
func startMeshServer() {
	cfg, err := mesh.LoadConfig("")
	if err != nil {
		log.Printf("[mesh] config load error: %v", err)
		return
	}
	go func() {
		srv := mesh.NewServer(cfg)
		if err := srv.ListenAndServe(context.Background()); err != nil {
			log.Printf("[mesh] listener exited: %v", err)
		}
	}()
}

func doneCount(state *ai.OrchestrationState) int {
	if state == nil {
		return 0
	}
	n := 0
	for _, s := range state.Plan {
		if s.Done {
			n++
		}
	}
	return n
}

// countScaffoldSessions returns the number of scaffold-<repo>-* sessions for
// the project. Used to cap automatic retries.
func countScaffoldSessions(projectPath, repo string) int {
	count := 0
	_ = db.WithProject(projectPath, func() error {
		rows, err := dbListSessionsForScaffoldFn(projectPath)
		if err != nil {
			return nil
		}
		prefix := "scaffold-" + strings.ToLower(repo) + "-"
		for _, row := range rows {
			if strings.HasPrefix(strings.ToLower(row.ID), prefix) {
				count++
			}
		}
		return nil
	})
	return count
}

// scaffoldPriorAttemptContext returns a human-readable summary of the last
// scaffold sessions for the repo so a retried scaffold prompt can feed prior
// attempts back into the AI's context window. Empty string when nothing prior
// is available. This is the loop's primary memory mechanism between retries.
func scaffoldPriorAttemptContext(projectPath, repo string) string {
	var b strings.Builder
	_ = db.WithProject(projectPath, func() error {
		sessions, err := dbListSessionsForScaffoldFn(projectPath)
		if err != nil {
			return nil
		}
		prefix := "scaffold-" + strings.ToLower(repo) + "-"
		var prior []db.Session
		for _, row := range sessions {
			if strings.HasPrefix(strings.ToLower(row.ID), prefix) {
				prior = append(prior, row)
			}
		}
		if len(prior) == 0 {
			return nil
		}
		// sessions list is ordered DESC by updated_at; show up to 3 most recent.
		shown := prior
		if len(shown) > 3 {
			shown = shown[:3]
		}
		fmt.Fprintf(&b, "Prior scaffold attempts (%d total):\n", len(prior))
		for _, s := range shown {
			summary := strings.TrimSpace(s.Summary)
			if summary == "" {
				if msgs, mErr := db.GetMessages(s.ID); mErr == nil && len(msgs) > 0 {
					last := strings.TrimSpace(msgs[len(msgs)-1].Content)
					if len(last) > 280 {
						last = last[:280] + "…"
					}
					summary = last
				}
			}
			if summary == "" {
				summary = "(no message recorded)"
			}
			fmt.Fprintf(&b, "- %s (msgs=%d, updated=%s): %s\n", s.ID, s.MessageCount, s.UpdatedAt, summary)
		}
		b.WriteString("\nThis is a continuation of those attempts. Do NOT restart from scratch — read PROJECT_GOAL.md and existing files first, identify what is already done, and pick up from where the prior attempt stopped. Address the specific blocker shown above.")
		return nil
	})
	return strings.TrimSpace(b.String())
}

// scheduleScaffoldRetry schedules a delayed re-trigger of the scaffold session
// when all internal attempts have been exhausted. It caps retries at
// scaffoldMaxRetries total scaffold sessions so a permanently broken project
// does not spin forever.
func scheduleScaffoldRetry(projectPath, owner, repo string, payload json.RawMessage, reason string) {
	total := countScaffoldSessions(projectPath, repo)
	if total >= scaffoldMaxRetries {
		notifyDiscordProjectProgress(projectPath, fmt.Sprintf(
			"🛑 Scaffold for **%s/%s** has reached the maximum of %d attempts. Stopping automatic retries. Push a new README change or fix the build environment to restart.",
			owner, repo, scaffoldMaxRetries,
		))
		log.Printf("[scaffold %s/%s] max retries (%d) reached; not scheduling another", owner, repo, scaffoldMaxRetries)
		return
	}
	notifyDiscordProjectProgress(projectPath, fmt.Sprintf(
		"🔄 Scaffold for **%s/%s** will retry in %s (attempt %d/%d). Last issue: %s",
		owner, repo, scaffoldRetryDelay, total+1, scaffoldMaxRetries, reason,
	))
	log.Printf("[scaffold %s/%s] scheduling retry in %s (session count %d)", owner, repo, scaffoldRetryDelay, total)
	time.AfterFunc(scaffoldRetryDelay, func() {
		triggerScaffoldSessionFn(projectPath, payload)
	})
}

func isRetryableAutonomousIssueError(msg string) bool {
	trimmed := strings.TrimSpace(msg)
	if trimmed == "" {
		return false
	}
	lower := strings.ToLower(trimmed)
	if strings.Contains(lower, "without signal_done") {
		return true
	}
	return ai.ClassifyBlocker(trimmed) == ai.BlockerAIResolvable
}

func buildAutonomousIssueRetryPrompt(initialPrompt, lastErr string, attempt, maxAttempts int) string {
	parts := []string{
		"This is a continuation of the current autonomous issue session after an AI-resolvable stall.",
		fmt.Sprintf("Recovery attempt %d of %d.", attempt, maxAttempts),
		"Do not restart from scratch. Inspect the current repository state, use recent session history, and continue from the existing working tree.",
		"If the path forward is ambiguous, choose the safest reasonable default, prefix it with 'Assumption:', and keep going.",
		"Use tools, run focused validation, commit finished work, and call signal_done only when the issue is actually complete.",
	}
	if trimmed := strings.TrimSpace(lastErr); trimmed != "" {
		parts = append(parts, "Previous blocker: "+trimmed)
	}
	parts = append(parts, "Original objective:\n"+strings.TrimSpace(initialPrompt))
	return strings.Join(parts, "\n\n")
}

func runAutonomousIssueAttempts(ctx *ai.ChatContext, initialPrompt string, onRetry func(nextAttempt int, msg string)) string {
	prompt := initialPrompt
	attempt := 1
	for {
		attemptErr := ""
		ctx.OnError = func(msg string) {
			trimmed := strings.TrimSpace(msg)
			if trimmed == "" {
				trimmed = "unknown failure"
			}
			attemptErr = trimmed
		}
		aiChatFn(ctx, prompt)
		if attemptErr == "" {
			return ""
		}
		if attempt >= autonomousIssueMaxAttempts || !isRetryableAutonomousIssueError(attemptErr) {
			return attemptErr
		}
		if onRetry != nil {
			onRetry(attempt+1, attemptErr)
		}
		prompt = buildAutonomousIssueRetryPrompt(initialPrompt, attemptErr, attempt+1, autonomousIssueMaxAttempts)
		attempt++
	}
}

// triggerCIAnalysisSession fires an AI session when a CI failure is detected.
func triggerCIAnalysisSession(projectPath string, payload json.RawMessage) {
	var ciEvent struct {
		WorkflowRun struct {
			Name       string `json:"name"`
			HTMLURL    string `json:"html_url"`
			Conclusion string `json:"conclusion"`
		} `json:"workflow_run"`
		Repository struct {
			FullName string `json:"full_name"`
		} `json:"repository"`
	}
	if err := json.Unmarshal(payload, &ciEvent); err != nil {
		log.Printf("ci-analysis: cannot parse CI event: %v", err)
		return
	}

	if dbErr := db.WithProject(projectPath, func() error {
		sessionID := fmt.Sprintf("ci-fix-%d", time.Now().UnixNano())
		branch, _ := gogit.GetCurrentBranch(projectPath)
		if err := createSessionFn(sessionID, projectPath, branch); err != nil {
			log.Printf("[ci-fix %s] create session: %v", ciEvent.Repository.FullName, err)
		}
		ciPolicy := ai.ResolveAutonomousPolicy(projectPath)
		ctx := &ai.ChatContext{
			ProjectPath:      projectPath,
			SessionID:        sessionID,
			AutonomousPolicy: &ciPolicy,
			OnChunk: func(content string, done bool) {
				if content != "" {
					log.Printf("[ci-fix %s] %s", ciEvent.Repository.FullName, content)
				}
				if done {
					msgID := fmt.Sprintf("%s-reply-%d", sessionID, time.Now().UnixNano())
					if dbErr := saveMessageFn(msgID, sessionID, "assistant", content, nil); dbErr != nil {
						log.Printf("[ci-fix %s] save message: %v", ciEvent.Repository.FullName, dbErr)
					}
				}
			},
			OnError: func(msg string) {
				log.Printf("[ci-fix error] %s", msg)
			},
		}

		prompt := fmt.Sprintf(
			"CI workflow '%s' failed for %s (conclusion: %s, url: %s).\n\n"+
				"Your job:\n"+
				"1. Use git_status and search_files to find recent changes\n"+
				"2. Run the failing tests or build command with the shell tool to reproduce the failure\n"+
				"3. Identify the root cause\n"+
				"4. Fix the issue\n"+
				"5. Verify the fix by running the tests again\n"+
				"6. Commit the fix with git_commit\n"+
				"Start by reproducing the failure now.",
			ciEvent.WorkflowRun.Name,
			ciEvent.Repository.FullName,
			ciEvent.WorkflowRun.Conclusion,
			ciEvent.WorkflowRun.HTMLURL,
		)
		aiChatFn(ctx, prompt)
		return nil
	}); dbErr != nil {
		log.Printf("[ci-fix %s] db.WithProject: %v", ciEvent.Repository.FullName, dbErr)
	}
}

// triggerIssueSession fires an AI session when a new comment is posted on a GitHub issue.
// It parses the issue_comment payload and asks the AI to understand and fix the issue.
func triggerIssueSession(projectPath string, payload json.RawMessage) {
	parsed, err := github.ParseIssueComment(&github.WebhookEvent{Type: "issue_comment", Payload: payload})
	if err != nil || parsed.Issue.Number == 0 {
		log.Printf("issue-session: cannot parse issue_comment payload: %v", err)
		return
	}
	if !reserveIssueCommentDispatch(projectPath, parsed.Repository.FullName, parsed.Issue.Number, parsed.Comment.ID, parsed.Comment.Body) {
		log.Printf("issue-session: suppressing duplicate issue comment for #%d in %s", parsed.Issue.Number, parsed.Repository.FullName)
		return
	}

	owner, repo, ok := parseOwnerRepo(parsed.Repository.FullName)
	if !ok {
		log.Printf("issue-session: unexpected full_name format: %s", parsed.Repository.FullName)
		return
	}
	targetProjectPath, workspaceErr := ensureAutonomousRepoWorkspace(projectPath, owner, repo)
	if workspaceErr != nil {
		log.Printf("issue-session: clone/sync failed for %s/%s: %v", owner, repo, workspaceErr)
		return
	}
	autoEnrollDiscordProject(targetProjectPath, owner, repo)
	if handle := ai.GetOrchestratorHandle(targetProjectPath); handle != nil {
		redirectMsg := fmt.Sprintf(
			"New issue comment on #%d in %s from %s.\n\nComment:\n%s\n\nQueue this into the current plan at the next boundary. If this changes acceptance criteria, revise affected steps explicitly.",
			parsed.Issue.Number,
			parsed.Repository.FullName,
			parsed.Comment.User.Login,
			parsed.Comment.Body,
		)
		handle.Redirect(redirectMsg)
		log.Printf("issue-session: routed comment on issue #%d into active orchestrator for %s/%s", parsed.Issue.Number, owner, repo)
		notifyDiscordProjectProgress(targetProjectPath, fmt.Sprintf("📨 Issue comment on #%d routed into active orchestrator for **%s/%s** (queued for next step boundary).", parsed.Issue.Number, owner, repo))
		github.EngageOnIssuePickup(targetProjectPath, owner, repo, parsed.Issue.Number, "orchestrator-comment")
		return
	}

	notifyDiscordProjectProgress(targetProjectPath, fmt.Sprintf("🛠️ Kickoff: working issue #%d for **%s/%s** after a new issue comment.", parsed.Issue.Number, owner, repo))
	github.EngageOnIssuePickup(targetProjectPath, owner, repo, parsed.Issue.Number, fmt.Sprintf("issue-%d", parsed.Issue.Number))

	if dbErr := db.WithProject(targetProjectPath, func() error {
		brief := fmt.Sprintf(
			"GitHub issue #%d '%s' in %s — new comment from %s.\n\n"+
				"Comment:\n%s\n\n"+
				"Execution contract:\n"+
				"- Understand original issue + comment intent and align with current project direction.\n"+
				"- Implement the fix end-to-end, run relevant tests/checks, and ship complete verified changes.\n"+
				"- If acceptance criteria need updates, revise plan steps explicitly before continuing.\n",
			parsed.Issue.Number,
			parsed.Issue.Title,
			parsed.Repository.FullName,
			parsed.Comment.User.Login,
			parsed.Comment.Body,
		)
		state, runErr := runOrchestratorFn(ai.OrchestratorConfig{
			ProjectPath:     targetProjectPath,
			Owner:           owner,
			Repo:            repo,
			Brief:           brief,
			SessionIDPrefix: fmt.Sprintf("issue-comment-%d", parsed.Issue.Number),
			ChatFn:          aiChatFn,
			OnPhase: func(phase, detail string) {
				log.Printf("[issue-session orchestrator %s/%s #%d] phase=%s %s", owner, repo, parsed.Issue.Number, phase, detail)
				notifyDiscordProjectProgress(targetProjectPath, fmt.Sprintf("%s **%s/%s issue #%d**: %s — %s", phaseIcon(phase), owner, repo, parsed.Issue.Number, phase, detail))
			},
			OnError: func(msg string) {
				if strings.TrimSpace(msg) != "" {
					log.Printf("[issue-session orchestrator error %s/%s #%d] %s", owner, repo, parsed.Issue.Number, msg)
				}
			},
		})
		if runErr != nil {
			notifyDiscordProjectProgress(targetProjectPath, fmt.Sprintf("❌ Issue session blocked for **%s** (#%d): %s", parsed.Repository.FullName, parsed.Issue.Number, runErr.Error()))
			github.EngageOnIssueBlocked(targetProjectPath, owner, repo, parsed.Issue.Number, runErr.Error())
			return nil
		}
		if state != nil && strings.TrimSpace(state.CompletedAt) != "" {
			notifyDiscordProjectProgress(targetProjectPath, fmt.Sprintf("✅ Issue session finished for **%s** (#%d).", parsed.Repository.FullName, parsed.Issue.Number))
		}
		github.EngageOnIssueComplete(targetProjectPath, owner, repo, parsed.Issue.Number, 0, "")
		return nil
	}); dbErr != nil {
		log.Printf("[issue #%d] db.WithProject: %v", parsed.Issue.Number, dbErr)
	}
}

func issueCommentDispatchKey(projectPath, fullName string, issueNumber int, commentID int64, body string) string {
	projectKey := strings.TrimSpace(projectPath)
	if commentID > 0 {
		return fmt.Sprintf("%s#%s#%d#%d", projectKey, strings.TrimSpace(fullName), issueNumber, commentID)
	}
	trimmedBody := strings.TrimSpace(body)
	if len(trimmedBody) > 160 {
		trimmedBody = trimmedBody[:160]
	}
	return fmt.Sprintf("%s#%s#%d#%s", projectKey, strings.TrimSpace(fullName), issueNumber, trimmedBody)
}

func reserveIssueCommentDispatch(projectPath, fullName string, issueNumber int, commentID int64, body string) bool {
	key := issueCommentDispatchKey(projectPath, fullName, issueNumber, commentID, body)
	now := time.Now()
	issueCommentTriggerMu.Lock()
	defer issueCommentTriggerMu.Unlock()
	if last, ok := issueCommentLastDispatch[key]; ok && now.Sub(last) < issueCommentTriggerCooldown {
		return false
	}
	issueCommentLastDispatch[key] = now
	if len(issueCommentLastDispatch) > 4096 {
		cutoff := now.Add(-2 * issueCommentTriggerCooldown)
		for k, t := range issueCommentLastDispatch {
			if t.Before(cutoff) {
				delete(issueCommentLastDispatch, k)
			}
		}
	}
	return true
}

// triggerIssueOpenedSession fires an AI session when a new GitHub issue is opened.
// It parses the issues payload and asks the AI to understand and fix the issue.
func triggerIssueOpenedSession(projectPath string, payload json.RawMessage) {
	parsed, err := github.ParseIssue(&github.WebhookEvent{Type: "issues", Payload: payload})
	if err != nil || parsed.Issue.Number == 0 {
		log.Printf("issue-opened: cannot parse issues payload: %v", err)
		return
	}

	owner, repo, ok := parseOwnerRepo(parsed.Repository.FullName)
	if !ok {
		log.Printf("issue-opened: unexpected full_name format: %s", parsed.Repository.FullName)
		return
	}
	targetProjectPath, workspaceErr := ensureAutonomousRepoWorkspace(projectPath, owner, repo)
	if workspaceErr != nil {
		log.Printf("issue-opened: clone/sync failed for %s/%s: %v", owner, repo, workspaceErr)
		return
	}
	autoEnrollDiscordProject(targetProjectPath, owner, repo)

	// One orchestrator per project. If a manager is already running for this
	// repo, feed the issue in as a redirect instead of spawning a parallel
	// session that would race the orchestrator on the same clone tree.
	if handle := ai.GetOrchestratorHandle(targetProjectPath); handle != nil {
		redirectMsg := fmt.Sprintf(
			"New GitHub issue #%d opened by %s in %s.\n\nTitle: %s\n\nBody:\n%s\n\nIncorporate this into the current plan as appropriate — adjust scope, add steps, or revise pending steps. Keep the existing acceptance criteria where they still apply.",
			parsed.Issue.Number, parsed.Sender.Login, parsed.Repository.FullName,
			parsed.Issue.Title, parsed.Issue.Body,
		)
		handle.Redirect(redirectMsg)
		log.Printf("issue-opened: routed issue #%d into active orchestrator for %s/%s", parsed.Issue.Number, owner, repo)
		notifyDiscordProjectProgress(targetProjectPath, fmt.Sprintf("📨 Issue #%d routed into active orchestrator for **%s/%s** (will land at next step boundary).", parsed.Issue.Number, owner, repo))
		// Acknowledge on GitHub even when routed into the orchestrator.
		github.EngageOnIssuePickup(targetProjectPath, owner, repo, parsed.Issue.Number, "orchestrator")
		return
	}

	notifyDiscordProjectProgress(targetProjectPath, fmt.Sprintf("🛠️ Kickoff: working newly opened issue #%d for **%s/%s**.", parsed.Issue.Number, owner, repo))
	github.EngageOnIssuePickup(targetProjectPath, owner, repo, parsed.Issue.Number, fmt.Sprintf("issue-opened-%d", parsed.Issue.Number))

	if dbErr := db.WithProject(targetProjectPath, func() error {
		brief := fmt.Sprintf(
			"GitHub issue #%d opened in %s by %s.\n\n"+
				"Title: %s\n"+
				"Body:\n%s\n\n"+
				"Execution contract:\n"+
				"- Resolve the issue end-to-end in this repo with full verification.\n"+
				"- Preserve project direction while adapting plan steps to this issue.\n"+
				"- Run required tests/checks, complete implementation, and finish with a validated result.\n",
			parsed.Issue.Number,
			parsed.Repository.FullName,
			parsed.Sender.Login,
			parsed.Issue.Title,
			parsed.Issue.Body,
		)
		state, runErr := runOrchestratorFn(ai.OrchestratorConfig{
			ProjectPath:     targetProjectPath,
			Owner:           owner,
			Repo:            repo,
			Brief:           brief,
			SessionIDPrefix: fmt.Sprintf("issue-opened-%d", parsed.Issue.Number),
			ChatFn:          aiChatFn,
			OnPhase: func(phase, detail string) {
				log.Printf("[issue-opened orchestrator %s/%s #%d] phase=%s %s", owner, repo, parsed.Issue.Number, phase, detail)
				notifyDiscordProjectProgress(targetProjectPath, fmt.Sprintf("%s **%s/%s issue #%d**: %s — %s", phaseIcon(phase), owner, repo, parsed.Issue.Number, phase, detail))
			},
			OnError: func(msg string) {
				if strings.TrimSpace(msg) != "" {
					log.Printf("[issue-opened orchestrator error %s/%s #%d] %s", owner, repo, parsed.Issue.Number, msg)
				}
			},
		})
		if runErr != nil {
			notifyDiscordProjectProgress(targetProjectPath, fmt.Sprintf("❌ Issue-opened session blocked for **%s** (#%d): %s", parsed.Repository.FullName, parsed.Issue.Number, runErr.Error()))
			github.EngageOnIssueBlocked(targetProjectPath, owner, repo, parsed.Issue.Number, runErr.Error())
			return nil
		}
		if state != nil && strings.TrimSpace(state.CompletedAt) != "" {
			notifyDiscordProjectProgress(targetProjectPath, fmt.Sprintf("✅ Issue-opened session finished for **%s** (#%d).", parsed.Repository.FullName, parsed.Issue.Number))
		}
		github.EngageOnIssueComplete(targetProjectPath, owner, repo, parsed.Issue.Number, 0, "")
		return nil
	}); dbErr != nil {
		log.Printf("[issue-opened #%d] db.WithProject: %v", parsed.Issue.Number, dbErr)
	}
}
