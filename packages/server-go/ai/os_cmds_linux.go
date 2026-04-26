//go:build linux

package ai

import (
	"os/exec"
)

var openURLForOS = func(urlStr string) (*exec.Cmd, string) {
	return openURLCommand("xdg-open", urlStr), ""
}

var screenshotCmdForOS = func(outPath string) (*exec.Cmd, string) {
	return exec.Command("scrot", outPath), ""
}
