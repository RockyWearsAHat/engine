package ai

import (
	"strings"
	"testing"
)

func TestExecuteToolForTest_ShellPublishBlockedWithoutExplicitIntent(t *testing.T) {
	ctx := &ChatContext{
		ProjectPath: t.TempDir(),
		Usage:       &SessionUsage{},
		Quarantine:  NewToolQuarantine(),
	}

	result, isErr := ExecuteToolForTest("shell", map[string]any{"command": "npm publish"}, ctx)
	if !isErr {
		t.Fatalf("expected publish shell command to be blocked, got result=%q", result)
	}
	if !strings.Contains(strings.ToLower(result), "blocked") {
		t.Fatalf("expected blocked error message, got %q", result)
	}
}
