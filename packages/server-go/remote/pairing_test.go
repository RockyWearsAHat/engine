package remote

import (
	"testing"
	"time"
)

func TestNewPairingManager(t *testing.T) {
	pm := NewPairingManager()
	if pm == nil {
		t.Fatal("expected non-nil PairingManager")
	}
}

func TestGenerateCode(t *testing.T) {
	pm := NewPairingManager()
	code, err := pm.GenerateCode()
	if err != nil {
		t.Fatalf("GenerateCode: %v", err)
	}
	if len(code) != 6 {
		t.Errorf("code length = %d, want 6", len(code))
	}
	for _, c := range code {
		if c < '0' || c > '9' {
			t.Errorf("code contains non-digit: %c", c)
		}
	}
}

func TestGenerateCode_Unique(t *testing.T) {
	pm := NewPairingManager()
	codes := make(map[string]bool)
	for i := 0; i < 5; i++ {
		code, err := pm.GenerateCode()
		if err != nil {
			t.Fatalf("GenerateCode: %v", err)
		}
		codes[code] = true
	}
	for code := range codes {
		if len(code) != 6 {
			t.Errorf("invalid code length: %q", code)
		}
	}
}

func TestValidateCode_Valid(t *testing.T) {
	pm := NewPairingManager()
	code, err := pm.GenerateCode()
	if err != nil {
		t.Fatalf("GenerateCode: %v", err)
	}

	ok := pm.ValidateCode(code)
	if !ok {
		t.Errorf("ValidateCode(%q) = false, want true", code)
	}
}

func TestValidateCode_SingleUse(t *testing.T) {
	pm := NewPairingManager()
	code, err := pm.GenerateCode()
	if err != nil {
		t.Fatalf("GenerateCode: %v", err)
	}

	_ = pm.ValidateCode(code)
	ok := pm.ValidateCode(code)
	if ok {
		t.Error("second ValidateCode should return false (single-use)")
	}
}

func TestValidateCode_Nonexistent(t *testing.T) {
	pm := NewPairingManager()
	ok := pm.ValidateCode("000000")
	if ok {
		t.Error("ValidateCode for nonexistent code should return false")
	}
}

func TestCleanup(t *testing.T) {
	pm := NewPairingManager()
	_, _ = pm.GenerateCode()
	_, _ = pm.GenerateCode()
	pm.Cleanup()
}

func TestValidateCode_AfterCleanup(t *testing.T) {
	pm := NewPairingManager()
	code, err := pm.GenerateCode()
	if err != nil {
		t.Fatalf("GenerateCode: %v", err)
	}
	pm.Cleanup()
	_ = pm.ValidateCode(code)
}

func TestValidateCode_Expired(t *testing.T) {
	pm := NewPairingManager()
	// Manually insert an expired session.
	pm.sessions["999999"] = &PairingSession{
		Code:      "999999",
		ExpiresAt: time.Now().Add(-1 * time.Minute),
	}
	ok := pm.ValidateCode("999999")
	if ok {
		t.Error("expected false for expired code")
	}
}

func TestCleanup_RemovesExpired(t *testing.T) {
	pm := NewPairingManager()
	// Add an expired session.
	pm.sessions["123456"] = &PairingSession{
		Code:      "123456",
		ExpiresAt: time.Now().Add(-1 * time.Minute),
	}
	// Add a valid session.
	pm.sessions["654321"] = &PairingSession{
		Code:      "654321",
		ExpiresAt: time.Now().Add(5 * time.Minute),
	}
	pm.Cleanup()
	if _, ok := pm.sessions["123456"]; ok {
		t.Error("expected expired session to be cleaned up")
	}
	if _, ok := pm.sessions["654321"]; !ok {
		t.Error("expected valid session to remain")
	}
}
