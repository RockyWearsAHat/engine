package main

import (
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/engine/server/db"
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

	port := os.Getenv("PORT")
	if port == "" {
		port = "3000"
	}

	if err := db.Init(projectPath); err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}

	hub := ws.NewHub(projectPath)

	http.HandleFunc("/ws", hub.ServeWS)
	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"status":"ok","projectPath":%q}`, projectPath)
	})

	addr := ":" + port
	fmt.Printf("Server running on http://localhost%s\n", addr)
	log.Fatal(http.ListenAndServe(addr, nil))
}
