package remote

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"sync"
	"time"
)

// PairingSession represents a single-use pairing code with an expiry time.
type PairingSession struct {
	Code      string
	ExpiresAt time.Time
	Used      bool
}

// PairingManager handles generation and validation of one-time pairing codes.
type PairingManager struct {
	mu       sync.Mutex
	sessions map[string]*PairingSession
}

// NewPairingManager creates a fresh PairingManager with no active codes.
func NewPairingManager() *PairingManager {
	return &PairingManager{sessions: make(map[string]*PairingSession)}
}

// GenerateCode creates a 6-digit one-time pairing code valid for 5 minutes.
func (pm *PairingManager) GenerateCode() (string, error) {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	n, err := rand.Int(rand.Reader, big.NewInt(1000000))
	if err != nil {
		return "", fmt.Errorf("generate random: %w", err)
	}
	code := fmt.Sprintf("%06d", n.Int64())

	pm.sessions[code] = &PairingSession{
		Code:      code,
		ExpiresAt: time.Now().Add(5 * time.Minute),
	}

	return code, nil
}

// ValidateCode checks a pairing code. Returns true and consumes it on success.
func (pm *PairingManager) ValidateCode(code string) bool {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	session, ok := pm.sessions[code]
	if !ok {
		return false
	}

	if session.Used || time.Now().After(session.ExpiresAt) {
		delete(pm.sessions, code)
		return false
	}

	session.Used = true
	delete(pm.sessions, code)
	return true
}

// Cleanup removes all expired pairing codes.
func (pm *PairingManager) Cleanup() {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	now := time.Now()
	for code, session := range pm.sessions {
		if now.After(session.ExpiresAt) {
			delete(pm.sessions, code)
		}
	}
}
