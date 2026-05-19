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

var filepathRelFn = filepath.Rel

// escapesWorkspace returns true when a shell command attempts to leave the
// project root, regardless of how it is written. This is awareness metadata,
// not an autonomous-session restriction.
func escapesWorkspace(projectPath, command string) bool {
	root := workspaceRoot(projectPath)
	lower := " " + strings.ToLower(strings.ReplaceAll(command, "\n", " ")) + " "

	// Any ../ traversal.
	if strings.Contains(lower, "../") || strings.Contains(lower, `..`+string(os.PathSeparator)) {
		return true
	}

	// cd with various destinations that escape.
	cdPrefixes := []string{" cd ", " cd\t", " cd\n", " cd;", " cd&&", " cd||"}
	for _, p := range cdPrefixes {
		if _, after, found := strings.Cut(lower, p); found {
			rest := strings.TrimSpace(after)
			dest := strings.Fields(rest)
			if len(dest) == 0 {
				// bare cd goes to $HOME.
				return true
			}
			d := dest[0]
			if strings.HasPrefix(d, "~") || strings.HasPrefix(d, "-") || strings.HasPrefix(d, "..") {
				return true
			}
			if strings.HasPrefix(d, "/") && !strings.HasPrefix(d, root) {
				return true
			}
		}
	}

	// Any absolute path token that lives outside the project root.
	for token := range strings.FieldsSeq(command) {
		clean := strings.Trim(token, `'"`)
		if strings.HasPrefix(clean, "/") && !strings.HasPrefix(clean, root) {
			return true
		}
	}

	return false
}

func isOutsideWorkspace(projectPath, targetPath string) bool {
	root := workspaceRoot(projectPath)
	abs := filepath.Clean(targetPath)
	rel, err := filepathRelFn(root, abs)
	if err != nil {
		return true
	}
	return rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}

// resolveAutonomousShellDirectory resolves a shell cwd without treating paths
// outside the project as errors. Autonomous agents are rooted in the project by
// default but may intentionally reach outside it when the task demands it.
func resolveAutonomousShellDirectory(projectPath, targetPath string) (string, string, error) {
	root := workspaceRoot(projectPath)
	candidate := strings.TrimSpace(targetPath)
	if candidate == "" {
		return root, "", nil
	}
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(root, candidate)
	}
	abs := filepath.Clean(candidate)
	if isOutsideWorkspace(projectPath, abs) {
		return abs, "AWARENESS: shell cwd is outside the project root (" + root + "). This is allowed when needed, but return to the project root for normal build/test work.", nil
	}
	return abs, "", nil
}

func workspaceRoot(projectPath string) string {
	abs, _ := filepath.Abs(projectPath)
	return filepath.Clean(abs)
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

	absoluteTarget := filepath.Clean(candidate)

	rel, _ := filepath.Rel(root, absoluteTarget)
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
	rel, _ := filepath.Rel(workspaceRoot(projectPath), absoluteTarget)
	return rel, nil
}

func requiresShellApproval(projectPath, command string) (title, message string, ok bool) {
	trimmed := strings.TrimSpace(command)
	if trimmed == "" {
		return "", "", false
	}

	if escapesWorkspace(projectPath, trimmed) {
		return "Approve workspace escape", "This shell command attempts to leave the project root. Engine blocks that unless you explicitly approve it.", true
	}

	lower := " " + strings.ToLower(trimmed) + " "
	for _, marker := range riskyShellMarkers {
		if strings.Contains(lower, marker) {
			return "Approve risky shell command", "Engine flagged this shell command as destructive or state-changing. Review it before allowing the assistant to run it.", true
		}
	}
	return "", "", false
}

// autonomousShellAwareness returns a non-empty note when an autonomous shell
// command leaves the project root or performs risky work. It never blocks the
// command; Engine trusts the AI but makes the working context explicit.
func autonomousShellAwareness(projectPath, command string) string {
	trimmed := strings.TrimSpace(command)
	if trimmed == "" {
		return ""
	}
	if escapesWorkspace(projectPath, trimmed) {
		return "AWARENESS: this command leaves the project root (" + workspaceRoot(projectPath) + "). That is allowed when necessary, but normal project work should happen from the project root."
	}
	lower := " " + strings.ToLower(trimmed) + " "
	for _, marker := range riskyShellMarkers {
		if strings.Contains(lower, marker) {
			return "AWARENESS: this command contains a risky operation (matched: " + strings.TrimSpace(marker) + "). Proceed only if it is required for the task and preserve project state."
		}
	}
	return ""
}

