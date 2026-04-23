package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"

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
	bootID := os.Getenv("ENGINE_LOCAL_BOOT_ID")

	http.HandleFunc("/ws", hub.ServeWS)
	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"status":"ok","projectPath":%q,"bootId":%q}`, projectPath, bootID)
	})

	// GitHub webhook receiver for repo monitoring.
	webhookSecret := os.Getenv("GITHUB_WEBHOOK_SECRET")
	webhookReceiver := github.NewWebhookReceiver(webhookSecret)
	repoMonitor := github.NewRepoMonitor()
	repoMonitor.OnReadmeChange = func(payload json.RawMessage) {
		log.Printf("README changed: triggering AI summary (payload size: %d bytes)", len(payload))
		// TODO: trigger AI session to summarize README change
	}
	repoMonitor.OnCIFailure = func(payload json.RawMessage) {
		log.Printf("CI failure detected: queuing AI analysis")
		// TODO: trigger AI session to analyze CI failure
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
