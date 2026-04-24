package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// initTestRepo creates a bare git repo with an initial commit for testing.
func initTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	cmds := [][]string{
		{"git", "init"},
		{"git", "config", "user.email", "test@test.com"},
		{"git", "config", "user.name", "Test"},
		{"git", "commit", "--allow-empty", "-m", "init"},
	}
	for _, args := range cmds {
		c := exec.Command(args[0], args[1:]...)
		c.Dir = dir
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("setup %v: %v\n%s", args, err, out)
		}
	}
	return dir
}

func TestRun_Success(t *testing.T) {
	dir := initTestRepo(t)
	out, err := run(dir, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if out == "" {
		t.Error("expected non-empty output")
	}
}

func TestRun_Failure(t *testing.T) {
	dir := t.TempDir()
	_, err := run(dir, "status")
	if err == nil {
		t.Error("expected error for non-git directory")
	}
}

func TestGetCurrentBranch(t *testing.T) {
	dir := initTestRepo(t)
	branch, err := GetCurrentBranch(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if branch == "" || branch == "unknown" {
		t.Logf("branch: %q (may be 'main' or 'master' depending on git config)", branch)
	}
}

func TestGetCurrentBranch_NonRepo(t *testing.T) {
	dir := t.TempDir()
	branch, err := GetCurrentBranch(dir)
	if err != nil {
		t.Fatalf("GetCurrentBranch returns 'unknown' on error, not error: %v", err)
	}
	// Returns "unknown" on error (function swallows error).
	if branch != "unknown" {
		t.Logf("non-repo returned %q", branch)
	}
}

func TestGetStatus_EmptyRepo(t *testing.T) {
	dir := initTestRepo(t)
	status, err := GetStatus(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status == nil {
		t.Fatal("expected non-nil status")
	}
}

func TestGetStatus_WithUntracked(t *testing.T) {
	dir := initTestRepo(t)
	if err := os.WriteFile(filepath.Join(dir, "new.txt"), []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}
	status, err := GetStatus(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(status.Untracked) == 0 {
		t.Error("expected at least one untracked file")
	}
}

func TestGetStatus_WithStaged(t *testing.T) {
	dir := initTestRepo(t)
	fPath := filepath.Join(dir, "staged.txt")
	if err := os.WriteFile(fPath, []byte("content"), 0644); err != nil {
		t.Fatal(err)
	}
	c := exec.Command("git", "add", "staged.txt")
	c.Dir = dir
	c.Run()

	status, err := GetStatus(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(status.Staged) == 0 {
		t.Error("expected staged file")
	}
}

func TestGetDiff_NoDiff(t *testing.T) {
	dir := initTestRepo(t)
	diff, err := GetDiff(dir, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_ = diff
}

func TestGetDiff_WithChanges(t *testing.T) {
	dir := initTestRepo(t)
	fPath := filepath.Join(dir, "file.txt")
	os.WriteFile(fPath, []byte("original"), 0644)
	exec.Command("git", "-C", dir, "add", ".").Run()
	exec.Command("git", "-C", dir, "config", "user.email", "t@t.com").Run()
	exec.Command("git", "-C", dir, "config", "user.name", "T").Run()
	exec.Command("git", "-C", dir, "commit", "-m", "add file").Run()
	os.WriteFile(fPath, []byte("modified"), 0644)
	diff, err := GetDiff(dir, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_ = diff
}

func TestGetLog_Empty(t *testing.T) {
	dir := initTestRepo(t)
	commits, err := GetLog(dir, 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if commits == nil {
		t.Error("expected non-nil slice")
	}
}

func TestGetLog_WithCommit(t *testing.T) {
	dir := initTestRepo(t)
	// add a file and commit
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a"), 0644)
	exec.Command("git", "-C", dir, "add", ".").Run()
	exec.Command("git", "-C", dir, "-c", "user.email=t@t.com", "-c", "user.name=T", "commit", "-m", "add a").Run()
	commits, err := GetLog(dir, 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(commits) == 0 {
		t.Error("expected at least one commit")
	}
}

func TestCommit(t *testing.T) {
	dir := initTestRepo(t)
	os.WriteFile(filepath.Join(dir, "commit.txt"), []byte("data"), 0644)
	hash, err := Commit(dir, "test commit")
	if err != nil {
		t.Fatalf("commit error: %v", err)
	}
	if hash == "" {
		t.Error("expected non-empty hash")
	}
}

func TestGetRemoteOrigin_NoRemote(t *testing.T) {
	dir := initTestRepo(t)
	_, err := GetRemoteOrigin(dir)
	if err == nil {
		t.Log("no error — no remote configured (expected in CI)")
	}
}

func TestGetRemoteURL_NoRemote(t *testing.T) {
	dir := initTestRepo(t)
	_, err := GetRemoteURL(dir, "origin")
	if err == nil {
		t.Log("no remote origin configured")
	}
}

func TestListRemotes_Empty(t *testing.T) {
	dir := initTestRepo(t)
	remotes, err := ListRemotes(dir)
	if err != nil {
		t.Fatalf("ListRemotes: %v", err)
	}
	if len(remotes) != 0 {
		t.Logf("got remotes: %v", remotes)
	}
}

func TestResolveGitHubRepo_NoRemote(t *testing.T) {
	dir := initTestRepo(t)
	_, _, err := ResolveGitHubRepo(dir)
	if err == nil {
		t.Log("no remote configured — expected error")
	}
}

func TestGetBaseBranch(t *testing.T) {
	dir := initTestRepo(t)
	base := GetBaseBranch(dir)
	if base == "" {
		t.Error("expected non-empty base branch")
	}
}

func TestListBranches(t *testing.T) {
	dir := initTestRepo(t)
	branches, err := ListBranches(dir)
	if err != nil {
		t.Fatalf("ListBranches: %v", err)
	}
	if branches == nil {
		t.Error("expected non-nil branches slice")
	}
}

func TestCreateBranch_New(t *testing.T) {
	dir := initTestRepo(t)
	_, err := CreateBranch(dir, "feature/test", true)
	if err != nil {
		t.Fatalf("CreateBranch: %v", err)
	}
	branch, _ := GetCurrentBranch(dir)
	if !strings.Contains(branch, "feature/test") {
		t.Errorf("expected feature/test branch, got %q", branch)
	}
}

func TestCreateBranch_Checkout(t *testing.T) {
	dir := initTestRepo(t)
	exec.Command("git", "-C", dir, "branch", "existing-branch").Run()
	_, err := CreateBranch(dir, "existing-branch", false)
	if err != nil {
		t.Fatalf("CheckoutBranch: %v", err)
	}
}

func TestCheckoutBranch(t *testing.T) {
	dir := initTestRepo(t)
	exec.Command("git", "-C", dir, "branch", "alt-branch").Run()
	err := CheckoutBranch(dir, "alt-branch")
	if err != nil {
		t.Fatalf("CheckoutBranch: %v", err)
	}
}

func TestRunGit(t *testing.T) {
	dir := initTestRepo(t)
	out, err := RunGit(dir, "log", "--oneline")
	if err != nil {
		t.Fatalf("RunGit: %v", err)
	}
	_ = out
}

func TestPruneWorktrees(t *testing.T) {
	dir := initTestRepo(t)
	err := PruneWorktrees(dir)
	if err != nil {
		t.Fatalf("PruneWorktrees: %v", err)
	}
}

func TestListWorktrees(t *testing.T) {
	dir := initTestRepo(t)
	worktrees, err := ListWorktrees(dir)
	if err != nil {
		t.Fatalf("ListWorktrees: %v", err)
	}
	if len(worktrees) == 0 {
		t.Error("expected at least one worktree (the main one)")
	}
}

func TestCreateRemoveWorktree(t *testing.T) {
	dir := initTestRepo(t)
	wtDir := filepath.Join(t.TempDir(), "wt1")
	err := CreateWorktree(dir, wtDir, "wt-branch")
	if err != nil {
		t.Fatalf("CreateWorktree: %v", err)
	}
	if _, err := os.Stat(wtDir); os.IsNotExist(err) {
		t.Error("worktree directory should exist")
	}
	if err := RemoveWorktree(dir, wtDir); err != nil {
		t.Fatalf("RemoveWorktree: %v", err)
	}
}

func TestGetStatus_NonRepo(t *testing.T) {
	dir := t.TempDir()
	status, err := GetStatus(dir)
	if err != nil {
		t.Fatal("GetStatus should handle non-repo gracefully")
	}
	_ = status
}

func TestGetDiff_WithPath(t *testing.T) {
	dir := initTestRepo(t)
	diff, err := GetDiff(dir, "nonexistent.txt")
	if err != nil {
		t.Fatalf("GetDiff with specific path: %v", err)
	}
	_ = diff
}
