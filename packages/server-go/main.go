package main

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
	"time"

	"github.com/engine/server/ai"
	"github.com/engine/server/db"
	"github.com/engine/server/discord"
	gogit "github.com/engine/server/git"
	"github.com/engine/server/github"
	"github.com/engine/server/remote"
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
}

var (
	runFn                = run
	logFatalFn           = log.Fatal
	dbInitFn             = db.Init
	createSessionFn      = db.CreateSession
	saveMessageFn        = db.SaveMessage
	newHubFn             = ws.NewHub
	loadDiscordConfigFn  = discord.LoadConfig
	newDiscordServiceFn  = func(cfg discord.Config, projectPath string) (discordRuntime, error) { return discord.NewService(cfg, projectPath) }
	setDiscordBridgeFn   = ws.SetDiscordBridge
	newWebhookReceiverFn = github.NewWebhookReceiver
	newRepoMonitorFn     = github.NewRepoMonitor
	repoMonitorStartFn   = func(rm *github.RepoMonitor) { rm.Start(context.Background()) }
	newVPNTunnelFn       = vpn.NewTunnel
	vpnRegisterRoutesFn  = (*vpn.Tunnel).RegisterRoutes
	vpnListenTLSFn       = (*vpn.Tunnel).ListenAndServeTLS
	newRemoteServerFn    = remote.NewServer
	setPairingManagerFn  = ws.SetPairingManager
	remoteListenTLSFn    = (*remote.Server).ListenAndServeTLS
	aiChatFn             = ai.Chat
	httpHandleFuncFn     = http.HandleFunc
	httpHandleFn         = http.Handle
	httpListenAndServeFn = http.ListenAndServe
	runAsyncFn           = func(fn func()) { go fn() }
	triggerScaffoldSessionFn = triggerScaffoldSession
	triggerCIAnalysisSessionFn = triggerCIAnalysisSession
	triggerIssueSessionFn = triggerIssueSession
	triggerIssueOpenedSessionFn = triggerIssueOpenedSession
	runCommandCombinedOutputFn = func(name string, args ...string) ([]byte, error) {
		cmd := exec.Command(name, args...)
		return cmd.CombinedOutput()
	}
)

// osGetwdFn and osUserHomeDirFn are injectable for tests.
var (
	osGetwdFn        = os.Getwd
	osUserHomeDirFn  = os.UserHomeDir
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

	// GitHub webhook receiver for repo monitoring.
	webhookSecret := os.Getenv("GITHUB_WEBHOOK_SECRET")
	webhookReceiver := newWebhookReceiverFn(webhookSecret)
	repoMonitor := newRepoMonitorFn()
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
	// Register the webhook route.
	httpHandleFn("/webhook/github", webhookReceiver)

	addr := ":" + port
	fmt.Printf("Server running on http://localhost%s\n", addr)
	return httpListenAndServeFn(addr, nil)
}

func buildAutonomousRepoPath(baseProjectPath, owner, repo string) string {
	clonesDir := strings.TrimSpace(os.Getenv("ENGINE_CLONES_DIR"))
	if clonesDir == "" {
		clonesDir = filepath.Join(baseProjectPath, ".engine", "projects")
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
		if out, cmdErr := runCommandCombinedOutputFn("git", "-C", dest, "fetch", "origin", "--prune"); cmdErr != nil {
			return "", fmt.Errorf("fetch repo update: %v: %s", cmdErr, strings.TrimSpace(string(out)))
		}
		if out, cmdErr := runCommandCombinedOutputFn("git", "-C", dest, "pull", "--ff-only", "origin", "HEAD"); cmdErr != nil {
			return "", fmt.Errorf("pull repo update: %v: %s", cmdErr, strings.TrimSpace(string(out)))
		}
		return dest, nil
	}

	repoURL := fmt.Sprintf("https://github.com/%s/%s.git", owner, repo)
	if out, cmdErr := runCommandCombinedOutputFn("git", "clone", repoURL, dest); cmdErr != nil {
		return "", fmt.Errorf("clone repo %s: %v: %s", repoURL, cmdErr, strings.TrimSpace(string(out)))
	}
	return dest, nil
}

