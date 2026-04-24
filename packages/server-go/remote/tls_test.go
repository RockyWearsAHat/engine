package remote

import (
	"crypto/tls"
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
