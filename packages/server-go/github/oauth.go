// Package github — OAuth device flow for GitHub authentication.
// Implements the GitHub Device Authorization Grant (RFC 8628).
// No browser is required: the user enters a code at github.com/login/device.
package github

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	deviceCodeURL   = "https://github.com/login/device/code"
	oauthTokenURL   = "https://github.com/login/oauth/access_token"
	// Minimum required scopes. Engine adds scopes only when a feature needs them.
	defaultScopes   = "repo read:user"
)

// DeviceCodeResponse is the initial response from the device code endpoint.
type DeviceCodeResponse struct {
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	VerificationURI string `json:"verification_uri"`
	ExpiresIn       int    `json:"expires_in"`
	Interval        int    `json:"interval"`
}

// TokenResponse is the response from the OAuth token endpoint.
type TokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	Scope       string `json:"scope"`
	Error       string `json:"error"`
	ErrorDesc   string `json:"error_description"`
}

// StartDeviceFlow initiates the GitHub device authorization flow.
// Returns the DeviceCodeResponse which contains:
//   - UserCode: the 8-character code the user must enter at VerificationURI
//   - VerificationURI: https://github.com/login/device
//   - DeviceCode: opaque code used when polling for the token
//
// The caller must display UserCode + VerificationURI to the user, then call PollForToken.
func StartDeviceFlow(clientID, scopes string) (*DeviceCodeResponse, error) {
	if scopes == "" {
		scopes = defaultScopes
	}

	resp, err := http.PostForm(deviceCodeURL, url.Values{
		"client_id": {clientID},
		"scope":     {scopes},
	})
	if err != nil {
		return nil, fmt.Errorf("device code request: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	// GitHub returns form-encoded data here, not JSON.
	vals, err := url.ParseQuery(string(body))
	if err != nil {
		return nil, fmt.Errorf("parse device code response: %w", err)
	}

	dc := &DeviceCodeResponse{
		DeviceCode:      vals.Get("device_code"),
		UserCode:        vals.Get("user_code"),
		VerificationURI: vals.Get("verification_uri"),
	}
	if dc.DeviceCode == "" {
		// Try JSON fallback (some GitHub Enterprise versions return JSON).
		if err := json.Unmarshal(body, dc); err != nil || dc.DeviceCode == "" {
			return nil, fmt.Errorf("empty device_code in response: %s", string(body))
		}
	}
	if expiresIn := vals.Get("expires_in"); expiresIn != "" {
		fmt.Sscanf(expiresIn, "%d", &dc.ExpiresIn)
	}
	if interval := vals.Get("interval"); interval != "" {
		fmt.Sscanf(interval, "%d", &dc.Interval)
	}
	if dc.ExpiresIn == 0 {
		dc.ExpiresIn = 900
	}
	if dc.Interval == 0 {
		dc.Interval = 5
	}
	return dc, nil
}

// PollForToken polls the GitHub token endpoint until the user completes the
// device flow or the device code expires. Blocks until done.
//
// Progress is reported via onStatus (may be nil). The callback receives one of:
//   - "waiting" — user has not yet authorized
//   - "slow_down" — GitHub asked us to poll less frequently
//   - "expired" — device code expired before user authorized
//   - "error: <msg>" — unexpected error
func PollForToken(clientID string, dcr *DeviceCodeResponse, onStatus func(string)) (*TokenResponse, error) {
	interval := time.Duration(dcr.Interval) * time.Second
	deadline := time.Now().Add(time.Duration(dcr.ExpiresIn) * time.Second)

	for time.Now().Before(deadline) {
		time.Sleep(interval)

		resp, err := http.PostForm(oauthTokenURL, url.Values{
			"client_id":   {clientID},
			"device_code": {dcr.DeviceCode},
			"grant_type":  {"urn:ietf:params:oauth:grant-type:device_code"},
		})
		if err != nil {
			if onStatus != nil {
				onStatus("error: " + err.Error())
			}
			continue
		}

		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		// Token endpoint always returns form-encoded.
		vals, _ := url.ParseQuery(string(body))
		tok := &TokenResponse{
			AccessToken: vals.Get("access_token"),
			TokenType:   vals.Get("token_type"),
			Scope:       vals.Get("scope"),
			Error:       vals.Get("error"),
			ErrorDesc:   vals.Get("error_description"),
		}

		switch tok.Error {
		case "":
			if tok.AccessToken != "" {
				return tok, nil
			}
		case "authorization_pending":
			if onStatus != nil {
				onStatus("waiting")
			}
		case "slow_down":
			interval += 5 * time.Second
			if onStatus != nil {
				onStatus("slow_down")
			}
		case "expired_token":
			if onStatus != nil {
				onStatus("expired")
			}
			return nil, fmt.Errorf("device code expired")
		case "access_denied":
			return nil, fmt.Errorf("user denied access")
		default:
			if onStatus != nil {
				onStatus("error: " + tok.Error + " — " + tok.ErrorDesc)
			}
		}
	}

	return nil, fmt.Errorf("device code polling timed out")
}

// GetAuthenticatedUser returns the login name for the given token.
// Used to verify a token is valid after the device flow completes.
func GetAuthenticatedUser(token string) (string, error) {
	req, _ := http.NewRequest("GET", apiBase+"/user", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GitHub API returned %d", resp.StatusCode)
	}

	var user struct {
		Login string `json:"login"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&user); err != nil {
		return "", err
	}
	return user.Login, nil
}

// RevokeToken revokes a GitHub OAuth token.
// Requires the client_secret (not applicable to device flow public clients;
// provided as best-effort for apps that have one).
func RevokeToken(clientID, clientSecret, token string) error {
	body := strings.NewReader(`{"access_token":"` + token + `"}`)
	req, _ := http.NewRequest("DELETE",
		fmt.Sprintf("%s/applications/%s/token", apiBase, clientID), body)
	req.SetBasicAuth(clientID, clientSecret)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		return fmt.Errorf("revoke returned status %d", resp.StatusCode)
	}
	return nil
}
