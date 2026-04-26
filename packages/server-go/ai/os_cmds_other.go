//go:build !darwin && !linux

package ai

import (
	"fmt"
	"os/exec"
	"runtime"
)

var openURLForOS = func(urlStr string) (*exec.Cmd, string) {
	return nil, fmt.Sprintf("open_url not supported on %s", runtime.GOOS)
}

var screenshotCmdForOS = func(outPath string) (*exec.Cmd, string) {
	return nil, fmt.Sprintf("screenshot not supported on %s", runtime.GOOS)
}
