package remote

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNewAuthManager_CreatesSecret(t *testing.T) {
	dir := t.TempDir()
	am, err := NewAuthManager(dir)
	if err != nil {
		t.Fatalf("NewAuthManager: %v", err)
	}
	if am == nil {
		t.Fatal("expected non-nil AuthManager")
	}
}

func TestNewAuthManager_LoadsExisting(t *testing.T) {
	dir := t.TempDir()
	am1, err := NewAuthManager(dir)
	if err != nil {
		t.Fatalf("first NewAuthManager: %v", err)
	}

	am2, err := NewAuthManager(dir)
	if err != nil {
		t.Fatalf("second NewAuthManager: %v", err)
	}
	if am1 == nil || am2 == nil {
		t.Fatal("expected non-nil managers")
	}
}

func TestIssueToken_And_ValidateToken(t *testing.T) {
	dir := t.TempDir()
	am, err := NewAuthManager(dir)
	if err != nil {
		t.Fatalf("NewAuthManager: %v", err)
	}

	token, err := am.IssueToken("device-1", time.Hour)
	if err != nil {
		t.Fatalf("IssueToken: %v", err)
	}
	if token == "" {
		t.Fatal("token should not be empty")
	}

	payload, err := am.ValidateToken(token)
	if err != nil {
		t.Fatalf("ValidateToken: %v", err)
	}
	if payload.DeviceID != "device-1" {
		t.Errorf("DeviceID = %q, want device-1", payload.DeviceID)
	}
}

func TestValidateToken_Expired(t *testing.T) {
	dir := t.TempDir()
	am, err := NewAuthManager(dir)
	if err != nil {
		t.Fatalf("NewAuthManager: %v", err)
	}

	token, err := am.IssueToken("device-exp", -time.Minute)
	if err != nil {
		t.Fatalf("IssueToken: %v", err)
	}

	_, err = am.ValidateToken(token)
	if err == nil {
		t.Fatal("expected error for expired token")
	}
}

func TestValidateToken_BadSignature(t *testing.T) {
	dir := t.TempDir()
	am, err := NewAuthManager(dir)
	if err != nil {
		t.Fatalf("NewAuthManager: %v", err)
	}

	_, err = am.ValidateToken("tampered.token.signature")
	if err == nil {
		t.Fatal("expected error for bad signature")
	}
}

func TestRevokeDevice(t *testing.T) {
	dir := t.TempDir()
	am, err := NewAuthManager(dir)
	if err != nil {
		t.Fatalf("NewAuthManager: %v", err)
	}

	token, err := am.IssueToken("device-to-revoke", time.Hour)
	if err != nil {
		t.Fatalf("IssueToken: %v", err)
	}

	am.RevokeDevice("device-to-revoke")

	_, err = am.ValidateToken(token)
	if err == nil {
		t.Fatal("expected error for revoked token")
	}
}

func TestRotateSecret(t *testing.T) {
	dir := t.TempDir()
	am, err := NewAuthManager(dir)
	if err != nil {
		t.Fatalf("NewAuthManager: %v", err)
	}

	token, err := am.IssueToken("device-rot", time.Hour)
	if err != nil {
		t.Fatalf("IssueToken: %v", err)
	}

	if err := am.RotateSecret(dir); err != nil {
		t.Fatalf("RotateSecret: %v", err)
	}

	// Old token should fail.
	_, err = am.ValidateToken(token)
	if err == nil {
		t.Fatal("expected old token to fail after rotation")
	}
}

func TestExtractToken_BearerHeader(t *testing.T) {
	r := httptest.NewRequest("GET", "/test", nil)
	r.Header.Set("Authorization", "Bearer mytoken123")

	token := ExtractToken(r)
	if token != "mytoken123" {
		t.Errorf("token = %q, want mytoken123", token)
	}
}

func TestExtractToken_QueryParam(t *testing.T) {
	r := httptest.NewRequest("GET", "/test?token=querytoken456", nil)

	token := ExtractToken(r)
	if token != "querytoken456" {
		t.Errorf("token = %q, want querytoken456", token)
	}
}

func TestExtractToken_Missing(t *testing.T) {
	r := httptest.NewRequest("GET", "/test", nil)

	token := ExtractToken(r)
	if token != "" {
		t.Errorf("expected empty token, got %q", token)
	}
}

func TestAuthMiddleware_ValidToken(t *testing.T) {
	dir := t.TempDir()
	am, err := NewAuthManager(dir)
	if err != nil {
		t.Fatalf("NewAuthManager: %v", err)
	}

	token, err := am.IssueToken("mid-device", time.Hour)
	if err != nil {
		t.Fatalf("IssueToken: %v", err)
	}

	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	mw := am.AuthMiddleware(next)
	req := httptest.NewRequest("GET", "/protected", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	mw.ServeHTTP(rr, req)

	if !called {
		t.Error("next handler was not called")
	}
	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rr.Code)
	}
}

