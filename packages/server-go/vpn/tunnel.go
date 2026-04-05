package vpn

import (
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/engine/server/remote"
)

// Config holds VPN tunnel server settings.
type Config struct {
	Enabled     bool
	Port        string
	StoragePath string
}

// DefaultConfig returns the default VPN configuration.
func DefaultConfig() Config {
	home, _ := os.UserHomeDir()
	return Config{
		Enabled:     false,
		Port:        "8443",
		StoragePath: filepath.Join(home, ".engine", "vpn"),
	}
}

// Tunnel wraps the Engine server with Ed25519-authenticated VPN access.
type Tunnel struct {
	Config   Config
	Identity *Identity
	Trust    *TrustStore
	Auth     *remote.AuthManager
	Pairing  *remote.PairingManager
}

// NewTunnel creates a VPN tunnel with identity keys and trust management.
func NewTunnel(cfg Config) (*Tunnel, error) {
	if err := os.MkdirAll(cfg.StoragePath, 0700); err != nil {
		return nil, fmt.Errorf("create vpn storage: %w", err)
	}

	identity, err := LoadOrCreateIdentity(cfg.StoragePath)
	if err != nil {
		return nil, fmt.Errorf("load identity: %w", err)
	}

	trust, err := NewTrustStore(cfg.StoragePath)
	if err != nil {
		return nil, fmt.Errorf("load trust store: %w", err)
	}

	authMgr, err := remote.NewAuthManager(cfg.StoragePath)
	if err != nil {
		return nil, fmt.Errorf("init auth: %w", err)
	}

	return &Tunnel{
		Config:   cfg,
		Identity: identity,
		Trust:    trust,
		Auth:     authMgr,
		Pairing:  remote.NewPairingManager(),
	}, nil
}

// RegisterRoutes adds VPN-specific endpoints to an HTTP mux.
func (t *Tunnel) RegisterRoutes(mux *http.ServeMux, wsHandler http.HandlerFunc) {
	// Tunnel pairing: client sends their Ed25519 public key + pairing code
	mux.HandleFunc("/vpn/pair", t.handleVPNPair)

	// List trusted devices
	mux.Handle("/vpn/devices", t.Auth.AuthMiddleware(http.HandlerFunc(t.handleDevices)))

	// Revoke a device
	mux.Handle("/vpn/revoke", t.Auth.AuthMiddleware(http.HandlerFunc(t.handleRevoke)))

	// Server identity fingerprint (public, no auth)
	mux.HandleFunc("/vpn/fingerprint", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"fingerprint": hex.EncodeToString(t.Identity.PublicKey),
		})
	})

	// WebSocket with auth
	mux.Handle("/ws", t.Auth.AuthMiddleware(http.HandlerFunc(wsHandler)))

	// Health
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status": "ok",
			"vpn":    true,
		})
	})
}

func (t *Tunnel) handleVPNPair(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Code      string `json:"code"`
		PublicKey string `json:"publicKey"`
		Name      string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}

	if !t.Pairing.ValidateCode(req.Code) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"ok":    false,
			"error": "invalid or expired pairing code",
		})
		return
	}

	pubKeyBytes, err := hex.DecodeString(req.PublicKey)
	if err != nil || len(pubKeyBytes) != ed25519.PublicKeySize {
		http.Error(w, "invalid public key", http.StatusBadRequest)
		return
	}

	deviceID := req.PublicKey[:16]
	deviceName := req.Name
	if deviceName == "" {
		deviceName = "Device " + deviceID[:8]
	}

	if err := t.Trust.AddDevice(deviceID, deviceName, ed25519.PublicKey(pubKeyBytes)); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	token, err := t.Auth.IssueToken(deviceID, 24*time.Hour)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	log.Printf("[engine-vpn] Device trusted: %s (%s)", deviceName, deviceID[:8])

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"ok":               true,
		"token":            token,
		"deviceId":         deviceID,
		"serverPublicKey":  hex.EncodeToString(t.Identity.PublicKey),
		"serverFingerprint": hex.EncodeToString(t.Identity.PublicKey[:16]),
	})
}

func (t *Tunnel) handleDevices(w http.ResponseWriter, _ *http.Request) {
	devices := t.Trust.List()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"devices": devices,
	})
}

func (t *Tunnel) handleRevoke(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		DeviceID string `json:"deviceId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}

	if err := t.Trust.RemoveDevice(req.DeviceID); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	t.Auth.RevokeDevice(req.DeviceID)

	log.Printf("[engine-vpn] Device revoked: %s", req.DeviceID)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"ok": true})
}

// ListenAndServeTLS starts the VPN tunnel with TLS and Ed25519 authentication.
func (t *Tunnel) ListenAndServeTLS(mux *http.ServeMux) error {
	tlsCfg, err := remote.LoadOrCreateTLSConfig(t.Config.StoragePath)
	if err != nil {
		return fmt.Errorf("load tls config: %w", err)
	}

	code, err := t.Pairing.GenerateCode()
	if err != nil {
		return fmt.Errorf("generate pairing code: %w", err)
	}

	server := &http.Server{
		Addr:      "0.0.0.0:" + t.Config.Port,
		Handler:   mux,
		TLSConfig: tlsCfg,
	}

	fingerprint := hex.EncodeToString(t.Identity.PublicKey[:16])
	localIP := getLocalIP()

	log.Printf("[engine-vpn] VPN tunnel on port %s (TLS 1.3 + Ed25519)", t.Config.Port)
	log.Printf("[engine-vpn] Server fingerprint: %s", fingerprint)
	log.Printf("[engine-vpn] Pairing code: %s (expires in 5 minutes)", code)
	log.Printf("[engine-vpn] Connect from device: https://%s:%s/vpn/pair", localIP, t.Config.Port)

	go func() {
		for {
			time.Sleep(time.Minute)
			t.Pairing.Cleanup()
		}
	}()

	return server.ListenAndServeTLS("", "")
}

func getLocalIP() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return "localhost"
	}
	for _, addr := range addrs {
		if ipNet, ok := addr.(*net.IPNet); ok && !ipNet.IP.IsLoopback() && ipNet.IP.To4() != nil {
			return ipNet.IP.String()
		}
	}
	return "localhost"
}
