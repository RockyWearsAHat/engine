//go:build windows

package ai

import (
	"os/exec"
	"strconv"
	"strings"
)

// setProcGroup is a no-op on Windows: there is no pgid-style kill(2) target
// to opt into. killProcessTree uses taskkill /T instead, which walks the
// process tree by parent PID regardless of process-group membership.
func setProcGroup(cmd *exec.Cmd) {}

// killProcessTree kills the CLI process and everything it spawned (the
// engine-server.exe MCP bridge child included) via `taskkill /T /F /PID`.
// Without /T, TerminateProcess (what cmd.Process.Kill()/CommandContext use by
// default) only ever kills the immediate PID, orphaning the MCP child to run
// on and hold its ~memory indefinitely.
func killProcessTree(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	return KillPIDTree(cmd.Process.Pid)
}

// KillPIDTree kills the tree rooted at pid via taskkill /T /F. Exported so
// the task api can reap orphaned session PIDs recovered from tasks.json on
// restart, not just PIDs this same process just spawned. taskkill exits
// non-zero when the PID is already gone (code 128) — not an error worth
// surfacing, the goal (nothing left running) is already met.
func KillPIDTree(pid int) error {
	if pid <= 0 {
		return nil
	}
	if err := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(pid)).Run(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == 128 {
			return nil
		}
		return err
	}
	return nil
}

// freeCommitMB reports free virtual/commit memory in MB for the
// spawn-accounting log, via `wmic OS get FreeVirtualMemory` (KB). Returns -1
// on any failure so the log line still writes with an honest "unknown"
// rather than blocking on a diagnostic.
func freeCommitMB() int64 {
	out, err := exec.Command("wmic", "OS", "get", "FreeVirtualMemory").Output()
	if err != nil {
		return -1
	}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line == "FreeVirtualMemory" {
			continue
		}
		kb, err := strconv.ParseInt(line, 10, 64)
		if err != nil {
			continue
		}
		return kb / 1024
	}
	return -1
}
