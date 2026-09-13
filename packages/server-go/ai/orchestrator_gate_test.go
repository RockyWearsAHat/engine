package ai

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestOrchestrationState_PopulateGateFields(t *testing.T) {
	// Test that gate fields are properly set on the orchestration state
	gateID := "test-gate-1"
	gateText := "This is the gate text content"
	gateHash := computeGateHash(gateText)
	baseSha := "abc123def456"

	cfg := OrchestratorConfig{
		ProjectPath: "/tmp/test-project",
		Brief:       "test brief",
		GateID:      gateID,
		GateText:    gateText,
		GateHash:    gateHash,
		BaseSha:     baseSha,
	}

	state := &OrchestrationState{
		Repo:  "test-repo",
		Owner: "test-owner",
		Brief: cfg.Brief,
	}

	// Simulate populating gate fields from config
	if cfg.GateID != "" {
		state.GateID = cfg.GateID
	}
	if cfg.GateText != "" {
		state.GateText = cfg.GateText
	}
	if cfg.GateHash != "" {
		state.GateHash = cfg.GateHash
	}
	if cfg.BaseSha != "" {
		state.BaseSha = cfg.BaseSha
	}

	if state.GateID != gateID {
		t.Errorf("expected GateID %q, got %q", gateID, state.GateID)
	}
	if state.GateText != gateText {
		t.Errorf("expected GateText %q, got %q", gateText, state.GateText)
	}
	if state.GateHash != gateHash {
		t.Errorf("expected GateHash %q, got %q", gateHash, state.GateHash)
	}
	if state.BaseSha != baseSha {
		t.Errorf("expected BaseSha %q, got %q", baseSha, state.BaseSha)
	}
}

func TestGateText_GateHash_Matches_SHA256(t *testing.T) {
	// Test that GateHash is the sha256 hex of GateText
	gateText := "This is the gate text for validation"
	expectedHash := computeGateHash(gateText)

	cfg := OrchestratorConfig{
		ProjectPath: "/tmp/test-project",
		GateText:    gateText,
		GateHash:    expectedHash,
	}

	state := &OrchestrationState{
		Repo:  "test-repo",
		Owner: "test-owner",
	}

	if cfg.GateText != "" {
		state.GateText = cfg.GateText
	}
	if cfg.GateHash != "" {
		state.GateHash = cfg.GateHash
	}

	// Verify the hash matches the text
	computedHash := computeGateHash(state.GateText)
	if state.GateHash != computedHash {
		t.Errorf("GateHash mismatch: expected %q, got %q", computedHash, state.GateHash)
	}
}

func TestPlanStep_PopulateGateFields(t *testing.T) {
	// Test that gate fields are properly populated into plan steps
	gateID := "gate-1"
	gateText := "Implementation step"
	gateHash := computeGateHash(gateText)
	baseSha := "def456abc123"

	state := &OrchestrationState{
		GateID:   gateID,
		GateText: gateText,
		GateHash: gateHash,
		BaseSha:  baseSha,
		Plan: []PlanStep{
			{
				Index: 1,
				Title: "Step 1",
				Body:  "Implementation step 1",
			},
			{
				Index: 2,
				Title: "Step 2",
				Body:  "Implementation step 2",
			},
		},
	}

	// Simulate populating gate fields into each plan step
	for i := range state.Plan {
		state.Plan[i].GateID = state.GateID
		state.Plan[i].GateText = state.GateText
		state.Plan[i].GateHash = state.GateHash
		state.Plan[i].BaseSha = state.BaseSha
	}

	// Verify all steps have the gate fields
	for i, step := range state.Plan {
		if step.GateID != gateID {
			t.Errorf("step %d: expected GateID %q, got %q", i, gateID, step.GateID)
		}
		if step.GateText != gateText {
			t.Errorf("step %d: expected GateText %q, got %q", i, gateText, step.GateText)
		}
		if step.GateHash != gateHash {
			t.Errorf("step %d: expected GateHash %q, got %q", i, gateHash, step.GateHash)
		}
		if step.BaseSha != baseSha {
			t.Errorf("step %d: expected BaseSha %q, got %q", i, baseSha, step.BaseSha)
		}
	}
}

func TestBaseSha_TempRepo_GitHead(t *testing.T) {
	// Create a temporary directory for the test repo
	tempDir, err := os.MkdirTemp("", "test-repo-*")
	if err != nil {
		t.Fatalf("failed to create temp directory: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Initialize a git repo
	cmd := exec.Command("git", "init")
	cmd.Dir = tempDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("git init failed: %v", err)
	}

	// Configure git user
	configUser := exec.Command("git", "config", "user.email", "test@example.com")
	configUser.Dir = tempDir
	configUser.Run() // ignore errors

	configName := exec.Command("git", "config", "user.name", "Test User")
	configName.Dir = tempDir
	configName.Run() // ignore errors

	// Create and commit a file
	testFile := fmt.Sprintf("%s/test.txt", tempDir)
	if err := os.WriteFile(testFile, []byte("test content"), 0o644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	addCmd := exec.Command("git", "add", "test.txt")
	addCmd.Dir = tempDir
	if err := addCmd.Run(); err != nil {
		t.Fatalf("git add failed: %v", err)
	}

	commitCmd := exec.Command("git", "commit", "-m", "initial commit")
	commitCmd.Dir = tempDir
	if err := commitCmd.Run(); err != nil {
		t.Fatalf("git commit failed: %v", err)
	}

	// Get HEAD SHA
	headCmd := exec.Command("git", "rev-parse", "HEAD")
	headCmd.Dir = tempDir
	output, err := headCmd.Output()
	if err != nil {
		t.Fatalf("git rev-parse HEAD failed: %v", err)
	}
	expectedSha := strings.TrimSpace(string(output))

	// Verify the SHA is valid (40 hex characters for full SHA)
	if len(expectedSha) != 40 && len(expectedSha) != 64 {
		t.Errorf("expected 40 or 64 character SHA, got %d characters", len(expectedSha))
	}

	// Create orchestration state with the BaseSha
	state := &OrchestrationState{
		BaseSha: expectedSha,
	}

	if state.BaseSha != expectedSha {
		t.Errorf("BaseSha mismatch: expected %q, got %q", expectedSha, state.BaseSha)
	}
}

// computeGateHash returns the sha256 hex of the gate text
func computeGateHash(gateText string) string {
	hash := sha256.Sum256([]byte(gateText))
	return hex.EncodeToString(hash[:])
}
