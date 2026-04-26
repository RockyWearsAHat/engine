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

var browserNavigateFnForOS = func(urlStr string) (string, error) {
	return "", fmt.Errorf("browser_navigate not supported on %s", runtime.GOOS)
}

var browserReadPageFnForOS = func() (string, error) {
	return "", fmt.Errorf("browser_read_page not supported on %s", runtime.GOOS)
}

var browserClickFnForOS = func(x, y int) (string, error) {
	return "", fmt.Errorf("browser_click not supported on %s", runtime.GOOS)
}

var browserTypeFnForOS = func(text string) (string, error) {
	return "", fmt.Errorf("browser_type not supported on %s", runtime.GOOS)
}
