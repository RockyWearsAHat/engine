//go:build darwin

package ai

import (
	"fmt"
	"os/exec"
	"strings"
)

var openURLForOS = func(urlStr string) (*exec.Cmd, string) {
	return openURLCommand("open", urlStr), ""
}

var screenshotCmdForOS = func(outPath string) (*exec.Cmd, string) {
	return exec.Command("screencapture", "-x", outPath), ""
}

var browserNavigateFnForOS = func(urlStr string) (string, error) {
	escaped := strings.ReplaceAll(urlStr, `"`, `\"`)
	script := fmt.Sprintf(`tell application "Google Chrome"
	activate
	if (count of windows) = 0 then make new window
	set URL of active tab of front window to "%s"
end tell`, escaped)
	cmd := exec.Command("osascript", "-e", script)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("browser_navigate: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return "Navigated Chrome to: " + urlStr, nil
}

var browserReadPageFnForOS = func() (string, error) {
	script := `tell application "Google Chrome"
	execute active tab of front window javascript "document.body.innerText.substring(0,8000)"
end tell`
	cmd := exec.Command("osascript", "-e", script)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("browser_read_page: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

var browserClickFnForOS = func(x, y int) (string, error) {
	script := fmt.Sprintf(`tell application "System Events" to click at {%d, %d}`, x, y)
	cmd := exec.Command("osascript", "-e", script)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("browser_click: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return fmt.Sprintf("Clicked at (%d, %d)", x, y), nil
}

var browserTypeFnForOS = func(text string) (string, error) {
	escaped := strings.ReplaceAll(strings.ReplaceAll(text, `\`, `\\`), `"`, `\"`)
	script := fmt.Sprintf(`tell application "System Events" to keystroke "%s"`, escaped)
	cmd := exec.Command("osascript", "-e", script)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("browser_type: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return "Typed text in browser", nil
}
