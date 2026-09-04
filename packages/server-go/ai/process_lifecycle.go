//go:build windows

package ai

import (
	"fmt"
	"log"
	"os"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// jobObjectRegistry maps a process PID to its Windows Job Object handle.
// Job objects ensure the entire process tree (including grandchildren like
// engine-server.exe MCP bridge) dies when the job is closed.
var (
	jobObjectMu sync.Mutex
	jobObjects  = make(map[int]windows.Handle)
)

// CreateJobForProcess creates a Windows Job Object for a process and assigns
// the process to it. The job is configured with JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
// so all processes in the job die when the job handle is closed.
// Call this immediately after cmd.Start() succeeds.
// Returns the job handle for cleanup later (store in registry).
func CreateJobForProcess(pid int) (windows.Handle, error) {
	if pid <= 0 {
		return 0, nil
	}

	// Create job object with a unique name (PIDs are unique across system).
	jobName := fmt.Sprintf("MyEditor_%d_%d", os.Getpid(), pid)
	jobNameUTF16, err := syscall.UTF16PtrFromString(jobName)
	if err != nil {
		return 0, fmt.Errorf("job name conversion: %w", err)
	}

	jobHandle, err := windows.CreateJobObject(nil, jobNameUTF16)
	if err != nil {
		return 0, fmt.Errorf("CreateJobObject: %w", err)
	}

	// Set the job object to kill all processes when the handle closes.
	jeli := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
		BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{
			LimitFlags: windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE,
		},
	}

	if _, err := windows.SetInformationJobObject(
		jobHandle,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&jeli)),
		uint32(unsafe.Sizeof(jeli)),
	); err != nil {
		windows.CloseHandle(jobHandle)
		return 0, fmt.Errorf("SetInformationJobObject: %w", err)
	}

	// Assign the process to the job.
	hProcess, err := windows.OpenProcess(
		windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE,
		false,
		uint32(pid),
	)
	if err != nil {
		windows.CloseHandle(jobHandle)
		return 0, fmt.Errorf("OpenProcess: %w", err)
	}
	defer windows.CloseHandle(hProcess)

	if err := windows.AssignProcessToJobObject(jobHandle, hProcess); err != nil {
		windows.CloseHandle(jobHandle)
		return 0, fmt.Errorf("AssignProcessToJobObject: %w", err)
	}

	// Store the job handle for later cleanup.
	jobObjectMu.Lock()
	jobObjects[pid] = jobHandle
	jobObjectMu.Unlock()

	return jobHandle, nil
}

// CloseJobForProcess closes the job object associated with a PID,
// which kills all processes in that job.
func CloseJobForProcess(pid int) {
	jobObjectMu.Lock()
	handle, ok := jobObjects[pid]
	if ok {
		delete(jobObjects, pid)
	}
	jobObjectMu.Unlock()

	if ok {
		windows.CloseHandle(handle)
	}
}

// LiveJobObjectCount returns the number of active job objects (process trees).
// Used for logging and process pile-up visibility.
func LiveJobObjectCount() int {
	jobObjectMu.Lock()
	defer jobObjectMu.Unlock()
	return len(jobObjects)
}

// BootSweep runs at startup to kill any orphaned processes from a prior
// MyEditor instance. On Windows, this is a placeholder; the primary safeguard
// is the job objects created for each task, which automatically kill all children
// when closed. Real orphan sweeping would require more sophisticated process
// tree analysis.
//
// Returns the count of processes killed and total MB reclaimed.
func BootSweep() (killed int, reclaimedMB int64) {
	// Placeholder: On Windows, job objects handle cleanup when tasks end.
	// Future enhancement: enumerate processes via tasklist.exe and kill
	// orphaned claude.exe/engine-server.exe/node.exe not in current job objects.
	log.Print("boot sweep: Windows system (job objects handle cleanup)")
	return 0, 0
}

// getProcessWorkingSetMB returns the working set (physical memory) used by a
// process in MB, for accounting purposes. Returns 0 as a placeholder since
// GetProcessMemoryInfo requires cgo or additional system calls not available
// in golang.org/x/sys/windows.
func getProcessWorkingSetMB(pid int) int64 {
	// Note: A full implementation would use GetProcessMemoryInfo via syscall,
	// but for now we return 0 as a placeholder. The boot sweep logging is
	// informational; exact memory accounting can be added later.
	return 0
}

// StartPeriodicGuard spawns a background goroutine that every 60 seconds
// checks for processes in the session registry that are no longer running
// and kills any strays. This is a safety net for processes that somehow
// escaped normal cleanup.
func StartPeriodicGuard() {
	go func() {
		ticker := time.NewTicker(60 * time.Second)
		defer ticker.Stop()

		for range ticker.C {
			guardOnce()
		}
	}()
}

// guardOnce checks for and kills stray processes in registered but terminal tasks.
func guardOnce() {
	// Get all registered PIDs across all tasks.
	sessionRegistryMu.Lock()
	allPIDs := make([]int, 0)
	for _, pids := range sessionRegistry {
		for pid := range pids {
			allPIDs = append(allPIDs, pid)
		}
	}
	sessionRegistryMu.Unlock()

	// Check each PID: if it's no longer running, kill its job.
	killed := 0
	for _, pid := range allPIDs {
		if !isProcessRunning(pid) {
			CloseJobForProcess(pid)
			killed++
		}
	}

	if killed > 0 {
		log.Printf("periodic guard: reaped %d stray process(es)", killed)
	}
}

// isProcessRunning checks if a process with the given PID is still running.
func isProcessRunning(pid int) bool {
	hProcess, err := windows.OpenProcess(windows.PROCESS_QUERY_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(hProcess)

	var exitCode uint32
	if err := windows.GetExitCodeProcess(hProcess, &exitCode); err != nil {
		return false
	}

	// STILL_ACTIVE is 259.
	return exitCode == 259
}
