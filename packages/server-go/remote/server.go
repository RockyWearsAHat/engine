package remote

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// Config holds settings for the remote access server.
type Config struct {
	Enabled     bool
	Port        string
	StoragePath string
}

// DefaultConfig returns the default remote access configuration.
func DefaultConfig() Config {
	home, _ := os.UserHomeDir()
	return Config{
		Enabled:     false,
		Port:        "3443",
		StoragePath: filepath.Join(home, ".engine", "remote"),
	}
}

// EnsureStorageDir creates the remote storage directory with restrictive permissions.
func EnsureStorageDir(cfg Config) error {
	return os.MkdirAll(cfg.StoragePath, 0700)
}

// Server holds the remote access server state.
type Server struct {
	Config     Config
	Auth       *AuthManager
	Pairing    *PairingManager
	mux        *http.ServeMux
	wsHandler  http.HandlerFunc
}

// NewServer creates a remote access server with TLS, pairing, and authentication.
func NewServer(cfg Config, wsHandler http.HandlerFunc) (*Server, error) {
	if err := EnsureStorageDir(cfg); err != nil {
		return nil, err
	}

	authMgr, err := NewAuthManager(cfg.StoragePath)
	if err != nil {
		return nil, err
	}

	s := &Server{
		Config:    cfg,
		Auth:      authMgr,
		Pairing:   NewPairingManager(),
		mux:       http.NewServeMux(),
		wsHandler: wsHandler,
	}

	s.registerRoutes()
	return s, nil
}

func (s *Server) registerRoutes() {
	// Pairing endpoint — no auth required, validates one-time code
	s.mux.HandleFunc("/remote/pair", s.handlePair)

	// Token refresh — auth required
	s.mux.Handle("/remote/refresh", s.Auth.AuthMiddleware(http.HandlerFunc(s.handleRefresh)))

	// Remote status — no auth, returns basic info
	s.mux.HandleFunc("/remote/status", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"engine": true,
			"remote": true,
			"port":   s.Config.Port,
		})
	})

	// Health endpoint — no auth
	s.mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"status": "ok",
			"remote": true,
		})
	})

	// WebSocket endpoint — auth required
	s.mux.Handle("/ws", s.Auth.AuthMiddleware(http.HandlerFunc(s.wsHandler)))
}

// randReadFn2 is injectable for tests to simulate random-read failure in pair handler.
var randReadFn2 = rand.Read

// genPairingCodeFn is injectable for tests to simulate code-generation failure.
var serverGenPairingCodeFn = func(pm *PairingManager) (string, error) { return pm.GenerateCode() }

// serverListenAndServeTLSFn is injectable for tests to simulate listen error.
var serverListenAndServeTLSFn = func(s *http.Server) error { return s.ListenAndServeTLS("", "") }

// serverPairingCleanupTimerFn is injectable for tests to skip the 1-minute sleep.
var serverPairingCleanupTimerFn = func() { time.Sleep(time.Minute) }

func (s *Server) handlePair(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}

	if !s.Pairing.ValidateCode(req.Code) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"ok":    false,
			"error": "invalid or expired pairing code",
		})
		return
	}

	deviceIDBytes := make([]byte, 16)
	if _, err := randReadFn2(deviceIDBytes); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	deviceID := hex.EncodeToString(deviceIDBytes)

	token, err := s.Auth.IssueToken(deviceID, 24*time.Hour)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	log.Printf("[engine-remote] Device paired: %s...", deviceID[:8])

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"ok":       true,
		"token":    token,
		"deviceId": deviceID,
	})
}

func (s *Server) handleRefresh(w http.ResponseWriter, r *http.Request) {
	token := ExtractToken(r)
	payload, err := s.Auth.ValidateToken(token)
	if err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	newToken, err := s.Auth.IssueToken(payload.DeviceID, 24*time.Hour)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"ok":    true,
		"token": newToken,
	})
}

// ListenAndServeTLS starts the remote access server with TLS.
func (s *Server) ListenAndServeTLS() error {
	tlsCfg, err := LoadOrCreateTLSConfig(s.Config.StoragePath)
	if err != nil {
		return err
	}

	code, err := serverGenPairingCodeFn(s.Pairing)
	if err != nil {
		return err
	}

	server := &http.Server{
		Addr:      "0.0.0.0:" + s.Config.Port,
		Handler:   s.mux,
		TLSConfig: tlsCfg,
	}

	log.Printf("[engine-remote] Secure remote access on port %s (TLS 1.3)", s.Config.Port)
	log.Printf("[engine-remote] Pairing code: %s (expires in 5 minutes)", code)
	log.Printf("[engine-remote] Pair from your device: POST https://<your-ip>:%s/remote/pair", s.Config.Port)

	// Start background cleanup of expired pairing codes
	go func() {
		for {
			serverPairingCleanupTimerFn()
			s.Pairing.Cleanup()
		}
	}()

	return serverListenAndServeTLSFn(server)
}
