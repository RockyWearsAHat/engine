//go:build linux

package ai

import (
	"fmt"
	"os/exec"
	"strings"
)

var openURLForOS = func(urlStr string) (*exec.Cmd, string) {
	return openURLCommand("xdg-open", urlStr), ""
}

var screenshotCmdForOS = func(outPath string) (*exec.Cmd, string) {
	return exec.Command("scrot", outPath), ""
}

var browserNavigateFnForOS = func(urlStr string) (string, error) {
	cmd := exec.Command("xdg-open", urlStr)
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("browser_navigate: %v", err)
	}
	return "Navigated to: " + urlStr, nil
}

var browserReadPageFnForOS = func() (string, error) {
	return "", fmt.Errorf("browser_read_page not supported on Linux without additional tools")
}

var browserClickFnForOS = func(x, y int) (string, error) {
	if err := exec.Command("xdotool", "mousemove", fmt.Sprintf("%d", x), fmt.Sprintf("%d", y)).Run(); err != nil {
		return "", fmt.Errorf("browser_click mousemove: %v", err)
	}
	if err := exec.Command("xdotool", "click", "1").Run(); err != nil {
		return "", fmt.Errorf("browser_click: %v", err)
	}
	return fmt.Sprintf("Clicked at (%d, %d)", x, y), nil
}

var browserTypeFnForOS = func(text string) (string, error) {
	cmd := exec.Command("xdotool", "type", "--clearmodifiers", "--", text)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("browser_type: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return "Typed text in browser", nil
}
