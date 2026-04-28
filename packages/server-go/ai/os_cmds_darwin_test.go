//go:build darwin

package ai

import (
	"errors"
	"strings"
	"testing"
)

// TestOSCmdDefaults_Darwin exercises all four browser OS-command function
// bodies by injecting a fast-failing osascriptFn so no real system calls are made.
func TestOSCmdDefaults_Darwin(t *testing.T) {
	origOsascript := osascriptFn
	t.Cleanup(func() { osascriptFn = origOsascript })
	osascriptFn = func(script string) ([]byte, error) {
		return []byte("mock output"), errors.New("mock osascript error")
	}

	_, err := browserNavigateFnForOS("http://example.com")
	if err == nil || !strings.Contains(err.Error(), "browser_navigate") {
		t.Fatalf("expected browser_navigate error, got %v", err)
	}

	_, err = browserReadPageFnForOS()
	if err == nil || !strings.Contains(err.Error(), "browser_read_page") {
		t.Fatalf("expected browser_read_page error, got %v", err)
	}

	_, err = browserClickFnForOS(10, 20)
	if err == nil || !strings.Contains(err.Error(), "browser_click") {
		t.Fatalf("expected browser_click error, got %v", err)
	}

	_, err = browserTypeFnForOS("hello")
	if err == nil || !strings.Contains(err.Error(), "browser_type") {
		t.Fatalf("expected browser_type error, got %v", err)
	}
}

// TestOSCmdDefaults_Darwin_Success exercises the success paths by having
// osascriptFn return no error.
func TestOSCmdDefaults_Darwin_Success(t *testing.T) {
	origOsascript := osascriptFn
	t.Cleanup(func() { osascriptFn = origOsascript })
	osascriptFn = func(script string) ([]byte, error) {
		return []byte("page content"), nil
	}

	result, err := browserNavigateFnForOS("http://example.com")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "example.com") {
		t.Fatalf("unexpected result: %q", result)
	}

	result, err = browserReadPageFnForOS()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "page content" {
		t.Fatalf("unexpected result: %q", result)
	}

	result, err = browserClickFnForOS(10, 20)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "10") {
		t.Fatalf("unexpected result: %q", result)
	}

	result, err = browserTypeFnForOS("hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == "" {
		t.Fatalf("expected non-empty result")
	}
}

// TestOsascriptFn_Default exercises the default osascriptFn which calls the
// real exec.Command. Uses an invalid script so it fails fast without side effects.
func TestOsascriptFn_Default(t *testing.T) {
	// "syntax error" is an intentionally invalid AppleScript that fails instantly.
	_, err := osascriptFn("syntax error")
	if err == nil {
		t.Fatal("expected osascript to fail for invalid script")
	}
}
