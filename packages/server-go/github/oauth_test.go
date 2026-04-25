package github

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// ─── helpers ──────────────────────────────────────────────────────────────────

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func mockOAuthClient(statusCode int, body string) *http.Client {
	return &http.Client{
		Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: statusCode,
				Body:       io.NopCloser(strings.NewReader(body)),
				Header:     make(http.Header),
			}, nil
		}),
	}
}

// ─── StartDeviceFlow ──────────────────────────────────────────────────────────

func TestStartDeviceFlow_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-www-form-urlencoded")
		io.WriteString(w, "device_code=DEVCODE&user_code=USER-CODE&verification_uri=https://github.com/login/device&expires_in=900&interval=5") //nolint:errcheck
	}))
	defer srv.Close()

	origURL := deviceCodeURL
	deviceCodeURL = srv.URL
	defer func() { deviceCodeURL = origURL }()

	origClient := oauthHTTPClient
	oauthHTTPClient = srv.Client()
	defer func() { oauthHTTPClient = origClient }()

	dcr, err := StartDeviceFlow("client-id", "")
	if err != nil {
		t.Fatalf("StartDeviceFlow error: %v", err)
	}
	if dcr.DeviceCode != "DEVCODE" {
		t.Errorf("DeviceCode = %q, want DEVCODE", dcr.DeviceCode)
	}
	if dcr.UserCode != "USER-CODE" {
		t.Errorf("UserCode = %q, want USER-CODE", dcr.UserCode)
	}
	if dcr.ExpiresIn != 900 {
		t.Errorf("ExpiresIn = %d, want 900", dcr.ExpiresIn)
	}
	if dcr.Interval != 5 {
		t.Errorf("Interval = %d, want 5", dcr.Interval)
	}
}

func TestStartDeviceFlow_DefaultExpiresAndInterval(t *testing.T) {
	// Response with no expires_in / interval → defaults applied.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, "device_code=DC&user_code=UC&verification_uri=uri") //nolint:errcheck
	}))
	defer srv.Close()

	origURL := deviceCodeURL
	deviceCodeURL = srv.URL
	defer func() { deviceCodeURL = origURL }()

	origClient := oauthHTTPClient
	oauthHTTPClient = srv.Client()
	defer func() { oauthHTTPClient = origClient }()

	dcr, err := StartDeviceFlow("cid", "custom:scope")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dcr.ExpiresIn != 900 {
		t.Errorf("default ExpiresIn = %d, want 900", dcr.ExpiresIn)
	}
	if dcr.Interval != 5 {
		t.Errorf("default Interval = %d, want 5", dcr.Interval)
	}
}

func TestStartDeviceFlow_EmptyDeviceCode_JSONFallback(t *testing.T) {
	// Response body is JSON (GitHub Enterprise style).
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{ //nolint:errcheck
			"device_code":      "JDEV",
			"user_code":        "JUSER",
			"verification_uri": "https://example.com",
			"expires_in":       600,
			"interval":         10,
		})
	}))
	defer srv.Close()

	origURL := deviceCodeURL
	deviceCodeURL = srv.URL
	defer func() { deviceCodeURL = origURL }()

	origClient := oauthHTTPClient
	oauthHTTPClient = srv.Client()
	defer func() { oauthHTTPClient = origClient }()

	dcr, err := StartDeviceFlow("cid", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dcr.DeviceCode != "JDEV" {
		t.Errorf("JSON fallback DeviceCode = %q, want JDEV", dcr.DeviceCode)
	}
}

func TestStartDeviceFlow_EmptyDeviceCode_Error(t *testing.T) {
	// Response returns no device_code and invalid JSON.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, "error=bad_verification_uri") //nolint:errcheck
	}))
	defer srv.Close()

	origURL := deviceCodeURL
	deviceCodeURL = srv.URL
	defer func() { deviceCodeURL = origURL }()

	origClient := oauthHTTPClient
	oauthHTTPClient = srv.Client()
	defer func() { oauthHTTPClient = origClient }()

	_, err := StartDeviceFlow("cid", "")
	if err == nil {
		t.Fatal("expected error for empty device_code")
	}
}

// ─── PollForToken ─────────────────────────────────────────────────────────────

