package ai

import "sync"

// sessionRegistry tracks every live `claude` CLI process this engine has
// spawned, keyed by the task-api id that dispatched it (empty key for
// non-task-mode chats — those are not restart-reaped, matching prior
// behaviour).
//
// It exists for two reasons:
//  1. Orphan reaping on restart: tasks.json persists which PIDs a running
//     task owned, so on reload the task api can tree-kill them before
//     marking the row failed+lost, instead of leaving them to run (and hold
//     memory) forever.
//  2. Single-live-session accounting: task mode must never have two
//     `claude` sessions alive for one task at once (a plan session lingering
//     into the execute phase). LiveSessionCount/LiveSessionPIDs let
//     claudecode.go and the task api both see the truth instead of assuming
//     it.
var (
	sessionRegistryMu sync.Mutex
	// taskID -> pid -> phase ("plan" | "execute")
	sessionRegistry = map[string]map[int]string{}
)

// RegisterSession records a spawned `claude` PID for taskID/phase. Call the
// moment cmd.Start() succeeds.
func RegisterSession(taskID string, pid int, phase string) {
	if pid <= 0 {
		return
	}
	sessionRegistryMu.Lock()
	defer sessionRegistryMu.Unlock()
	m, ok := sessionRegistry[taskID]
	if !ok {
		m = map[int]string{}
		sessionRegistry[taskID] = m
	}
	m[pid] = phase
}

// UnregisterSession removes a PID once its process has been waited on (exited
// or killed). Also closes any Windows job object handle associated with the PID.
// Safe to call even if it was never registered.
func UnregisterSession(taskID string, pid int) {
	sessionRegistryMu.Lock()
	defer sessionRegistryMu.Unlock()
	m, ok := sessionRegistry[taskID]
	if !ok {
		goto cleanup_job
	}
	delete(m, pid)
	if len(m) == 0 {
		delete(sessionRegistry, taskID)
	}

cleanup_job:
	// Close any job object associated with this PID (Windows only; no-op on Unix).
	CloseJobForProcess(pid)
}

// LiveSessionPIDs returns the PIDs currently registered for taskID (plan
// and/or execute). Used by task_api.go to persist SessionPIDs into
// tasks.json and to reap them on restart.
func LiveSessionPIDs(taskID string) []int {
	sessionRegistryMu.Lock()
	defer sessionRegistryMu.Unlock()
	m := sessionRegistry[taskID]
	pids := make([]int, 0, len(m))
	for pid := range m {
		pids = append(pids, pid)
	}
	return pids
}

// LiveSessionCount returns how many `claude` sessions are live across every
// task right now — the number the spawn/exit accounting log reports as
// live=.
func LiveSessionCount() int {
	sessionRegistryMu.Lock()
	defer sessionRegistryMu.Unlock()
	n := 0
	for _, m := range sessionRegistry {
		n += len(m)
	}
	return n
}

// LiveTaskSessionCount returns how many `claude` sessions are currently live
// for a specific task. Used for per-task accounting in spawn logs.
func LiveTaskSessionCount(taskID string) int {
	sessionRegistryMu.Lock()
	defer sessionRegistryMu.Unlock()
	return len(sessionRegistry[taskID])
}

// LiveTreeCount returns the count of distinct process trees (each tree has one
// root PID — the `claude` process itself). Used for process pile-up visibility
// in spawn/exit logs.
func LiveTreeCount() int {
	sessionRegistryMu.Lock()
	defer sessionRegistryMu.Unlock()
	// Each task has one or more sessions (plan, execute); each session root
	// represents one process tree. For simplicity and given that most tasks
	// run plan+execute sequentially (plan completes before execute starts),
	// we count the number of root PIDs across all tasks. In the common case
	// this equals LiveSessionCount; the separation is for visibility.
	n := 0
	for _, m := range sessionRegistry {
		n += len(m)
	}
	return n
}
