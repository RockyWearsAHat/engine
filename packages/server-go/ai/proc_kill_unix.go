//go:build !windows

package ai

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

// setProcGroup puts the child in its own process group so killProcessTree can
// signal the whole tree (the CLI plus its engine-server.exe MCP bridge child
// and anything either of those spawned) rather than just the immediate PID.
// Without this, cmd.Cancel/CommandContext kills only `claude` on timeout or
// cancellation and its children are orphaned to keep running and holding
// memory — exactly what turned 3 in-flight tasks into 14 live processes.
func setProcGroup(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

// killProcessTree signals the whole process group started by setProcGroup.
// Safe to call even if the process never started or already exited.
func killProcessTree(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	return KillPIDTree(cmd.Process.Pid)
}

// KillPIDTree kills the process group led by pid. Exported so the task api
// can reap orphaned session PIDs recovered from tasks.json on restart, not
// just PIDs this same process just spawned. Safe to call on an already-dead
// pid (ESRCH is not an error worth surfacing).
func KillPIDTree(pid int) error {
	if pid <= 0 {
		return nil
	}
	// Negative pid = "the process group", per kill(2). setProcGroup makes a
	// freshly spawned `claude` its own group leader, so its pgid equals its
	// pid — true for both the live-spawn path and a PID recovered from disk.
	if err := syscall.Kill(-pid, syscall.SIGKILL); err != nil && err != syscall.ESRCH {
		return err
	}
	return nil
}

// freeCommitMB reports free-ish memory in MB for the spawn-accounting log:
// /proc/meminfo's MemAvailable when present (the kernel's own "could commit
// this much without swapping" estimate), else -1 when unavailable (a
// platform without /proc, e.g. macOS dev boxes — not worth a full sysctl
// wrapper just for a diagnostic log line).
func freeCommitMB() int64 {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return -1
	}
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(line, "MemAvailable:") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return -1
		}
		kb, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil {
			return -1
		}
		return kb / 1024
	}
	return -1
}
