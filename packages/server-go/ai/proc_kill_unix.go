//go:build !windows

package ai

import (
	"os/exec"
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
	// Negative pid = "the process group", per kill(2). setProcGroup made
	// this process its own group leader, so its pgid equals its pid.
	return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}