func buildReadmeAutonomousBuildPrompt(owner, repo, localRepoPath string) string {
	readmePath := filepath.Join(localRepoPath, "README.md")
	return fmt.Sprintf(
		"The GitHub repository %s/%s needs an end-to-end autonomous build from its README.\n\n"+
			"Execution contract (must complete all phases):\n"+
			"1. Understand\n"+
			"- Read local README first: %s\n"+
			"- If missing/stale, fetch latest README with shell: curl -s https://raw.githubusercontent.com/%s/%s/HEAD/README.md\n"+
			"- Write a concrete implementation plan to %s\n\n"+
			"2. Scaffold\n"+
			"- Create missing project structure and bootstrap files in %s\n\n"+
			"3. Implement\n"+
			"- Build the feature set described by the README, not just stubs\n\n"+
			"4. Validate\n"+
			"- Run the real build/test commands with shell in %s\n"+
			"- If no tests exist, add a minimal meaningful test and run it\n"+
			"- Iterate until checks pass or you are genuinely blocked\n\n"+
			"5. Deliver\n"+
			"- Commit all completed work with git_commit\n"+
			"- Final response must include: files created/changed, commands run, and test/build results\n\n"+
			"Autonomy rule: continue without asking for input unless blocked by missing credentials/permissions/requirements.",
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
	parts := strings.SplitN(pushEvent.Repository.FullName, "/", 2)
	if len(parts) != 2 {
		log.Printf("scaffold: unexpected full_name format: %s", pushEvent.Repository.FullName)
		return
	}
	owner, repo := parts[0], parts[1]
	targetProjectPath, err := ensureAutonomousRepoWorkspace(projectPath, owner, repo)
	if err != nil {
		log.Printf("scaffold: clone/sync failed for %s/%s: %v; falling back to default project path", owner, repo, err)
		targetProjectPath = projectPath
	}

	sessionID := fmt.Sprintf("scaffold-%s-%d", repo, time.Now().UnixNano())
	branch, _ := gogit.GetCurrentBranch(targetProjectPath)
	if err := createSessionFn(sessionID, targetProjectPath, branch); err != nil {
		log.Printf("[scaffold %s/%s] create session: %v", owner, repo, err)
	}
	scaffoldPolicy := ai.ResolveAutonomousPolicy(targetProjectPath)
	ctx := &ai.ChatContext{
		ProjectPath:     targetProjectPath,
		SessionID:       sessionID,
		AutonomousPolicy: &scaffoldPolicy,
		OnChunk: func(content string, done bool) {
			if content != "" {
				log.Printf("[scaffold %s/%s] %s", owner, repo, content)
			}
			if done {
				msgID := fmt.Sprintf("%s-reply-%d", sessionID, time.Now().UnixNano())
				if dbErr := saveMessageFn(msgID, sessionID, "assistant", content, nil); dbErr != nil {
					log.Printf("[scaffold %s/%s] save message: %v", owner, repo, dbErr)
				}
			}
		},
		OnError: func(msg string) {
			log.Printf("[scaffold error %s/%s] %s", owner, repo, msg)
		},
	}

	prompt := buildReadmeAutonomousBuildPrompt(owner, repo, targetProjectPath)
	aiChatFn(ctx, prompt)
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

	sessionID := fmt.Sprintf("ci-fix-%d", time.Now().UnixNano())
	branch, _ := gogit.GetCurrentBranch(projectPath)
	if err := createSessionFn(sessionID, projectPath, branch); err != nil {
		log.Printf("[ci-fix %s] create session: %v", ciEvent.Repository.FullName, err)
	}
	ciPolicy := ai.ResolveAutonomousPolicy(projectPath)
	ctx := &ai.ChatContext{
		ProjectPath:     projectPath,
		SessionID:       sessionID,
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
}

// triggerIssueSession fires an AI session when a new comment is posted on a GitHub issue.
// It parses the issue_comment payload and asks the AI to understand and fix the issue.
func triggerIssueSession(projectPath string, payload json.RawMessage) {
	parsed, err := github.ParseIssueComment(&github.WebhookEvent{Type: "issue_comment", Payload: payload})
	if err != nil || parsed.Issue.Number == 0 {
		log.Printf("issue-session: cannot parse issue_comment payload: %v", err)
		return
	}

	sessionID := fmt.Sprintf("issue-%d-%d", parsed.Issue.Number, time.Now().UnixNano())
	branch, _ := gogit.GetCurrentBranch(projectPath)
	if dbErr := createSessionFn(sessionID, projectPath, branch); dbErr != nil {
		log.Printf("issue-session: create session: %v", dbErr)
	}

	issuePolicy := ai.ResolveAutonomousPolicy(projectPath)
	ctx := &ai.ChatContext{
		ProjectPath:     projectPath,
		SessionID:       sessionID,
		AutonomousPolicy: &issuePolicy,
		OnChunk: func(content string, done bool) {
			if content != "" {
				log.Printf("[issue #%d %s] %s", parsed.Issue.Number, parsed.Repository.FullName, content)
			}
			if done {
				msgID := fmt.Sprintf("%s-reply-%d", sessionID, time.Now().UnixNano())
				if dbErr := saveMessageFn(msgID, sessionID, "assistant", content, nil); dbErr != nil {
					log.Printf("issue-session: save message: %v", dbErr)
				}
			}
		},
		OnError: func(msg string) {
			log.Printf("[issue error #%d] %s", parsed.Issue.Number, msg)
		},
	}

	prompt := fmt.Sprintf(
		"GitHub issue #%d '%s' in %s received a new comment from %s.\n\n"+
			"Comment: %s\n\n"+
			"Your job:\n"+
			"1. Read the issue and understand what needs to be fixed\n"+
			"2. Use search_files and read_file to explore the relevant code\n"+
			"3. Write code to fix the issue\n"+
			"4. Run tests with the shell tool to verify the fix\n"+
			"5. Commit the fix with git_commit\n"+
			"Start by exploring the codebase to understand the issue now.",
		parsed.Issue.Number,
		parsed.Issue.Title,
		parsed.Repository.FullName,
		parsed.Comment.User.Login,
		parsed.Comment.Body,
	)
	aiChatFn(ctx, prompt)
}

// triggerIssueOpenedSession fires an AI session when a new GitHub issue is opened.
// It parses the issues payload and asks the AI to understand and fix the issue.
func triggerIssueOpenedSession(projectPath string, payload json.RawMessage) {
	parsed, err := github.ParseIssue(&github.WebhookEvent{Type: "issues", Payload: payload})
	if err != nil || parsed.Issue.Number == 0 {
		log.Printf("issue-opened: cannot parse issues payload: %v", err)
		return
	}

	sessionID := fmt.Sprintf("issue-%d-%d", parsed.Issue.Number, time.Now().UnixNano())
	branch, _ := gogit.GetCurrentBranch(projectPath)
	if dbErr := createSessionFn(sessionID, projectPath, branch); dbErr != nil {
		log.Printf("issue-opened: create session: %v", dbErr)
	}

	issueOpenedPolicy := ai.ResolveAutonomousPolicy(projectPath)
	ctx := &ai.ChatContext{
		ProjectPath:     projectPath,
		SessionID:       sessionID,
		AutonomousPolicy: &issueOpenedPolicy,
		OnChunk: func(content string, done bool) {
			if content != "" {
				log.Printf("[issue-opened #%d %s] %s", parsed.Issue.Number, parsed.Repository.FullName, content)
			}
			if done {
				msgID := fmt.Sprintf("%s-reply-%d", sessionID, time.Now().UnixNano())
				if dbErr := saveMessageFn(msgID, sessionID, "assistant", content, nil); dbErr != nil {
					log.Printf("issue-opened: save message: %v", dbErr)
				}
			}
		},
		OnError: func(msg string) {
			log.Printf("[issue-opened error #%d] %s", parsed.Issue.Number, msg)
		},
	}

	prompt := fmt.Sprintf(
		"A new GitHub issue #%d was opened in %s by %s.\n\n"+
			"Issue title: %s\n"+
			"Issue body: %s\n\n"+
			"Your job:\n"+
			"1. Read the issue and understand what needs to be fixed\n"+
			"2. Use search_files and read_file to explore the relevant code\n"+
			"3. Write code to fix the issue\n"+
			"4. Run tests with the shell tool to verify the fix\n"+
			"5. Commit the fix with git_commit\n"+
			"Start by exploring the codebase to understand the issue now.",
		parsed.Issue.Number,
		parsed.Repository.FullName,
		parsed.Sender.Login,
		parsed.Issue.Title,
		parsed.Issue.Body,
	)
	aiChatFn(ctx, prompt)
}
