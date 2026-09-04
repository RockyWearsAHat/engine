//go:build windows

package ai

import (
	"os/exec"
	"strconv"
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
	return exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid)).Run()
}
