package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLiveURLRead(t *testing.T) {
	tempDir := t.TempDir()

	// Test 1: File present with URL
	engineDir := filepath.Join(tempDir, ".engine")
	if err := os.MkdirAll(engineDir, 0755); err != nil {
		t.Fatalf("Failed to create .engine directory: %v", err)
	}
	liveURLFile := filepath.Join(engineDir, "live-url.txt")
	if err := os.WriteFile(liveURLFile, []byte("https://x.test/"), 0644); err != nil {
		t.Fatalf("Failed to write live-url.txt: %v", err)
	}

	url := readLiveURL(tempDir)
	if url != "https://x.test/" {
		t.Errorf("Expected 'https://x.test/', got '%s'", url)
	}
}

func TestLiveURLAbsent(t *testing.T) {
	tempDir := t.TempDir()

	// Test 2: File absent
	url := readLiveURL(tempDir)
	if url != "" {
		t.Errorf("Expected empty string for absent file, got '%s'", url)
	}
}

func TestLiveURLWithWhitespace(t *testing.T) {
	tempDir := t.TempDir()

	// Test 3: File with whitespace
	engineDir := filepath.Join(tempDir, ".engine")
	if err := os.MkdirAll(engineDir, 0755); err != nil {
		t.Fatalf("Failed to create .engine directory: %v", err)
	}
	liveURLFile := filepath.Join(engineDir, "live-url.txt")
	if err := os.WriteFile(liveURLFile, []byte("  https://x.test/  \n"), 0644); err != nil {
		t.Fatalf("Failed to write live-url.txt: %v", err)
	}

	url := readLiveURL(tempDir)
	if url != "https://x.test/" {
		t.Errorf("Expected 'https://x.test/', got '%s'", url)
	}
}

func TestLiveURLEmptyProjectPath(t *testing.T) {
	// Test 4: Empty project path
	url := readLiveURL("")
	if url != "" {
		t.Errorf("Expected empty string for empty project path, got '%s'", url)
	}
}

func TestSnapshotWithLiveURL(t *testing.T) {
	tempDir := t.TempDir()

	// Create .engine/live-url.txt
	engineDir := filepath.Join(tempDir, ".engine")
	if err := os.MkdirAll(engineDir, 0755); err != nil {
		t.Fatalf("Failed to create .engine directory: %v", err)
	}
	liveURLFile := filepath.Join(engineDir, "live-url.txt")
	if err := os.WriteFile(liveURLFile, []byte("https://x.test/"), 0644); err != nil {
		t.Fatalf("Failed to write live-url.txt: %v", err)
	}

	// Create an engineTask
	task := &engineTask{
		ID:          "test-task",
		ProjectPath: tempDir,
		Brief:       "Test brief",
		Status:      taskRunning,
		Phase:       "test",
		Detail:      "test detail",
		StartedAt:   time.Now(),
	}

	snapshot := task.snapshot()
	if liveUrl, ok := snapshot["liveUrl"]; ok {
		if liveUrl != "https://x.test/" {
			t.Errorf("Expected 'https://x.test/' in snapshot, got '%v'", liveUrl)
		}
	} else {
		t.Error("liveUrl not found in snapshot")
	}
}

func TestSnapshotWithoutLiveURL(t *testing.T) {
	tempDir := t.TempDir()

	// Don't create .engine/live-url.txt

	// Create an engineTask
	task := &engineTask{
		ID:          "test-task",
		ProjectPath: tempDir,
		Brief:       "Test brief",
		Status:      taskRunning,
		Phase:       "test",
		Detail:      "test detail",
		StartedAt:   time.Now(),
	}

	snapshot := task.snapshot()
	if liveUrl, ok := snapshot["liveUrl"]; ok {
		if liveUrl != "" {
			t.Errorf("Expected empty string in snapshot when file absent, got '%v'", liveUrl)
		}
	} else {
		t.Error("liveUrl not found in snapshot")
	}
}

func TestCompletionPayloadWithLiveURL(t *testing.T) {
	tempDir := t.TempDir()

	// Create .engine/live-url.txt
	engineDir := filepath.Join(tempDir, ".engine")
	if err := os.MkdirAll(engineDir, 0755); err != nil {
		t.Fatalf("Failed to create .engine directory: %v", err)
	}
	liveURLFile := filepath.Join(engineDir, "live-url.txt")
	if err := os.WriteFile(liveURLFile, []byte("https://x.test/"), 0644); err != nil {
		t.Fatalf("Failed to write live-url.txt: %v", err)
	}

	// Create an engineTask
	task := &engineTask{
		ID:          "test-task",
		ProjectPath: tempDir,
		Brief:       "Test brief",
		Status:      taskDone,
		Phase:       "test",
		Detail:      "test detail",
		StartedAt:   time.Now(),
	}

	payload := task.completionPayload()
	if liveUrl, ok := payload["liveUrl"]; ok {
		if liveUrl != "https://x.test/" {
			t.Errorf("Expected 'https://x.test/' in completionPayload, got '%v'", liveUrl)
		}
	} else {
		t.Error("liveUrl not found in completionPayload")
	}
}

func TestCompletionPayloadWithoutLiveURL(t *testing.T) {
	tempDir := t.TempDir()

	// Don't create .engine/live-url.txt

	// Create an engineTask
	task := &engineTask{
		ID:          "test-task",
		ProjectPath: tempDir,
		Brief:       "Test brief",
		Status:      taskDone,
		Phase:       "test",
		Detail:      "test detail",
		StartedAt:   time.Now(),
	}

	payload := task.completionPayload()
	if liveUrl, ok := payload["liveUrl"]; ok {
		if liveUrl != "" {
			t.Errorf("Expected empty string in completionPayload when file absent, got '%v'", liveUrl)
		}
	} else {
		t.Error("liveUrl not found in completionPayload")
	}
}
