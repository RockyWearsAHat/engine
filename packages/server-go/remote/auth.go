package remote

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// TokenPayload is the signed data embedded in each auth token.
type TokenPayload struct {
	DeviceID  string `json:"d"`
	IssuedAt  int64  `json:"i"`
	ExpiresAt int64  `json:"e"`
}

// AuthManager handles token creation, validation, and device revocation.
type AuthManager struct {
	mu         sync.RWMutex
	secret     []byte
	revokedIDs map[string]bool
}

// NewAuthManager loads an existing HMAC secret or generates a new one.
func NewAuthManager(storagePath string) (*AuthManager, error) {
	secretPath := filepath.Join(storagePath, "secret.key")

	secret, err := os.ReadFile(secretPath)
	if err != nil || len(secret) < 32 {
		secret = make([]byte, 32)
		if _, err := rand.Read(secret); err != nil {
			return nil, fmt.Errorf("generate secret: %w", err)
		}
		if err := os.WriteFile(secretPath, secret, 0600); err != nil {
			return nil, fmt.Errorf("write secret: %w", err)
		}
	}

	return &AuthManager{secret: secret, revokedIDs: make(map[string]bool)}, nil
}

// IssueToken creates an HMAC-signed token for a paired device.
func (am *AuthManager) IssueToken(deviceID string, ttl time.Duration) (string, error) {
	now := time.Now()
	payload := TokenPayload{
		DeviceID:  deviceID,
		IssuedAt:  now.Unix(),
		ExpiresAt: now.Add(ttl).Unix(),
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal payload: %w", err)
	}

	mac := hmac.New(sha256.New, am.secret)
	mac.Write(payloadBytes)
	sig := mac.Sum(nil)

	return base64.RawURLEncoding.EncodeToString(payloadBytes) + "." +
		base64.RawURLEncoding.EncodeToString(sig), nil
}

// ValidateToken verifies an HMAC token and returns the payload.
func (am *AuthManager) ValidateToken(token string) (*TokenPayload, error) {
	dotIdx := -1
	for i := 0; i < len(token); i++ {
		if token[i] == '.' {
			dotIdx = i
			break
		}
	}
	if dotIdx < 0 {
		return nil, fmt.Errorf("invalid token format")
	}

	payloadBytes, err := base64.RawURLEncoding.DecodeString(token[:dotIdx])
	if err != nil {
		return nil, fmt.Errorf("decode payload: %w", err)
	}

	sigBytes, err := base64.RawURLEncoding.DecodeString(token[dotIdx+1:])
	if err != nil {
		return nil, fmt.Errorf("decode signature: %w", err)
	}

	mac := hmac.New(sha256.New, am.secret)
	mac.Write(payloadBytes)
	if !hmac.Equal(sigBytes, mac.Sum(nil)) {
		return nil, fmt.Errorf("invalid signature")
	}

	var payload TokenPayload
	if err := json.Unmarshal(payloadBytes, &payload); err != nil {
		return nil, fmt.Errorf("unmarshal payload: %w", err)
	}

	if time.Now().Unix() > payload.ExpiresAt {
		return nil, fmt.Errorf("token expired")
	}

	am.mu.RLock()
	revoked := am.revokedIDs[payload.DeviceID]
	am.mu.RUnlock()
	if revoked {
		return nil, fmt.Errorf("device revoked")
	}

	return &payload, nil
}

// RevokeDevice marks a device ID as revoked so all its tokens are rejected.
func (am *AuthManager) RevokeDevice(deviceID string) {
	am.mu.Lock()
	defer am.mu.Unlock()
	am.revokedIDs[deviceID] = true
}

// RotateSecret generates a new HMAC key, invalidating all existing tokens.
func (am *AuthManager) RotateSecret(storagePath string) error {
	am.mu.Lock()
	defer am.mu.Unlock()

	newSecret := make([]byte, 32)
	if _, err := rand.Read(newSecret); err != nil {
		return fmt.Errorf("generate new secret: %w", err)
	}

	secretPath := filepath.Join(storagePath, "secret.key")
	if err := os.WriteFile(secretPath, newSecret, 0600); err != nil {
		return fmt.Errorf("write new secret: %w", err)
	}

	am.secret = newSecret
	return nil
}

// ExtractToken reads the auth token from a request query parameter or Authorization header.
func ExtractToken(r *http.Request) string {
	if token := r.URL.Query().Get("token"); token != "" {
		return token
	}
	auth := r.Header.Get("Authorization")
	if len(auth) > 7 && auth[:7] == "Bearer " {
		return auth[7:]
	}
	return ""
}

// AuthMiddleware rejects requests without a valid token.
func (am *AuthManager) AuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := ExtractToken(r)
		if token == "" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if _, err := am.ValidateToken(token); err != nil {
			http.Error(w, "unauthorized: "+err.Error(), http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}
