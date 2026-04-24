package remote

import (
	"testing"
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
