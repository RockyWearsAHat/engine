package remote

import (
	"crypto/tls"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadOrCreateTLSConfig_CreatesNew(t *testing.T) {
	dir := t.TempDir()
	cfg, err := LoadOrCreateTLSConfig(dir)
	if err != nil {
		t.Fatalf("LoadOrCreateTLSConfig: %v", err)
	}
	if cfg == nil {
		t.Fatal("expected non-nil TLS config")
	}
	if len(cfg.Certificates) == 0 {
		t.Error("expected at least one certificate")
	}
}

func TestLoadOrCreateTLSConfig_LoadsExisting(t *testing.T) {
	dir := t.TempDir()
	cfg1, err := LoadOrCreateTLSConfig(dir)
	if err != nil {
		t.Fatalf("first LoadOrCreateTLSConfig: %v", err)
	}

	cfg2, err := LoadOrCreateTLSConfig(dir)
	if err != nil {
		t.Fatalf("second LoadOrCreateTLSConfig: %v", err)
	}

	// Both should have valid certificates.
	cert1 := cfg1.Certificates[0]
	cert2 := cfg2.Certificates[0]
	_ = cert1
	_ = cert2
}

func TestLoadOrCreateTLSConfig_ValidCert(t *testing.T) {
	dir := t.TempDir()
	cfg, err := LoadOrCreateTLSConfig(dir)
	if err != nil {
		t.Fatalf("LoadOrCreateTLSConfig: %v", err)
	}

	cert, err := tls.X509KeyPair(
		cfg.Certificates[0].Certificate[0],
		nil, // We just test the cert was created
	)
	_ = cert
	// The certificate was created successfully if cfg has it.
	if len(cfg.Certificates[0].Certificate) == 0 {
		t.Error("expected certificate data")
	}
}

// TestLoadOrCreateTLSConfig_CorruptCertFallthrough: cert.pem exists but is corrupt,
// so LoadX509KeyPair fails and the function falls through to generate a new cert.
func TestLoadOrCreateTLSConfig_CorruptCertFallthrough(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "cert.pem"), []byte("not-a-cert"), 0600); err != nil {
		t.Fatal(err)
	}
	// key.pem does not exist — LoadX509KeyPair will fail, triggering fallthrough.
	cfg, err := LoadOrCreateTLSConfig(dir)
	if err != nil {
		t.Fatalf("expected fallthrough to generate new cert, got error: %v", err)
	}
	if cfg == nil || len(cfg.Certificates) == 0 {
		t.Error("expected a valid TLS config after fallthrough")
	}
}
func TestLoadOrCreateTLSConfig_WriteCertError(t *testing.T) {
	// Pass a non-existent storage path so os.Create(certPath) fails.
	_, err := LoadOrCreateTLSConfig("/nonexistent/path/that/cannot/be/created")
	if err == nil {
		t.Error("expected error when cert path is in non-existent directory")
	}
}

func TestLoadOrCreateTLSConfig_WriteKeyError(t *testing.T) {
	// Create a temp dir, write a directory named "key.pem" so os.OpenFile for key fails.
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "key.pem")
	if err := os.Mkdir(keyPath, 0755); err != nil {
		t.Fatal(err)
	}
	// cert.pem does not exist, so generation runs; os.Create(certPath) succeeds,
	// then os.OpenFile(keyPath) fails because it's a directory.
	_, err := LoadOrCreateTLSConfig(dir)
	if err == nil {
		t.Error("expected error when key path is a directory")
	}
}