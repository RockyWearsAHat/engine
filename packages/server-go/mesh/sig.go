package mesh

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

// signatureHeader carries the HMAC-SHA256 of (timestamp + body) signed with the
// peer's shared secret. Clock skew tolerance is bounded by signatureMaxSkew.
const (
	signatureHeader     = "X-Mesh-Signature"
	timestampHeader     = "X-Mesh-Timestamp"
	originHeader        = "X-Mesh-Origin"
	signatureMaxSkew    = 5 * time.Minute
	timestampLayout     = time.RFC3339
)

// signRequest returns the headers a sender should attach: signature + timestamp + origin.
func signRequest(secret, origin string, body []byte) (timestamp, signature string) {
	timestamp = time.Now().UTC().Format(timestampLayout)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(timestamp))
	mac.Write([]byte("\n"))
	mac.Write(body)
	signature = hex.EncodeToString(mac.Sum(nil))
	_ = origin // included as a separate header so the receiver knows which peer's secret to verify against
	return
}

// verifyRequest returns nil when the signature is valid and the timestamp is
// within signatureMaxSkew of now.
func verifyRequest(secret string, body []byte, timestamp, signature string) error {
	if strings.TrimSpace(secret) == "" {
		return fmt.Errorf("mesh: secret not configured")
	}
	if strings.TrimSpace(signature) == "" || strings.TrimSpace(timestamp) == "" {
		return fmt.Errorf("mesh: signature or timestamp missing")
	}
	parsed, err := time.Parse(timestampLayout, timestamp)
	if err != nil {
		return fmt.Errorf("mesh: bad timestamp: %w", err)
	}
	if delta := time.Since(parsed); delta > signatureMaxSkew || delta < -signatureMaxSkew {
		return fmt.Errorf("mesh: timestamp out of window (delta=%s)", delta)
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(timestamp))
	mac.Write([]byte("\n"))
	mac.Write(body)
	want := mac.Sum(nil)
	got, err := hex.DecodeString(signature)
	if err != nil {
		return fmt.Errorf("mesh: bad signature encoding: %w", err)
	}
	if !hmac.Equal(want, got) {
		return fmt.Errorf("mesh: signature mismatch")
	}
	return nil
}
