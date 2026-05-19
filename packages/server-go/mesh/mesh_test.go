package mesh

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadConfig_MissingFileReturnsDefaults(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "no-such.json")
	cfg, err := LoadConfig(tmp)
	if err != nil {
		t.Fatalf("expected nil error for missing file, got %v", err)
	}
	if cfg.ListenAddr != ":24445" {
		t.Errorf("expected default listen addr, got %q", cfg.ListenAddr)
	}
}

func TestLoadConfig_RoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mesh.json")
	src := &Config{
		SelfName:   "mac",
		ListenAddr: ":9991",
		Peers: []Peer{
			{Name: "pc", Address: "192.168.0.10:9990", Secret: "shh", Roles: []string{"tests"}},
		},
	}
	if err := SaveConfig(path, src); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got.SelfName != "mac" || got.ListenAddr != ":9991" || len(got.Peers) != 1 {
		t.Errorf("roundtrip mismatch: %+v", got)
	}
	if got.FindPeer("PC") == nil {
		t.Error("FindPeer should be case-insensitive")
	}
	if got.PeerWithRole("tests") == nil {
		t.Error("PeerWithRole should match")
	}
}

func TestSignVerify_RoundTrip(t *testing.T) {
	body := []byte(`{"command":"go","args":["test","./..."]}`)
	timestamp, sig := signRequest("topsecret", "mac", body)
	if err := verifyRequest("topsecret", body, timestamp, sig); err != nil {
		t.Fatalf("verify failed: %v", err)
	}
}

func TestVerifyRequest_RejectsTamper(t *testing.T) {
	body := []byte(`{"command":"go"}`)
	timestamp, sig := signRequest("topsecret", "mac", body)
	tampered := []byte(`{"command":"rm"}`)
	if err := verifyRequest("topsecret", tampered, timestamp, sig); err == nil {
		t.Fatal("expected tamper rejection")
	}
}

func TestVerifyRequest_RejectsWrongSecret(t *testing.T) {
	body := []byte(`{}`)
	timestamp, sig := signRequest("topsecret", "mac", body)
	if err := verifyRequest("nope", body, timestamp, sig); err == nil {
		t.Fatal("expected wrong-secret rejection")
	}
}

func TestServer_HealthEndpointRequiresOrigin(t *testing.T) {
	cfg := &Config{
		SelfName: "host",
		Peers:    []Peer{{Name: "mac", Secret: "k"}},
	}
	srv := NewServer(cfg)
	mux := http.NewServeMux()
	srv.Register(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/mesh/health", "application/json", strings.NewReader(""))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401 without signature, got %d", resp.StatusCode)
	}
}

func TestServer_HealthEndpointAcceptsSignedRequest(t *testing.T) {
	cfg := &Config{
		SelfName: "host",
		Peers:    []Peer{{Name: "mac", Secret: "k"}},
	}
	srv := NewServer(cfg)
	mux := http.NewServeMux()
	srv.Register(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	body := []byte{}
	timestamp, sig := signRequest("k", "mac", body)
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/mesh/health", strings.NewReader(""))
	req.Header.Set(timestampHeader, timestamp)
	req.Header.Set(signatureHeader, sig)
	req.Header.Set(originHeader, "mac")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var hr HealthResponse
	if err := json.NewDecoder(resp.Body).Decode(&hr); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if hr.Name != "host" {
		t.Errorf("name = %q want host", hr.Name)
	}
	if hr.CPUs <= 0 {
		t.Errorf("CPUs should be > 0, got %d", hr.CPUs)
	}
}

func TestServer_ExecRunsCommand(t *testing.T) {
	cfg := &Config{
		SelfName: "host",
		Peers:    []Peer{{Name: "mac", Secret: "k"}},
	}
	srv := NewServer(cfg)
	mux := http.NewServeMux()
	srv.Register(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	peer := &Peer{Name: "host", Address: strings.TrimPrefix(ts.URL, "http://"), Secret: "k"}
	client := NewClient("mac")
	resp, err := client.Exec(context.Background(), peer, ExecRequest{Command: "echo", Args: []string{"hello-mesh"}})
	if err != nil {
		t.Fatalf("exec: %v", err)
	}
	if !strings.Contains(resp.Stdout, "hello-mesh") {
		t.Errorf("stdout missing greeting: %q", resp.Stdout)
	}
	if resp.ExitCode != 0 {
		t.Errorf("exitCode = %d, want 0", resp.ExitCode)
	}
}

func TestServer_ExecRejectsUnknownOrigin(t *testing.T) {
	cfg := &Config{
		SelfName: "host",
		Peers:    []Peer{{Name: "mac", Secret: "k"}},
	}
	srv := NewServer(cfg)
	mux := http.NewServeMux()
	srv.Register(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	body := []byte(`{"command":"echo"}`)
	timestamp, sig := signRequest("k", "rogue", body)
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/mesh/exec", strings.NewReader(string(body)))
	req.Header.Set(timestampHeader, timestamp)
	req.Header.Set(signatureHeader, sig)
	req.Header.Set(originHeader, "rogue")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401 for unknown origin, got %d", resp.StatusCode)
	}
}

func TestDefaultConfigPath_EnvOverride(t *testing.T) {
	t.Setenv("ENGINE_MESH_CONFIG", "/tmp/special.json")
	if got := DefaultConfigPath(); got != "/tmp/special.json" {
		t.Errorf("env override not honoured: %q", got)
	}
}

func TestLimitedBuffer_TruncatesPastMax(t *testing.T) {
	b := &limitedBuffer{Max: 10}
	_, _ = b.Write([]byte("0123456789ABCDEF"))
	if got := b.String(); len(got) != 10 || got != "0123456789" {
		t.Errorf("unexpected buffer: %q", got)
	}
}

func TestSaveConfig_DefaultPathRespectsHome(t *testing.T) {
	t.Setenv("ENGINE_MESH_CONFIG", "")
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	cfg := &Config{SelfName: "x"}
	if err := SaveConfig("", cfg); err != nil {
		t.Fatalf("save: %v", err)
	}
	expected := filepath.Join(tmp, ".engine", "mesh.json")
	if _, err := os.Stat(expected); err != nil {
		t.Fatalf("file missing at %s: %v", expected, err)
	}
}
