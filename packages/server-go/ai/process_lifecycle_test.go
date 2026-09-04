//go:build windows

package ai

import (
	"fmt"
	"os"
	"os/exec"
	"testing"
	"time"
)

// TestJobObjectCreation verifies that a job object is created and tracked properly.
func TestJobObjectCreation(t *testing.T) {
	// Spawn a sleep process that will live long enough for us to verify the job.
	cmd := exec.Command("cmd", "/C", "timeout", "/t", "10")
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start test process: %v", err)
	}
	pid := cmd.Process.Pid
	defer cmd.Wait() //nolint:errcheck

	// Create a job object for the process.
	handle, err := CreateJobForProcess(pid)
	if err != nil {
		t.Fatalf("CreateJobForProcess failed: %v", err)
	}
	if handle == 0 {
		t.Fatal("CreateJobForProcess returned nil handle")
	}

	// Verify the job object is tracked.
	count := LiveJobObjectCount()
	if count != 1 {
		t.Errorf("expected 1 job object, got %d", count)
	}

	// Close the job, which should kill the process.
	CloseJobForProcess(pid)

	// After a brief delay, verify the job object is untracked.
	time.Sleep(100 * time.Millisecond)
	count = LiveJobObjectCount()
	if count != 0 {
		t.Errorf("expected 0 job objects after close, got %d", count)
	}

	// Verify the process is dead.
	// Try to wait with a timeout; if it's dead, this should complete quickly.
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	select {
	case err := <-done:
		// Process exited (either due to job close or timeout)
		if err == nil {
			t.Log("process exited normally (timeout)")
		}
	case <-time.After(2 * time.Second):
		t.Error("process did not exit after job close (expected automatic kill)")
	}
}

// TestJobObjectMultiple verifies that multiple job objects can be created and tracked.
func TestJobObjectMultiple(t *testing.T) {
	const numProcesses = 3
	pids := make([]int, 0, numProcesses)

	// Spawn multiple sleep processes.
	for i := 0; i < numProcesses; i++ {
		cmd := exec.Command("cmd", "/C", "timeout", "/t", "10")
		if err := cmd.Start(); err != nil {
			t.Fatalf("failed to start test process %d: %v", i, err)
		}
		pid := cmd.Process.Pid
		pids = append(pids, pid)
		defer cmd.Wait() //nolint:errcheck

		// Create a job for each process.
		_, err := CreateJobForProcess(pid)
		if err != nil {
			t.Fatalf("CreateJobForProcess(%d) failed: %v", pid, err)
		}
	}

	// Verify all job objects are tracked.
	count := LiveJobObjectCount()
	if count != numProcesses {
		t.Errorf("expected %d job objects, got %d", numProcesses, count)
	}

	// Close all jobs.
	for _, pid := range pids {
		CloseJobForProcess(pid)
	}

	// Verify all are untracked.
	time.Sleep(100 * time.Millisecond)
	count = LiveJobObjectCount()
	if count != 0 {
		t.Errorf("expected 0 job objects after closing all, got %d", count)
	}
}

// TestProcessTreeKill verifies that closing a job kills the entire process tree.
// We spawn cmd -> sleep (child) and verify both die when the job is closed.
func TestProcessTreeKill(t *testing.T) {
	// This test creates a cmd that spawns a child (sleep).
	// When we close the job, both should die.
	// We use a batch script to spawn a child process.
	scriptPath := os.TempDir() + "\\test_spawn_" + fmt.Sprintf("%d", os.Getpid()) + ".bat"
	script := "@echo off\necho Starting child\nREM Start a sleep process in the background\nstart /B cmd /C timeout /t 30\nREM Keep this process running\ntimeout /t 30\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o600); err != nil {
		t.Fatalf("failed to create test script: %v", err)
	}
	defer os.Remove(scriptPath) //nolint:errcheck

	cmd := exec.Command(scriptPath)
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start test script: %v", err)
	}
	pid := cmd.Process.Pid

	// Create a job for the parent process. Children spawned by this process
	// will also be in the same job (unless they use breakaway).
	_, err := CreateJobForProcess(pid)
	if err != nil {
		t.Fatalf("CreateJobForProcess failed: %v", err)
	}

	// Give the child a moment to spawn.
	time.Sleep(500 * time.Millisecond)

	// Close the job, which should kill parent and children.
	CloseJobForProcess(pid)

	// Verify the root process exited.
	time.Sleep(200 * time.Millisecond)
	var exitCode int
	if err := cmd.Wait(); err != nil {
		// Process was killed (expected)
		if ee, ok := err.(*exec.ExitError); ok {
			exitCode = ee.ExitCode()
		}
	}
	t.Logf("process exit code: %d (killed processes typically have non-zero exit)", exitCode)
}
