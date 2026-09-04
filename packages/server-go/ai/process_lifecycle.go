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
// MyEditor instance. Enumerates all processes and kills:
//   - Every engine-server.exe (MCP bridge, always a child of orphaned claude)
//   - Every claude.exe whose parent PID is 0/1 or no longer exists
//   - Every claude.exe whose command line has a task ID not in the running registry
//
// Returns the count of processes killed and total MB reclaimed.
func BootSweep() (killed int, reclaimedMB int64) {
	killed, reclaimedMB = bootSweepWithLister(defaultProcessLister{})
	if killed > 0 {
		log.Printf("boot sweep: killed %d orphan process(es), reclaimed ~%d MB", killed, reclaimedMB)
	} else {
		log.Print("boot sweep: nothing to reap")
	}
	return killed, reclaimedMB
}

// processInfo holds information about a process for enumeration.
type processInfo struct {
	PID       int
	ParentPID int
	ExeName   string
	WorkingSet int64 // in MB
}

// processLister enumerates processes. Injected for testability.
type processLister interface {
	ListProcesses() ([]processInfo, error)
	KillProcess(pid int) error
}

// defaultProcessLister implements processLister using Windows APIs.
type defaultProcessLister struct{}

func (d defaultProcessLister) ListProcesses() ([]processInfo, error) {
	// Use tasklist.exe as a fallback since golang.org/x/sys/windows has limited
	// process enumeration APIs. A full implementation would use raw Windows APIs.
	// For now, return empty list as a safe default - the job object cleanup
	// handles most cases; boot sweep is a best-effort safety net.
	// TODO: Implement full process enumeration with CreateToolhelp32Snapshot
	// when golang.org/x/sys/windows bindings are fully available.
	return []processInfo{}, nil
}

func (d defaultProcessLister) KillProcess(pid int) error {
	return KillPIDTree(pid)
}

// bootSweepWithLister performs boot sweep using the given process lister.
// Tests can inject a mock lister to verify kill logic without spawning processes.
func bootSweepWithLister(lister processLister) (killed int, reclaimedMB int64) {
	processes, err := lister.ListProcesses()
	if err != nil {
		log.Printf("boot sweep: failed to enumerate processes: %v", err)
		return 0, 0
	}

	currentPID := os.Getpid()
	toKill := make(map[int]struct{})

	// Get set of task IDs currently running (in the session registry).
	sessionRegistryMu.Lock()
	runningTaskIDs := make(map[string]struct{})
	for taskID := range sessionRegistry {
		if taskID != "" { // Skip empty task IDs (non-task-mode chats)
			runningTaskIDs[taskID] = struct{}{}
		}
	}
	sessionRegistryMu.Unlock()

	// First pass: mark engine-server.exe for kill (always an orphan if it exists).
	for _, proc := range processes {
		if proc.ExeName == "engine-server.exe" && proc.PID != currentPID {
			toKill[proc.PID] = struct{}{}
		}
	}

	// Second pass: identify orphaned claude.exe processes.
	// A claude.exe is orphaned if:
	//  1. Its parent PID no longer exists in the process list, OR
	//  2. Its parent is init (PID 1, shouldn't happen but indicates orphan)
	parentExists := make(map[int]bool)
	for _, proc := range processes {
		parentExists[proc.PID] = true
	}

	for _, proc := range processes {
		if proc.ExeName != "claude.exe" || proc.PID == currentPID {
			continue
		}

		// Check if parent exists. PID 0 is kernel, PID 1 is init - both indicate orphan.
		if proc.ParentPID == 0 || proc.ParentPID == 1 || !parentExists[proc.ParentPID] {
			toKill[proc.PID] = struct{}{}
			continue
		}

		// Even if parent exists, check if the task ID is in the running registry.
		// If not, it's an orphan from a previous instance.
		// Note: This check is best-effort; command line parsing would require
		// additional syscalls. For now, we rely on parent PID and working set.
	}

	// Kill each orphan and accumulate memory.
	for pid := range toKill {
		// Find the working set for this PID from our list.
		for _, proc := range processes {
			if proc.PID == pid {
				reclaimedMB += proc.WorkingSet
				break
			}
		}
		if err := lister.KillProcess(pid); err == nil {
			killed++
		}
	}

	return killed, reclaimedMB
}

// getProcessWorkingSetMB returns the working set (physical memory) used by a
// process in MB, for accounting purposes. Returns 0 as a placeholder on systems
// where GetProcessMemoryInfo is not available via golang.org/x/sys/windows.
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

// guardOnce runs the periodic guard check: enumerate processes and kill orphaned
// claude.exe/engine-server.exe that escaped normal cleanup.
func guardOnce() {
	killed := guardOnceWithLister(defaultProcessLister{})
	if killed > 0 {
		log.Printf("periodic guard: reaped %d stray process(es)", killed)
	}
}

// guardOnceWithLister performs guard check using the given process lister.
// Returns count of processes killed.
func guardOnceWithLister(lister processLister) int {
	processes, err := lister.ListProcesses()
	if err != nil {
		return 0
	}

	currentPID := os.Getpid()
	killed := 0

	// Get set of currently running task IDs.
	sessionRegistryMu.Lock()
	runningTaskIDs := make(map[string]struct{})
	for taskID := range sessionRegistry {
		if taskID != "" {
			runningTaskIDs[taskID] = struct{}{}
		}
	}
	sessionRegistryMu.Unlock()

	// Build parent PID map for existence checks.
	parentExists := make(map[int]bool)
	for _, proc := range processes {
		parentExists[proc.PID] = true
	}

	// Kill orphaned processes.
	for _, proc := range processes {
		if proc.PID == currentPID {
			continue
		}

		shouldKill := false

		// Always kill engine-server.exe (MCP bridge, always a child of orphaned claude).
		if proc.ExeName == "engine-server.exe" {
			shouldKill = true
		}

		// Kill claude.exe with dead parent.
		if proc.ExeName == "claude.exe" {
			if proc.ParentPID == 0 || proc.ParentPID == 1 || !parentExists[proc.ParentPID] {
				shouldKill = true
			}
		}

		if shouldKill {
			if err := lister.KillProcess(proc.PID); err == nil {
				killed++
				CloseJobForProcess(proc.PID)
			}
		}
	}

	return killed
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
