//go:build darwin

package ai

import (
	"os/exec"
)

var openURLForOS = func(urlStr string) (*exec.Cmd, string) {
	return openURLCommand("open", urlStr), ""
}

var screenshotCmdForOS = func(outPath string) (*exec.Cmd, string) {
	return exec.Command("screencapture", "-x", outPath), ""
}
