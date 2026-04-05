package ai

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

var riskyShellMarkers = []string{
	" rm ", "rm -", " sudo ", " chmod ", " chown ", " git reset", " git clean",
	" git checkout ", " git switch ", " git restore ", " git rebase", " git push",
	" git cherry-pick", " git stash drop", " git branch -d", " git branch -D",
	" git tag -d", " mv ", " dd ", " mkfs", " shutdown", " reboot", " kill ",
	" pkill ", "| sh", "| bash", " tee ", " > ", " >> ", " sed -i", " perl -i",
}

func workspaceRoot(projectPath string) string {
	if abs, err := filepath.Abs(projectPath); err == nil {
		return filepath.Clean(abs)
	}
	return filepath.Clean(projectPath)
}

func resolveWorkspacePath(projectPath, targetPath string) (string, error) {
	if strings.TrimSpace(targetPath) == "" {
		return "", fmt.Errorf("path required")
	}

	root := workspaceRoot(projectPath)
	candidate := strings.TrimSpace(targetPath)
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(root, candidate)
	}

	absoluteTarget, err := filepath.Abs(candidate)
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", targetPath, err)
	}
	absoluteTarget = filepath.Clean(absoluteTarget)

	rel, err := filepath.Rel(root, absoluteTarget)
	if err != nil {
		return "", fmt.Errorf("resolve %s relative to workspace: %w", targetPath, err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("path %s is outside the current workspace (%s)", targetPath, root)
	}
	return absoluteTarget, nil
}

func resolveWorkspaceDirectory(projectPath, targetPath string) (string, error) {
	if strings.TrimSpace(targetPath) == "" {
		return workspaceRoot(projectPath), nil
	}
	return resolveWorkspacePath(projectPath, targetPath)
}

func workspaceRelativePath(projectPath, targetPath string) (string, error) {
	absoluteTarget, err := resolveWorkspacePath(projectPath, targetPath)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(workspaceRoot(projectPath), absoluteTarget)
	if err != nil {
		return "", fmt.Errorf("resolve %s relative to workspace: %w", targetPath, err)
	}
	return rel, nil
}

func requiresShellApproval(projectPath, command string) (title, message string, ok bool) {
	trimmed := strings.TrimSpace(command)
	if trimmed == "" {
		return "", "", false
	}

	lower := " " + strings.ToLower(trimmed) + " "
	if strings.Contains(lower, "../") || strings.Contains(lower, " cd ..") {
		return "Approve workspace escape", "This shell command appears to leave the current workspace. Engine blocks that unless you explicitly approve it.", true
	}

	root := workspaceRoot(projectPath)
	if strings.Contains(trimmed, "/") && !strings.Contains(trimmed, root) {
		for _, token := range strings.Fields(trimmed) {
			if strings.HasPrefix(token, "/") && !strings.HasPrefix(token, root) {
				return "Approve external path access", "This shell command references an absolute path outside the current workspace.", true
			}
		}
	}

	for _, marker := range riskyShellMarkers {
		if strings.Contains(lower, marker) {
			return "Approve risky shell command", "Engine flagged this shell command as destructive or state-changing. Review it before allowing the assistant to run it.", true
		}
	}

	return "", "", false
}
