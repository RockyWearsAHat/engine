package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/engine/server/ai"
	"github.com/engine/server/db"
	"github.com/engine/server/discord"
	"github.com/engine/server/github"
	"github.com/engine/server/remote"
	"github.com/engine/server/vpn"
	"github.com/engine/server/ws"
)

func defaultProjectPath() string {
	if cwd, err := os.Getwd(); err == nil && cwd != "" {
		return cwd
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return home
	}
	return "."
}

func main() {
	projectPath := os.Getenv("PROJECT_PATH")
	if projectPath == "" {
		projectPath = defaultProjectPath()
	}

	if err := db.Init(projectPath); err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}

	hub := ws.NewHub(projectPath)

	if cfg, err := discord.LoadConfig(projectPath); err != nil {
		log.Fatalf("Invalid Discord config: %v", err)
	} else if cfg.Enabled {
		discordService, err := discord.NewService(cfg, projectPath)
		if err != nil {
			log.Fatalf("Failed to initialize Discord service: %v", err)
		}
		if err := discordService.Start(); err != nil {
			log.Fatalf("Failed to start Discord service: %v", err)
		}
		ws.SetDiscordBridge(discordService)
		defer discordService.Close() //nolint:errcheck
	} else {
		// Even when disabled, allow the UI to save/validate config via WS by
		// wiring a stub that proxies only the bridge methods relying on the
		// config + archive. We construct a non-started service so CurrentConfig
		// and history queries work.
		if stub, err := discord.NewService(cfg, projectPath); err == nil {
			ws.SetDiscordBridge(stub)
		}
	}

	// VPN tunnel mode: ENGINE_VPN=1 starts Ed25519-authenticated tunnel on top of TLS
	if os.Getenv("ENGINE_VPN") == "1" {
		vpnCfg := vpn.DefaultConfig()
		if port := os.Getenv("VPN_PORT"); port != "" {
			vpnCfg.Port = port
		}
		vpnCfg.Enabled = true

		tunnel, err := vpn.NewTunnel(vpnCfg)
		if err != nil {
			log.Fatalf("Failed to start VPN tunnel: %v", err)
		}

		mux := http.NewServeMux()
		tunnel.RegisterRoutes(mux, hub.ServeWS)
		log.Fatal(tunnel.ListenAndServeTLS(mux))
		return
	}

	// Remote mode: ENGINE_REMOTE=1 starts a TLS-secured server with pairing and auth
	if os.Getenv("ENGINE_REMOTE") == "1" {
		cfg := remote.DefaultConfig()
		if port := os.Getenv("REMOTE_PORT"); port != "" {
			cfg.Port = port
		}
		cfg.Enabled = true

		srv, err := remote.NewServer(cfg, hub.ServeWS)
		if err != nil {
			log.Fatalf("Failed to start remote server: %v", err)
		}

		log.Fatal(srv.ListenAndServeTLS())
		return
	}

	// Local mode: plain HTTP, no authentication needed
	port := os.Getenv("PORT")
	if port == "" {
		port = "3000"
	}
	http.HandleFunc("/ws", hub.ServeWS)
	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
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
	webhookReceiver := github.NewWebhookReceiver(webhookSecret)
	repoMonitor := github.NewRepoMonitor()
	repoMonitor.OnReadmeChange = func(payload json.RawMessage) {
		log.Printf("README changed: launching AI scaffold session (payload %d bytes)", len(payload))
		go triggerScaffoldSession(projectPath, payload)
	}
	repoMonitor.OnCIFailure = func(payload json.RawMessage) {
		log.Printf("CI failure: launching AI analysis session (payload %d bytes)", len(payload))
		go triggerCIAnalysisSession(projectPath, payload)
	}
	repoMonitor.OnIssueComment = func(payload json.RawMessage) {
		log.Printf("Issue comment received (payload size: %d bytes)", len(payload))
	}
	webhookReceiver.AddHandler(repoMonitor.Enqueue)
	repoMonitor.Start(context.Background())
	// Register the webhook route.
	http.Handle("/webhook/github", webhookReceiver)

	addr := ":" + port
	fmt.Printf("Server running on http://localhost%s\n", addr)
	log.Fatal(http.ListenAndServe(addr, nil))
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

	sessionID := fmt.Sprintf("scaffold-%s-%d", repo, time.Now().UnixNano())
	ctx := &ai.ChatContext{
		ProjectPath: projectPath,
		SessionID:   sessionID,
		OnChunk: func(content string, done bool) {
			if content != "" {
				log.Printf("[scaffold %s/%s] %s", owner, repo, content)
			}
		},
		OnError: func(msg string) {
			log.Printf("[scaffold error %s/%s] %s", owner, repo, msg)
		},
	}

	prompt := fmt.Sprintf(
		"The GitHub repository %s/%s just had its README updated.\n\n"+
			"Your job:\n"+
			"1. Use the shell tool to fetch the README: curl -s https://raw.githubusercontent.com/%s/%s/HEAD/README.md\n"+
			"2. Understand what this project is trying to build\n"+
			"3. Create or update PROJECT_GOAL.md in the project root with a clear plan\n"+
			"4. Scaffold the initial directory structure and key files if they don't exist yet\n"+
			"5. Commit your scaffold work with git_commit\n"+
			"Start by fetching the README now.",
		owner, repo, owner, repo,
	)
	ai.Chat(ctx, prompt)
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
	ctx := &ai.ChatContext{
		ProjectPath: projectPath,
		SessionID:   sessionID,
		OnChunk: func(content string, done bool) {
			if content != "" {
				log.Printf("[ci-fix %s] %s", ciEvent.Repository.FullName, content)
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
	ai.Chat(ctx, prompt)
}
