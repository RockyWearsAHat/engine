package main

import (
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/engine/server/db"
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
	http.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"status":"ok","projectPath":%q}`, projectPath)
	})

	addr := ":" + port
	fmt.Printf("Server running on http://localhost%s\n", addr)
	log.Fatal(http.ListenAndServe(addr, nil))
}