func TestPollForToken_ImmediateSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		params := url.Values{}
		params.Set("access_token", "gho_test")
		params.Set("token_type", "bearer")
		params.Set("scope", "repo")
		io.WriteString(w, params.Encode()) //nolint:errcheck
	}))
	defer srv.Close()

	origURL := oauthTokenURL
	oauthTokenURL = srv.URL
	defer func() { oauthTokenURL = origURL }()

	origClient := oauthHTTPClient
	oauthHTTPClient = srv.Client()
	defer func() { oauthHTTPClient = origClient }()

	dcr := &DeviceCodeResponse{
		DeviceCode: "DC",
		ExpiresIn:  30,
		Interval:   0, // 0 → sleep(0)
	}
	tok, err := PollForToken("cid", dcr, nil)
	if err != nil {
		t.Fatalf("PollForToken error: %v", err)
	}
	if tok.AccessToken != "gho_test" {
		t.Errorf("AccessToken = %q, want gho_test", tok.AccessToken)
	}
}

func TestPollForToken_AuthorizationPending_ThenSuccess(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		if calls == 1 {
			io.WriteString(w, "error=authorization_pending") //nolint:errcheck
			return
		}
		io.WriteString(w, "access_token=gho_final&token_type=bearer&scope=repo") //nolint:errcheck
	}))
	defer srv.Close()

	origURL := oauthTokenURL
	oauthTokenURL = srv.URL
	defer func() { oauthTokenURL = origURL }()

	origClient := oauthHTTPClient
	oauthHTTPClient = srv.Client()
	defer func() { oauthHTTPClient = origClient }()

	var statuses []string
	dcr := &DeviceCodeResponse{DeviceCode: "DC", ExpiresIn: 30, Interval: 0}
	tok, err := PollForToken("cid", dcr, func(s string) { statuses = append(statuses, s) })
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tok.AccessToken != "gho_final" {
		t.Errorf("AccessToken = %q, want gho_final", tok.AccessToken)
	}
	if len(statuses) == 0 || statuses[0] != "waiting" {
		t.Errorf("expected first status = waiting, got %v", statuses)
	}
}

func TestPollForToken_SlowDown(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		if calls == 1 {
			io.WriteString(w, "error=slow_down") //nolint:errcheck
			return
		}
		io.WriteString(w, "access_token=gho_slow&token_type=bearer") //nolint:errcheck
	}))
	defer srv.Close()

	origURL := oauthTokenURL
	oauthTokenURL = srv.URL
	defer func() { oauthTokenURL = origURL }()

	origClient := oauthHTTPClient
	oauthHTTPClient = srv.Client()
	defer func() { oauthHTTPClient = origClient }()

	var statuses []string
	dcr := &DeviceCodeResponse{DeviceCode: "DC", ExpiresIn: 30, Interval: 0}
	tok, err := PollForToken("cid", dcr, func(s string) { statuses = append(statuses, s) })
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tok.AccessToken != "gho_slow" {
		t.Errorf("AccessToken = %q, want gho_slow", tok.AccessToken)
	}
	if len(statuses) == 0 || statuses[0] != "slow_down" {
		t.Errorf("expected first status = slow_down, got %v", statuses)
	}
}

func TestPollForToken_ExpiredToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, "error=expired_token") //nolint:errcheck
	}))
	defer srv.Close()

	origURL := oauthTokenURL
	oauthTokenURL = srv.URL
	defer func() { oauthTokenURL = origURL }()

	origClient := oauthHTTPClient
	oauthHTTPClient = srv.Client()
	defer func() { oauthHTTPClient = origClient }()

	var statuses []string
	dcr := &DeviceCodeResponse{DeviceCode: "DC", ExpiresIn: 30, Interval: 0}
	_, err := PollForToken("cid", dcr, func(s string) { statuses = append(statuses, s) })
	if err == nil {
		t.Fatal("expected error for expired_token")
	}
	if len(statuses) == 0 || statuses[0] != "expired" {
		t.Errorf("expected status expired, got %v", statuses)
	}
}

func TestPollForToken_AccessDenied(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, "error=access_denied") //nolint:errcheck
	}))
	defer srv.Close()

	origURL := oauthTokenURL
	oauthTokenURL = srv.URL
	defer func() { oauthTokenURL = origURL }()

	origClient := oauthHTTPClient
	oauthHTTPClient = srv.Client()
	defer func() { oauthHTTPClient = origClient }()

	dcr := &DeviceCodeResponse{DeviceCode: "DC", ExpiresIn: 30, Interval: 0}
	_, err := PollForToken("cid", dcr, nil)
	if err == nil {
		t.Fatal("expected error for access_denied")
	}
}

