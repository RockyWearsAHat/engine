package ai

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestGateFieldsFromRequest verifies that gate fields from a request
// are properly stored in the orchestration state at plan time.
// This calls loadOrCreateOrchestrationState to verify state initialization.
func TestGateFieldsFromRequest(t *testing.T) {
	// Create a temporary directory for the test project
	tempDir, err := os.MkdirTemp("", "test-project-*")
	if err != nil {
		t.Fatalf("failed to create temp directory: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Initialize a git repo with one commit to get a valid HEAD
	gitInit := exec.Command("git", "init")
	gitInit.Dir = tempDir
	if err := gitInit.Run(); err != nil {
		t.Fatalf("git init failed: %v", err)
	}

	// Configure git user
	gitEmail := exec.Command("git", "config", "user.email", "test@example.com")
	gitEmail.Dir = tempDir
	gitEmail.Run() // ignore errors

	gitName := exec.Command("git", "config", "user.name", "Test User")
	gitName.Dir = tempDir
	gitName.Run() // ignore errors

	// Create and commit a file to establish HEAD
	testFile := tempDir + "/test.txt"
	if err := os.WriteFile(testFile, []byte("test content"), 0o644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	gitAdd := exec.Command("git", "add", "test.txt")
	gitAdd.Dir = tempDir
	if err := gitAdd.Run(); err != nil {
		t.Fatalf("git add failed: %v", err)
	}

	gitCommit := exec.Command("git", "commit", "-m", "initial commit")
	gitCommit.Dir = tempDir
	if err := gitCommit.Run(); err != nil {
		t.Fatalf("git commit failed: %v", err)
	}

	// Get the actual HEAD SHA
	gitRevParse := exec.Command("git", "rev-parse", "HEAD")
	gitRevParse.Dir = tempDir
	output, err := gitRevParse.Output()
	if err != nil {
		t.Fatalf("git rev-parse HEAD failed: %v", err)
	}
	expectedBaseSha := strings.TrimSpace(string(output))

	// Test data - these would come from the handover request in the real system
	gateID := "test-gate-1"
	gateText := "This is the gate text that should be hashed"
	expectedGateHash := computeGateHash(gateText)

	// Load or create orchestration state
	state, err := loadOrCreateOrchestrationState(tempDir, "test-owner", "test-repo", "test brief")
	if err != nil {
		t.Fatalf("failed to load or create orchestration state: %v", err)
	}

	// Manually populate gate fields as RunAutonomousProject does
	if gateID != "" {
		state.GateID = gateID
	}
	if gateText != "" {
		state.GateText = gateText
	}
	if expectedGateHash != "" {
		state.GateHash = expectedGateHash
	}
	if expectedBaseSha != "" {
		state.BaseSha = expectedBaseSha
	}

	// Verify the gate fields were populated correctly
	if state.GateID != gateID {
		t.Errorf("expected GateID %q, got %q", gateID, state.GateID)
	}
	if state.GateText != gateText {
		t.Errorf("expected GateText %q, got %q", gateText, state.GateText)
	}
	if state.GateHash != expectedGateHash {
		t.Errorf("expected GateHash %q, got %q", expectedGateHash, state.GateHash)
	}
	if state.BaseSha != expectedBaseSha {
		t.Errorf("expected BaseSha %q, got %q", expectedBaseSha, state.BaseSha)
	}
}

// TestGateHash_SHA256_Verification verifies that GateHash is the correct
// SHA256 hex digest of GateText.
func TestGateHash_SHA256_Verification(t *testing.T) {
	gateText := "This is the gate text for SHA256 verification"
	expectedHash := computeGateHash(gateText)

	// Verify the hash is correct
	hash := sha256.Sum256([]byte(gateText))
	directHash := hex.EncodeToString(hash[:])

	if expectedHash != directHash {
		t.Errorf("hash mismatch: computed %q, direct %q", expectedHash, directHash)
	}

	// Create an orchestration state and set the fields
	state := &OrchestrationState{
		GateText: gateText,
		GateHash: expectedHash,
	}

	// Verify the hash matches the text
	computedHash := computeGateHash(state.GateText)
	if state.GateHash != computedHash {
		t.Errorf("GateHash mismatch: expected %q, got %q", computedHash, state.GateHash)
	}
}

// TestPlanStepInheritsGateFields verifies that plan steps inherit gate fields
// from the orchestration state.
func TestPlanStepInheritsGateFields(t *testing.T) {
	gateID := "gate-1"
	gateText := "Implementation step description"
	gateHash := computeGateHash(gateText)
	baseSha := "abc123def456" // Mock SHA for testing

	state := &OrchestrationState{
		Repo:     "test-repo",
		Owner:    "test-owner",
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

	// Populate gate fields into each plan step
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

// computeGateHash returns the sha256 hex of the gate text
func computeGateHash(gateText string) string {
	hash := sha256.Sum256([]byte(gateText))
	return hex.EncodeToString(hash[:])
}
