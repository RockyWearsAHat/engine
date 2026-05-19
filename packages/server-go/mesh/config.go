// Package mesh implements the LAN compute mesh that lets a Mac and a PC pool
// inference + test-execution compute. Peers are listed in ~/.engine/mesh.json
// and authenticate with a shared HMAC secret.
//
// This is a v1: explicit peer registry, no mDNS discovery. Service discovery
// can be added later behind the same Peer/PeerSet types.
package mesh

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Peer is one mesh participant.
type Peer struct {
	Name    string   `json:"name"`
	Address string   `json:"address"`              // host:port for the mesh HTTP listener
	Secret  string   `json:"secret"`               // HMAC secret used to sign requests to/from this peer
	Roles   []string `json:"roles,omitempty"`       // optional: "inference", "tests", "build"
	// OllamaURL is the peer's local Ollama endpoint (e.g. http://127.0.0.1:11434).
	// When set on the listening peer, /mesh/inference proxies upstream Ollama
	// traffic to it — letting a Mac route inference to a PC with a bigger GPU.
	OllamaURL string `json:"ollamaURL,omitempty"`
}

// Config is the on-disk representation of mesh state.
type Config struct {
	// SelfName identifies this machine in mesh signed payloads.
	SelfName string `json:"selfName"`
	// ListenAddr is the address:port the mesh server binds to.
	// Defaults to ":24445" when empty.
	ListenAddr string `json:"listenAddr,omitempty"`
	// SelfOllamaURL is this machine's local Ollama. Advertised in /mesh/health
	// and proxied through /mesh/inference when set.
	SelfOllamaURL string `json:"selfOllamaURL,omitempty"`
	// Peers lists the other machines this engine knows about.
	Peers []Peer `json:"peers,omitempty"`
}

var userHomeDirFn = os.UserHomeDir

// DefaultConfigPath returns the canonical mesh.json location:
// ~/.engine/mesh.json (or overridden by ENGINE_MESH_CONFIG).
func DefaultConfigPath() string {
	if v := strings.TrimSpace(os.Getenv("ENGINE_MESH_CONFIG")); v != "" {
		return v
	}
	home, _ := userHomeDirFn()
	if strings.TrimSpace(home) == "" {
		return ".engine/mesh.json"
	}
	return filepath.Join(home, ".engine", "mesh.json")
}

// LoadConfig reads mesh.json from disk. Missing file returns an empty config
// with a sensible default ListenAddr so callers can still operate without peers.
func LoadConfig(path string) (*Config, error) {
	if strings.TrimSpace(path) == "" {
		path = DefaultConfigPath()
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &Config{ListenAddr: ":24445"}, nil
		}
		return nil, fmt.Errorf("mesh: read %s: %w", path, err)
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("mesh: parse %s: %w", path, err)
	}
	if strings.TrimSpace(cfg.ListenAddr) == "" {
		cfg.ListenAddr = ":24445"
	}
	return &cfg, nil
}

// SaveConfig writes the config back to disk. Used by the pairing UI when
// adding a peer.
func SaveConfig(path string, cfg *Config) error {
	if strings.TrimSpace(path) == "" {
		path = DefaultConfigPath()
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("mesh: mkdir %s: %w", filepath.Dir(path), err)
	}
	data, _ := json.MarshalIndent(cfg, "", "  ")
	return os.WriteFile(path, data, 0o600)
}

// FindPeer returns the peer matching name (case-insensitive), or nil.
func (c *Config) FindPeer(name string) *Peer {
	if c == nil {
		return nil
	}
	target := strings.ToLower(strings.TrimSpace(name))
	for i := range c.Peers {
		if strings.ToLower(strings.TrimSpace(c.Peers[i].Name)) == target {
			return &c.Peers[i]
		}
	}
	return nil
}

// PeerWithRole returns the first configured peer carrying the requested role,
// or nil. Roles let the dispatcher say "give me an inference box" without
// hardcoding peer names.
func (c *Config) PeerWithRole(role string) *Peer {
	if c == nil {
		return nil
	}
	target := strings.ToLower(strings.TrimSpace(role))
	if target == "" {
		return nil
	}
	for i := range c.Peers {
		for _, r := range c.Peers[i].Roles {
			if strings.EqualFold(strings.TrimSpace(r), target) {
				return &c.Peers[i]
			}
		}
	}
	return nil
}
