package ai

import (
	"strings"
	"testing"
)

// ── workspaceRoot / resolveWorkspacePath ──────────────────────────────────────

func TestWorkspaceRoot_AbsPath(t *testing.T) {
	root := workspaceRoot("/tmp/myproject")
	if root == "" {
		t.Error("expected non-empty workspace root")
	}
}

func TestResolveWorkspacePath_RelativePath(t *testing.T) {
	dir := t.TempDir()
	path, err := resolveWorkspacePath(dir, "src/main.go")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(path, "src/main.go") {
		t.Errorf("expected path to contain src/main.go, got %q", path)
	}
}

func TestResolveWorkspacePath_AbsolutePath(t *testing.T) {
	dir := t.TempDir()
	path, err := resolveWorkspacePath(dir, dir+"/src/file.go")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if path == "" {
		t.Error("expected non-empty path")
	}
}

func TestResolveWorkspacePath_EmptyPath(t *testing.T) {
	dir := t.TempDir()
	_, err := resolveWorkspacePath(dir, "")
	if err == nil {
		t.Error("expected error for empty path")
	}
}

func TestResolveWorkspacePath_EscapeWorkspace(t *testing.T) {
	dir := t.TempDir()
	_, err := resolveWorkspacePath(dir, "../../../etc/passwd")
	if err == nil {
		t.Error("expected error for path escaping workspace")
	}
}

func TestResolveWorkspaceDirectory_Empty(t *testing.T) {
	dir := t.TempDir()
	path, err := resolveWorkspaceDirectory(dir, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if path == "" {
		t.Error("expected non-empty directory path")
	}
}

func TestResolveWorkspaceDirectory_SubDir(t *testing.T) {
	dir := t.TempDir()
	path, err := resolveWorkspaceDirectory(dir, "src")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(path, "src") {
		t.Errorf("expected path to contain src, got %q", path)
	}
}

func TestWorkspaceRelativePath(t *testing.T) {
	dir := t.TempDir()
	rel, err := workspaceRelativePath(dir, "src/main.go")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rel != "src/main.go" {
		t.Errorf("got %q, want src/main.go", rel)
	}
}

func TestWorkspaceRelativePath_EscapeWorkspace(t *testing.T) {
	dir := t.TempDir()
	_, err := workspaceRelativePath(dir, "../../../etc/passwd")
	if err == nil {
		t.Error("expected error for path escaping workspace")
	}
}

// ── requiresShellApproval ─────────────────────────────────────────────────────

func TestRequiresShellApproval_SafeCommand(t *testing.T) {
	_, _, ok := requiresShellApproval("/project", "ls -la")
	if ok {
		t.Error("ls -la should not require approval")
	}
}

func TestRequiresShellApproval_EmptyCommand(t *testing.T) {
	_, _, ok := requiresShellApproval("/project", "")
	if ok {
		t.Error("empty command should not require approval")
	}
}

func TestRequiresShellApproval_WorkspaceEscape(t *testing.T) {
	_, _, ok := requiresShellApproval("/project", "cat ../secrets")
	if !ok {
		t.Error("cd ../ should require approval")
	}
}

func TestRequiresShellApproval_RiskyCommand_Rm(t *testing.T) {
	_, _, ok := requiresShellApproval("/project", "find . -name '*.tmp' -exec rm -f {} +")
	if !ok {
		t.Error("rm command should require approval")
	}
}

func TestRequiresShellApproval_RiskyCommand_Sudo(t *testing.T) {
	_, _, ok := requiresShellApproval("/project", " sudo apt install something")
	if !ok {
		t.Error("sudo should require approval")
	}
}

func TestRequiresShellApproval_RiskyCommand_GitReset(t *testing.T) {
	_, _, ok := requiresShellApproval("/project", "git reset --hard HEAD")
	if !ok {
		t.Error("git reset should require approval")
	}
}
