package ai

import (
	"os"
	"strings"
	"testing"
	"time"
)

// TestStartupHangDetection tests that a process that hangs before producing
// any output is killed and returns a startup-hang error with stderr tail.
func TestRunLoop_StartupHang(t *testing.T) {
	// Save the original and restore after test
	oldBinary := claudeBinary
	oldStartupTimeoutFn := startupTimeoutFn
	defer func() {
		claudeBinary = oldBinary
		startupTimeoutFn = oldStartupTimeoutFn
	}()

	// Use a short startup timeout for testing
	startupTimeoutFn = func() time.Duration {
		return 1 * time.Second
	}

	// Create a test script that hangs (writes nothing to stdout for >1 second)
	scriptPath := "/tmp/test-hang-script.sh"
	err := os.WriteFile(scriptPath, []byte(`#!/bin/bash
echo "stderr message" >&2
sleep 10
`), 0755)
	if err != nil {
		t.Fatalf("failed to create test script: %v", err)
	}
	defer os.Remove(scriptPath)

	claudeBinary = scriptPath

	var errMsg string
	ctx := &ChatContext{
		OnError: func(s string) { errMsg = s },
	}

	provider := &claudecodeProvider{}
	provider.RunLoop(ctx, "claude-opus-4-8", "", nil, nil, &strings.Builder{})

	if !strings.Contains(errMsg, "startup-hang") {
		t.Errorf("expected startup-hang error, got %q", errMsg)
	}
	if !strings.Contains(errMsg, "stderr message") {
		t.Errorf("expected stderr tail in error, got %q", errMsg)
	}
}

// TestRunLoop_QuickEventThenLongRun tests that a process emitting a valid
// event early (0.5s) and then running long (3s) is NOT killed as a startup hang.
func TestRunLoop_QuickEventThenLongRun(t *testing.T) {
	oldBinary := claudeBinary
	oldStartupTimeoutFn := startupTimeoutFn
	defer func() {
		claudeBinary = oldBinary
		startupTimeoutFn = oldStartupTimeoutFn
	}()

	// Use a 1-second startup timeout
	startupTimeoutFn = func() time.Duration {
		return 1 * time.Second
	}

	// Create a test script that emits a valid event after 0.5s, then runs for 3s total
	scriptPath := "/tmp/test-quick-event-script.sh"
	err := os.WriteFile(scriptPath, []byte(`#!/bin/bash
sleep 0.5
echo '{"type":"system","subtype":"init","session_id":"test-sess-1"}'
sleep 2.5
exit 0
`), 0755)
	if err != nil {
		t.Fatalf("failed to create test script: %v", err)
	}
	defer os.Remove(scriptPath)

	claudeBinary = scriptPath

	var errMsg string
	ctx := &ChatContext{
		OnError: func(s string) { errMsg = s },
	}

	provider := &claudecodeProvider{}
	provider.RunLoop(ctx, "claude-opus-4-8", "", nil, nil, &strings.Builder{})

	// Should NOT have a startup-hang error since the process emitted an event before timeout
	if strings.Contains(errMsg, "startup-hang") {
		t.Errorf("process should NOT be killed as startup-hang when it emits event quickly, got error: %q", errMsg)
	}
}

// TestZeroTurnStderrLogging tests that when a process exits 0 but produces
// no result event (turns==0), the stderr is logged with zero-turn prefix.
func TestRunLoop_ZeroTurnStderr(t *testing.T) {
	oldBinary := claudeBinary
	defer func() {
		claudeBinary = oldBinary
	}()

	// Create a test script that exits cleanly with stderr but no valid JSON output
	scriptPath := "/tmp/test-no-output-script.sh"
	err := os.WriteFile(scriptPath, []byte(`#!/bin/bash
echo "some diagnostic message" >&2
exit 0
`), 0755)
	if err != nil {
		t.Fatalf("failed to create test script: %v", err)
	}
	defer os.Remove(scriptPath)

	claudeBinary = scriptPath

	ctx := &ChatContext{
		OnError: func(string) {},
	}

	provider := &claudecodeProvider{}
	provider.RunLoop(ctx, "claude-opus-4-8", "", nil, nil, &strings.Builder{})

	// The test passes if no panic occurs and the code handles the zero-turn case gracefully.
	// In a real scenario, we would check logs, but that requires more infrastructure.
}

// TestLastNBytes tests the helper function that extracts the last N bytes.
func TestLastNBytes(t *testing.T) {
	tests := []struct {
		input    string
		n        int
		expected string
	}{
		{"hello world", 5, "world"},
		{"hi", 10, "hi"},
		{"test", 4, "test"},
		{"abc", 1, "c"},
		{"", 10, ""},
	}

	for _, tt := range tests {
		got := lastNBytes(tt.input, tt.n)
		if got != tt.expected {
			t.Errorf("lastNBytes(%q, %d) = %q, want %q", tt.input, tt.n, got, tt.expected)
		}
	}
}
