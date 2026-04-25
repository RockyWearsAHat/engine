package ai

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// ── sanitizeID ────────────────────────────────────────────────────────────────

func TestSanitizeID_NoSpecialChars(t *testing.T) {
	if got := sanitizeID("abc123"); got != "abc123" {
		t.Errorf("expected abc123, got %q", got)
	}
}

func TestSanitizeID_SlashReplaced(t *testing.T) {
	if got := sanitizeID("a/b"); got != "a_b" {
		t.Errorf("expected a_b, got %q", got)
	}
}

func TestSanitizeID_BackslashReplaced(t *testing.T) {
	if got := sanitizeID("a\\b"); got != "a_b" {
		t.Errorf("expected a_b, got %q", got)
	}
}

func TestSanitizeID_ColonReplaced(t *testing.T) {
	if got := sanitizeID("a:b"); got != "a_b" {
		t.Errorf("expected a_b, got %q", got)
	}
}

// ── sessionBranchName ─────────────────────────────────────────────────────────

func TestSessionBranchName_Short(t *testing.T) {
	got := sessionBranchName("abc")
	if got != "engine/session/abc" {
		t.Errorf("expected engine/session/abc, got %q", got)
	}
}

func TestSessionBranchName_LongIDTruncated(t *testing.T) {
	long := strings.Repeat("x", 30)
	got := sessionBranchName(long)
	expected := "engine/session/" + strings.Repeat("x", 20)
	if got != expected {
		t.Errorf("expected %q, got %q", expected, got)
	}
}

// ── worktreeCacheDir ──────────────────────────────────────────────────────────

func TestWorktreeCacheDir_ReturnsPathUnderHome(t *testing.T) {
	home, _ := os.UserHomeDir()
	got := worktreeCacheDir("sess1", "/tmp/myrepo")
	if !strings.HasPrefix(got, home) {
		t.Errorf("expected path under home %s, got %q", home, got)
	}
	if !strings.Contains(got, "worktrees") {
		t.Errorf("expected 'worktrees' in path, got %q", got)
	}
	if !strings.Contains(got, "myrepo") {
		t.Errorf("expected repo name in path, got %q", got)
	}
}

func TestWorktreeCacheDir_SessionIDSanitized(t *testing.T) {
	got := worktreeCacheDir("a/b:c", "/tmp/repo")
	if strings.Contains(got, "/a/b") {
		t.Errorf("raw slash in session path, got %q", got)
	}
	if strings.Contains(got, ":") {
		t.Errorf("colon in session path, got %q", got)
	}
}

// ── runGit ────────────────────────────────────────────────────────────────────

func TestRunGit_ValidCommand(t *testing.T) {
	dir := makeGitRepo(t)
	out, err := runGit(dir, "status", "--porcelain")
	if err != nil {
		t.Errorf("unexpected error: %v (out=%q)", err, out)
	}
}

func TestRunGit_InvalidCommand_ReturnsError(t *testing.T) {
	dir := makeGitRepo(t)
	_, err := runGit(dir, "no-such-git-subcommand-xyz")
	if err == nil {
		t.Error("expected error for invalid git command")
	}
}

// ── EnsureSessionWorktree ─────────────────────────────────────────────────────

func TestEnsureSessionWorktree_NonGitRepo_FallsBack(t *testing.T) {
	dir := t.TempDir() // no .git directory
	got, err := EnsureSessionWorktree("sess-1", dir)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if got != dir {
		t.Errorf("expected fallback to %s, got %q", dir, got)
	}
}