func TestPollForToken_UnknownError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, "error=unknown_error&error_description=something+bad") //nolint:errcheck
	}))
	defer srv.Close()

	origURL := oauthTokenURL
	oauthTokenURL = srv.URL
	defer func() { oauthTokenURL = origURL }()

	origClient := oauthHTTPClient
	oauthHTTPClient = srv.Client()
	defer func() { oauthHTTPClient = origClient }()

	var statuses []string
	dcr := &DeviceCodeResponse{DeviceCode: "DC", ExpiresIn: 5, Interval: 0}
	// This will loop until deadline but for testing we use a short ExpiresIn.
	// The unknown_error path notifies status then continues looping.
	// ExpiresIn=5s → will hit timeout after expiry. Use a very short expires.
	dcr.ExpiresIn = 1
	start := time.Now()
	_, err := PollForToken("cid", dcr, func(s string) { statuses = append(statuses, s) })
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if time.Since(start) > 10*time.Second {
		t.Error("PollForToken took too long")
	}
	if len(statuses) == 0 {
		t.Error("expected at least one status callback")
	}
}

func TestPollForToken_Timeout(t *testing.T) {
	// ExpiresIn=1 → loop exits immediately via deadline check.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, "error=authorization_pending") //nolint:errcheck
	}))
	defer srv.Close()

	origURL := oauthTokenURL
	oauthTokenURL = srv.URL
	defer func() { oauthTokenURL = origURL }()

	origClient := oauthHTTPClient
	oauthHTTPClient = srv.Client()
	defer func() { oauthHTTPClient = origClient }()

	dcr := &DeviceCodeResponse{DeviceCode: "DC", ExpiresIn: 1, Interval: 0}
	_, err := PollForToken("cid", dcr, nil)
	if err == nil {
		t.Fatal("expected timeout error")
	}
}

// ─── GetAuthenticatedUser ─────────────────────────────────────────────────────

func TestGetAuthenticatedUser_Success(t *testing.T) {
	origClient := oauthHTTPClient
	oauthHTTPClient = mockOAuthClient(200, `{"login":"cave-user","name":"Cave Person"}`)
	defer func() { oauthHTTPClient = origClient }()

	login, err := GetAuthenticatedUser("gho_test_token")
	if err != nil {
		t.Fatalf("GetAuthenticatedUser error: %v", err)
	}
	if login != "cave-user" {
		t.Errorf("login = %q, want cave-user", login)
	}
}

func TestGetAuthenticatedUser_APIError(t *testing.T) {
	origClient := oauthHTTPClient
	oauthHTTPClient = mockOAuthClient(401, `{"message":"Bad credentials"}`)
	defer func() { oauthHTTPClient = origClient }()

	_, err := GetAuthenticatedUser("bad_token")
	if err == nil {
		t.Fatal("expected error for 401")
	}
}

func TestGetAuthenticatedUser_InvalidJSON(t *testing.T) {
	origClient := oauthHTTPClient
	oauthHTTPClient = mockOAuthClient(200, `{bad json}`)
	defer func() { oauthHTTPClient = origClient }()

	_, err := GetAuthenticatedUser("tok")
	if err == nil {
		t.Fatal("expected error for invalid JSON body")
	}
}

// ─── RevokeToken ─────────────────────────────────────────────────────────────

func TestRevokeToken_Success(t *testing.T) {
	origClient := oauthHTTPClient
	oauthHTTPClient = mockOAuthClient(204, "")
	defer func() { oauthHTTPClient = origClient }()

	err := RevokeToken("client-id", "client-secret", "gho_token")
	if err != nil {
		t.Fatalf("RevokeToken error: %v", err)
	}
}

func TestRevokeToken_OK200(t *testing.T) {
	origClient := oauthHTTPClient
	oauthHTTPClient = mockOAuthClient(200, "")
	defer func() { oauthHTTPClient = origClient }()

	err := RevokeToken("cid", "sec", "tok")
	if err != nil {
		t.Fatalf("RevokeToken 200 error: %v", err)
	}
}

func TestRevokeToken_Error(t *testing.T) {
	origClient := oauthHTTPClient
	oauthHTTPClient = mockOAuthClient(422, `{"message":"error"}`)
	defer func() { oauthHTTPClient = origClient }()

	err := RevokeToken("cid", "sec", "tok")
	if err == nil {
		t.Fatal("expected error for 422")
	}
}
