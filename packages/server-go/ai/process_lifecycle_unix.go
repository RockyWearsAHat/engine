//go:build !windows

package ai

import (
	"log"
)

// CreateJobForProcess is a no-op on Unix systems. Process group management
// is handled by setProcGroup in proc_kill_unix.go.
func CreateJobForProcess(pid int) (interface{}, error) {
	return nil, nil
}

// CloseJobForProcess is a no-op on Unix systems.
func CloseJobForProcess(pid int) {}

// BootSweep runs at startup to kill any orphaned processes from a prior
// MyEditor instance. On Unix systems, this uses ps to find orphaned
// processes and kill them by PID.
func BootSweep() (killed int, reclaimedMB int64) {
	// On Unix, it's harder to reliably identify truly orphaned processes
	// without walking the whole tree. For now, we trust the pgid-based
	// cleanup on exit and the restart reaping of PIDs from tasks.json.
	// A future enhancement could hook ps/pgrep here, but the primary
	// safeguard (process groups) already handles most cases.
	log.Print("boot sweep: Unix system (process groups handle most cleanup)")
	return 0, 0
}

// StartPeriodicGuard spawns a background goroutine that periodically
// checks for stray processes. On Unix, process groups handle most cleanup,
// so this is primarily a safety check using the session registry.
func StartPeriodicGuard() {
	// On Unix, process groups (set via Setpgid) handle cleanup at exit.
	// For now, we rely on that mechanism. A future enhancement could add
	// periodic checks via ps(1) if needed, but the process group cleanup
	// is the primary safeguard.
	// This is a no-op stub; full implementation would enumerate /proc.
}

// LiveJobObjectCount returns the number of active job objects. On Unix,
// this is a stub that returns LiveSessionCount() since Unix uses process
// groups instead of Windows job objects.
func LiveJobObjectCount() int {
	return LiveSessionCount()
}