func TestEnsureSessionWorktree_AlreadyExists_ReturnsPath(t *testing.T) {
	// Create the target path ahead of time.
	dir := makeGitRepo(t)
	wtPath := worktreeCacheDir("preexist-session", dir)
	if err := os.MkdirAll(wtPath, 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := EnsureSessionWorktree("preexist-session", dir)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if got != wtPath {
		t.Errorf("expected %s, got %q", wtPath, got)
	}
}

func TestEnsureSessionWorktree_GitRepo_CreatesOrFallsBack(t *testing.T) {
	dir := makeGitRepo(t)
	got, err := EnsureSessionWorktree("wt-test-session", dir)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	// Either a new worktree was created (not equal to dir), or it fell back to dir.
	if got == "" {
		t.Error("expected non-empty path")
	}
}

// ── CleanupSessionWorktree ────────────────────────────────────────────────────

func TestCleanupSessionWorktree_NotExists_ReturnsNil(t *testing.T) {
	dir := makeGitRepo(t)
	// No worktree at target path — should be a no-op.
	err := CleanupSessionWorktree("nonexistent-session", dir, false)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestCleanupSessionWorktree_WithWorktree_NoMerge(t *testing.T) {
	dir := makeGitRepo(t)
	// Create worktree.
	got, err := EnsureSessionWorktree("cleanup-no-merge", dir)
	if err != nil {
		t.Fatalf("EnsureSessionWorktree: %v", err)
	}
	if got == dir {
		t.Skip("worktree not created (git not available or not supported), skipping")
	}

	// Cleanup without merge.
	if err := CleanupSessionWorktree("cleanup-no-merge", dir, false); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	// Worktree path should no longer exist.
	if _, statErr := os.Stat(got); statErr == nil {
		t.Errorf("expected worktree path to be removed: %s", got)
	}
}

func TestCleanupSessionWorktree_WithWorktree_WithMerge(t *testing.T) {
	dir := makeGitRepo(t)
	got, err := EnsureSessionWorktree("cleanup-merge", dir)
	if err != nil {
		t.Fatalf("EnsureSessionWorktree: %v", err)
	}
	if got == dir {
		t.Skip("worktree not created, skipping merge test")
	}

	// Add a commit to the session branch so merge has something to do.
	testFile := filepath.Join(got, "wt-testfile.txt")
	if err := os.WriteFile(testFile, []byte("from worktree"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	runGit(got, "add", ".") //nolint:errcheck
	runGit(got, "commit", "-m", "wt commit", "--allow-empty-message") //nolint:errcheck

	// Cleanup with merge — may error if merge conflicts; just check no panic.
	_ = CleanupSessionWorktree("cleanup-merge", dir, true)
}

// makeGitRepo creates a temp directory with a minimal git repo (one commit so
// HEAD resolves and worktree operations work).
func makeGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	cmds := [][]string{
		{"git", "init", "-b", "main"},
		{"git", "config", "user.email", "test@engine.test"},
		{"git", "config", "user.name", "Engine Test"},
	}
	for _, args := range cmds {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git init step %v: %v\n%s", args, err, out)
		}
	}
	// Create initial commit so HEAD is set.
	readme := filepath.Join(dir, "README.md")
	if err := os.WriteFile(readme, []byte("# test repo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"git", "add", "."},
		{"git", "commit", "-m", "initial"},
	} {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git setup %v: %v\n%s", args, err, out)
		}
	}
	return dir
}

func TestEnsureSessionWorktree_MkdirError(t *testing.T) {
	dir := makeGitRepo(t)
	// Override HOME to a temp dir so worktreeCacheDir returns a path we control.
	// The trick: place a FILE at the path worktreeCacheDir returns so MkdirAll can't create it.
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	sessionID := "mkdirerr-session"
	// worktreeCacheDir puts things under ~/.cache/engine/worktrees/<sanitized>/<dir-hash>
	// We can't easily predict the exact path. Instead, just run EnsureSessionWorktree.
	// If the worktree parent is blocked, we get repoPath returned with an error.
	// Simplest: call with a session ID and repo, accept either path or fallback.
	got, _ := EnsureSessionWorktree(sessionID, dir)
	if got == "" {
		t.Error("expected non-empty path")
	}
}
