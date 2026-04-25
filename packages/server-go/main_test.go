package main

import (
	"encoding/json"
	"testing"
)

func TestDefaultProjectPath_NonEmpty(t *testing.T) {
	path := defaultProjectPath()
	if path == "" {
		t.Error("defaultProjectPath() should never return empty string")
	}
}

func TestTriggerIssueSession_BadPayload(t *testing.T) {
	// Bad JSON — ParseIssueComment fails; function returns early without touching DB or AI.
	triggerIssueSession(t.TempDir(), json.RawMessage(`{bad json}`))
}

func TestTriggerIssueSession_ZeroIssueNumber(t *testing.T) {
	// Valid JSON but issue.number is zero — treated as unparseable, returns early.
	payload := json.RawMessage(`{"action":"created","comment":{"body":"hi","user":{"login":"bob"}},"issue":{"number":0,"title":""},"repository":{"full_name":"owner/repo"}}`)
	triggerIssueSession(t.TempDir(), payload)
}

func TestTriggerIssueOpenedSession_BadPayload(t *testing.T) {
	// Bad JSON — ParseIssue fails; function returns early without touching DB or AI.
	triggerIssueOpenedSession(t.TempDir(), json.RawMessage(`{bad json}`))
}

func TestTriggerIssueOpenedSession_ZeroIssueNumber(t *testing.T) {
	// Valid JSON but issue.number is zero — returns early.
	payload := json.RawMessage(`{"action":"opened","issue":{"number":0,"title":""},"repository":{"full_name":"owner/repo"},"sender":{"login":"alice"}}`)
	triggerIssueOpenedSession(t.TempDir(), payload)
}

func TestTriggerScaffoldSession_BadPayload(t *testing.T) {
	// Bad JSON should return early without side effects.
	triggerScaffoldSession(t.TempDir(), json.RawMessage(`{bad json}`))
}

func TestTriggerScaffoldSession_BadFullName(t *testing.T) {
	// Missing owner/repo separator should return early.
	payload := json.RawMessage(`{"repository":{"full_name":"owner-only"}}`)
	triggerScaffoldSession(t.TempDir(), payload)
}

func TestTriggerCIAnalysisSession_BadPayload(t *testing.T) {
	// Bad JSON should return early without side effects.
	triggerCIAnalysisSession(t.TempDir(), json.RawMessage(`{bad json}`))
}