func TestAuthMiddleware_InvalidToken(t *testing.T) {
	dir := t.TempDir()
	am, err := NewAuthManager(dir)
	if err != nil {
		t.Fatalf("NewAuthManager: %v", err)
	}

	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	})

	mw := am.AuthMiddleware(next)
	req := httptest.NewRequest("GET", "/protected", nil)
	req.Header.Set("Authorization", "Bearer bad.token")
	rr := httptest.NewRecorder()
	mw.ServeHTTP(rr, req)

	if called {
		t.Error("next handler should not have been called")
	}
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rr.Code)
	}
}

func TestAuthMiddleware_MissingToken(t *testing.T) {
	dir := t.TempDir()
	am, _ := NewAuthManager(dir)

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})
	mw := am.AuthMiddleware(next)
	req := httptest.NewRequest("GET", "/protected", nil)
	rr := httptest.NewRecorder()
	mw.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rr.Code)
	}
}

func TestValidateToken_EmptyString(t *testing.T) {
	dir := t.TempDir()
	am, _ := NewAuthManager(dir)
	_, err := am.ValidateToken("")
	if err == nil {
		t.Fatal("expected error for empty token")
	}
}

func TestValidateToken_InvalidFormat(t *testing.T) {
	dir := t.TempDir()
	am, _ := NewAuthManager(dir)
	_, err := am.ValidateToken("not-a-valid-token")
	if err == nil {
		t.Fatal("expected error for invalid token format")
	}
}

func TestExtractToken_EmptyBearerHeader(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Authorization", "Bearer ")
	token := ExtractToken(r)
	_ = token
}

func TestRotateSecret_WriteError(t *testing.T) {
	dir := t.TempDir()
	am, err := NewAuthManager(dir)
	if err != nil {
		t.Fatal(err)
	}
	// Make the dir read-only so WriteFile fails.
	if err := os.Chmod(dir, 0444); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(dir, 0755) //nolint:errcheck
	if err := am.RotateSecret(dir); err == nil {
		t.Fatal("expected error when dir is read-only")
	}
}

func TestNewAuthManager_WriteError(t *testing.T) {
	dir := t.TempDir()
	// Put a directory named secret.key to force WriteFile error.
	if err := os.Mkdir(filepath.Join(dir, "secret.key"), 0755); err != nil {
		t.Fatal(err)
	}
	_, err := NewAuthManager(dir)
	if err == nil {
		t.Fatal("expected error when secret.key is a directory")
	}
}

func TestValidateToken_BadPayloadBase64(t *testing.T) {
	dir := t.TempDir()
	am, err := NewAuthManager(dir)
	if err != nil {
		t.Fatalf("NewAuthManager: %v", err)
	}
	_, err = am.ValidateToken("!!!invalid-base64!!.anothersig")
	if err == nil {
		t.Error("expected error for bad payload base64")
	}
}

func TestValidateToken_BadSigBase64(t *testing.T) {
	dir := t.TempDir()
	am, err := NewAuthManager(dir)
	if err != nil {
		t.Fatalf("NewAuthManager: %v", err)
	}
	// Issue a valid token, then corrupt only the signature part.
	token, err := am.IssueToken("dev1", time.Hour)
	if err != nil {
		t.Fatalf("IssueToken: %v", err)
	}
	dotIdx := len(token) - len("SIG")
	for i := 0; i < len(token); i++ {
		if token[i] == '.' {
			dotIdx = i
			break
		}
	}
	corruptedToken := token[:dotIdx+1] + "!!!bad-base64"
	_, err = am.ValidateToken(corruptedToken)
	if err == nil {
		t.Error("expected error for bad signature base64")
	}
}

func TestValidateToken_BadPayloadJSON(t *testing.T) {
	dir := t.TempDir()
	am, err := NewAuthManager(dir)
	if err != nil {
		t.Fatalf("NewAuthManager: %v", err)
	}
	// Craft a token with valid base64 payload that doesn't parse as JSON.
	badPayload := base64.RawURLEncoding.EncodeToString([]byte("not json"))
	mac2 := hmac.New(sha256.New, am.secret)
	mac2.Write([]byte("not json"))
	sig := base64.RawURLEncoding.EncodeToString(mac2.Sum(nil))
	_, err = am.ValidateToken(badPayload + "." + sig)
	if err == nil {
		t.Error("expected error for bad JSON payload")
	}
}
