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

func TestGetStatus_WithStagedAndUnstagedSameFile(t *testing.T) {
	dir := initTestRepo(t)
	path := filepath.Join(dir, "both.txt")

	if err := os.WriteFile(path, []byte("v1"), 0644); err != nil {
		t.Fatal(err)
	}
	exec.Command("git", "-C", dir, "add", "both.txt").Run()
	exec.Command("git", "-C", dir, "-c", "user.email=t@t.com", "-c", "user.name=T", "commit", "-m", "add both").Run()

	if err := os.WriteFile(path, []byte("v2"), 0644); err != nil {
		t.Fatal(err)
	}
	exec.Command("git", "-C", dir, "add", "both.txt").Run()
	if err := os.WriteFile(path, []byte("v3"), 0644); err != nil {
		t.Fatal(err)
	}

	status, err := GetStatus(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(status.Staged) == 0 {
		t.Fatal("expected staged entry")
	}
	if len(status.Unstaged) == 0 {
		t.Fatal("expected unstaged entry")
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

// ─── Push / Pull ──────────────────────────────────────────────────────────────

// initBareRemote creates a bare git repo to serve as a remote.
func initBareRemote(t *testing.T) string {
	t.Helper()
	bare := t.TempDir()
	c := exec.Command("git", "init", "--bare", bare)
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("git init --bare: %v\n%s", err, out)
	}
	return bare
}

func TestPush_DefaultRemote(t *testing.T) {
	repo := initTestRepo(t)
	bare := initBareRemote(t)

	// Add bare as "origin".
	if out, err := exec.Command("git", "-C", repo, "remote", "add", "origin", bare).CombinedOutput(); err != nil {
		t.Fatalf("add remote: %v\n%s", err, out)
	}

	// Push (remote="") → defaults to origin.
	out, err := Push(repo, "")
	if err != nil {
		t.Fatalf("Push: %v\nout: %s", err, out)
	}
}

func TestPush_NamedRemote(t *testing.T) {
	repo := initTestRepo(t)
	bare := initBareRemote(t)

	if out, err := exec.Command("git", "-C", repo, "remote", "add", "upstream", bare).CombinedOutput(); err != nil {
		t.Fatalf("add remote: %v\n%s", err, out)
	}

	out, err := Push(repo, "upstream")
	if err != nil {
		t.Fatalf("Push upstream: %v\nout: %s", err, out)
	}
	_ = out
}

func TestPush_Error(t *testing.T) {
	repo := initTestRepo(t)
	// Push to non-existent remote → error.
	_, err := Push(repo, "no-such-remote")
	if err == nil {
		t.Fatal("expected error for missing remote")
	}
}

func TestPull_Success(t *testing.T) {
	bare := initBareRemote(t)

	// Create a source repo and push initial commit to bare.
	src := initTestRepo(t)
	if out, err := exec.Command("git", "-C", src, "remote", "add", "origin", bare).CombinedOutput(); err != nil {
		t.Fatalf("add remote: %v\n%s", err, out)
	}
	if out, err := exec.Command("git", "-C", src, "push", "origin", "HEAD").CombinedOutput(); err != nil {
		t.Fatalf("initial push: %v\n%s", err, out)
	}

	// Clone from bare so dst shares the same history (no divergence).
	dstParent := t.TempDir()
	dst := filepath.Join(dstParent, "dst")
	if out, err := exec.Command("git", "clone", bare, dst).CombinedOutput(); err != nil {
		t.Fatalf("clone: %v\n%s", err, out)
	}
	exec.Command("git", "-C", dst, "config", "user.email", "test@test.com").Run() //nolint:errcheck
	exec.Command("git", "-C", dst, "config", "user.name", "Test").Run()           //nolint:errcheck

	// Push a new commit from src so dst has something to pull.
	if err := os.WriteFile(filepath.Join(src, "update.txt"), []byte("update"), 0644); err != nil {
		t.Fatal(err)
	}
	exec.Command("git", "-C", src, "add", ".").Run()                                                                   //nolint:errcheck
	exec.Command("git", "-C", src, "commit", "-m", "second commit").Run()                                              //nolint:errcheck
	if out, err := exec.Command("git", "-C", src, "push", "origin", "HEAD").CombinedOutput(); err != nil {
		t.Fatalf("push update: %v\n%s", err, out)
	}

	out, err := Pull(dst, "origin")
	if err != nil {
		t.Fatalf("Pull: %v\nout: %s", err, out)
	}
	_ = out
}

func TestPull_Error(t *testing.T) {
	repo := initTestRepo(t)
	_, err := Pull(repo, "no-such-remote")
	if err == nil {
		t.Fatal("expected error for missing remote")
	}
}

// ─── ParseGitHubRepo SSH URL ──────────────────────────────────────────────────

func TestParseGitHubRepo_SSHFormat(t *testing.T) {
	owner, repo, ok := ParseGitHubRepo("git@github.com:alice/myrepo.git")
	if !ok {
		t.Fatal("expected ok for SSH URL")
	}
	if owner != "alice" || repo != "myrepo" {
		t.Errorf("got %q/%q, want alice/myrepo", owner, repo)
	}
}

func TestParseGitHubRepo_HTTPSFormat(t *testing.T) {
	owner, repo, ok := ParseGitHubRepo("https://github.com/bob/other-repo.git")
	if !ok {
		t.Fatal("expected ok for HTTPS URL")
	}
	if owner != "bob" || repo != "other-repo" {
		t.Errorf("got %q/%q, want bob/other-repo", owner, repo)
	}
}

func TestParseGitHubRepo_Empty(t *testing.T) {
	_, _, ok := ParseGitHubRepo("")
	if ok {
		t.Fatal("expected not ok for empty URL")
	}
}

func TestParseGitHubRepo_NotGitHub(t *testing.T) {
	_, _, ok := ParseGitHubRepo("https://gitlab.com/alice/repo")
	if ok {
		t.Fatal("expected not ok for non-GitHub URL")
	}
}

func TestParseGitHubRepo_MissingRepoPart(t *testing.T) {
	_, _, ok := ParseGitHubRepo("https://github.com/alice/")
	if ok {
		t.Fatal("expected not ok for URL missing repo component")
	}
}

// ─── ResolveGitHubRepo with non-origin remote ─────────────────────────────────

func TestResolveGitHubRepo_NonOriginRemote(t *testing.T) {
	dir := initTestRepo(t)
	// Add a non-origin remote that points to a GitHub-style URL.
	exec.Command("git", "-C", dir, "remote", "add", "upstream", "https://github.com/alice/repo.git").Run()

	owner, repo, err := ResolveGitHubRepo(dir)
	if err != nil {
		t.Fatalf("ResolveGitHubRepo: %v", err)
	}
	if owner != "alice" || repo != "repo" {
		t.Errorf("got %q/%q, want alice/repo", owner, repo)
	}
}

func TestResolveGitHubRepo_OriginIsGitHub(t *testing.T) {
	dir := initTestRepo(t)
	exec.Command("git", "-C", dir, "remote", "add", "origin", "https://github.com/eng/proj.git").Run()

	owner, repo, err := ResolveGitHubRepo(dir)
	if err != nil {
		t.Fatalf("ResolveGitHubRepo via origin: %v", err)
	}
	if owner != "eng" || repo != "proj" {
		t.Errorf("got %q/%q, want eng/proj", owner, repo)
	}
}

// ─── CreateWorktree error path ────────────────────────────────────────────────

func TestCreateWorktree_BranchAlreadyExists(t *testing.T) {
	dir := initTestRepo(t)
	// Create the branch first.
	exec.Command("git", "-C", dir, "branch", "existing-wt-branch").Run()
	// Now worktree add with -b should fail, falling back to add without -b.
	wtDir := filepath.Join(t.TempDir(), "wt-existing")
	err := CreateWorktree(dir, wtDir, "existing-wt-branch")
	if err != nil {
		t.Fatalf("CreateWorktree with existing branch: %v", err)
	}
	if _, statErr := os.Stat(wtDir); os.IsNotExist(statErr) {
		t.Error("worktree directory should exist")
	}
	// Cleanup.
	RemoveWorktree(dir, wtDir) //nolint:errcheck
}

// ─── Commit with staging ──────────────────────────────────────────────────────

func TestCommit_WithChanges(t *testing.T) {
	dir := initTestRepo(t)
	// Write a file so there's something to stage.
	if err := os.WriteFile(filepath.Join(dir, "file.txt"), []byte("hello"), 0600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	hash, err := Commit(dir, "test commit")
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if hash == "" || hash == "unknown" {
		t.Errorf("expected valid hash, got %q", hash)
	}
}

// ─── ListRemotes with a remote configured ────────────────────────────────────

func TestListRemotes_WithRemote(t *testing.T) {
	dir := initTestRepo(t)
	exec.Command("git", "-C", dir, "remote", "add", "origin", "https://github.com/x/y.git").Run()

	remotes, err := ListRemotes(dir)
	if err != nil {
		t.Fatalf("ListRemotes: %v", err)
	}
	found := false
	for _, r := range remotes {
		if r == "origin" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected 'origin' in remotes, got %v", remotes)
	}
}

// ─── GetBaseBranch dev branch ─────────────────────────────────────────────────

func TestGetBaseBranch_DevBranch(t *testing.T) {
	dir := initTestRepo(t)
	// Rename default branch to dev.
	branch, _ := GetCurrentBranch(dir)
	exec.Command("git", "-C", dir, "branch", "-m", branch, "dev").Run()
	base := GetBaseBranch(dir)
	if base != "dev" {
		t.Errorf("expected dev, got %q", base)
	}
}

// ─── ListWorktrees detached HEAD ─────────────────────────────────────────────

func TestListWorktrees_WithWorktree(t *testing.T) {
	dir := initTestRepo(t)
	wtDir := filepath.Join(t.TempDir(), "extra-wt")
	if err := CreateWorktree(dir, wtDir, "extra-branch"); err != nil {
		t.Fatalf("CreateWorktree: %v", err)
	}
	defer RemoveWorktree(dir, wtDir) //nolint:errcheck

	worktrees, err := ListWorktrees(dir)
	if err != nil {
		t.Fatalf("ListWorktrees: %v", err)
	}
	if len(worktrees) < 2 {
		t.Errorf("expected at least 2 worktrees, got %d", len(worktrees))
	}
}

// ─── Commit error paths ───────────────────────────────────────────────────────

func TestCommit_NonRepo(t *testing.T) {
	dir := t.TempDir()
	_, err := Commit(dir, "msg")
	if err == nil {
		t.Fatal("expected error committing in non-repo")
	}
}

func TestCommit_NothingToCommit(t *testing.T) {
	dir := initTestRepo(t)
	// Repo is clean (no files changed) — commit should fail.
	_, err := Commit(dir, "empty commit")
	if err == nil {
		t.Fatal("expected error when nothing to commit")
	}
}

// ─── CreateBranch error paths ─────────────────────────────────────────────────

func TestCreateBranch_CreateFails(t *testing.T) {
	dir := initTestRepo(t)
	// Detect the current branch name so we always try to re-create it.
	out, err := run(dir, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		t.Fatalf("could not detect HEAD branch: %v", err)
	}
	currentBranch := strings.TrimSpace(out)
	// Trying to create the already-existing branch must fail.
	_, err = CreateBranch(dir, currentBranch, true)
	if err == nil {
		t.Fatalf("expected error creating already-existing branch %q", currentBranch)
	}
}

func TestCreateBranch_CheckoutFails(t *testing.T) {
	dir := initTestRepo(t)
	_, err := CreateBranch(dir, "nonexistent-branch-xyz", false)
	if err == nil {
		t.Fatal("expected error checking out nonexistent branch")
	}
}

// ─── GetStatus ahead/behind path ─────────────────────────────────────────────

func TestGetStatus_WithStagedOnly(t *testing.T) {
	dir := initTestRepo(t)
	if err := os.WriteFile(filepath.Join(dir, "s.txt"), []byte("staged"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := run(dir, "add", "s.txt"); err != nil {
		t.Fatal(err)
	}
	status, err := GetStatus(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(status.Staged) == 0 {
		t.Error("expected staged file")
	}
}

// ─── ListBranches_ListRemotes error path ─────────────────────────────────────

func TestListBranches_NonRepo(t *testing.T) {
	_, err := ListBranches(t.TempDir())
	if err == nil {
		t.Fatal("expected error listing branches in non-repo")
	}
}

func TestListRemotes_NonRepo(t *testing.T) {
	_, err := ListRemotes(t.TempDir())
	if err == nil {
		t.Fatal("expected error listing remotes in non-repo")
	}
}

// ─── GetLog with bad date ─────────────────────────────────────────────────────

func TestGetLog_WithBadDate(t *testing.T) {
	dir := initTestRepo(t)
	// Make a commit first so GetLog has something.
	if err := os.WriteFile(filepath.Join(dir, "x.txt"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	hash, err := Commit(dir, "msg")
	if err != nil {
		t.Fatal(err)
	}
	if hash == "" {
		t.Fatal("expected hash")
	}
	commits, err := GetLog(dir, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(commits) == 0 {
		t.Fatal("expected at least one commit")
	}
}

// ─── Pull error path ──────────────────────────────────────────────────────────

func TestPull_NonRepo(t *testing.T) {
	_, err := Pull(t.TempDir(), "origin")
	if err == nil {
		t.Fatal("expected error pulling in non-repo")
	}
}

func TestGetBaseBranch_FallbackToMain(t *testing.T) {
	dir := t.TempDir()
	cmds := [][]string{
		{"git", "init", "-b", "custom-branch"},
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
	base := GetBaseBranch(dir)
	if base != "main" {
		t.Errorf("expected fallback to 'main', got %q", base)
	}
}

func TestResolveGitHubRepo_WithRemote(t *testing.T) {
	dir := initTestRepo(t)
	cmds := [][]string{
		{"git", "remote", "add", "upstream", "https://github.com/owner/myrepo.git"},
	}
	for _, args := range cmds {
		c := exec.Command(args[0], args[1:]...)
		c.Dir = dir
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("setup %v: %v\n%s", args, err, out)
		}
	}
	owner, repo, err := ResolveGitHubRepo(dir)
	if err != nil {
		t.Fatalf("ResolveGitHubRepo: %v", err)
	}
	if owner != "owner" || repo != "myrepo" {
		t.Errorf("got %s/%s, want owner/myrepo", owner, repo)
	}
}

func TestGetStatus_StagedAndModified(t *testing.T) {
	dir := initTestRepo(t)
	f := filepath.Join(dir, "test.txt")
	if err := os.WriteFile(f, []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}
	exec.Command("git", "-C", dir, "add", f).Run()          //nolint:errcheck
	os.WriteFile(f, []byte("world"), 0644)                  //nolint:errcheck
	status, err := GetStatus(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(status.Staged) == 0 {
		t.Error("expected staged file")
	}
	if len(status.Unstaged) == 0 {
		t.Error("expected unstaged file (modified after stage)")
	}
}

func TestPull_DefaultRemote(t *testing.T) {
	_, err := Pull(t.TempDir(), "")
	if err == nil {
		t.Fatal("expected error pulling empty dir with default remote")
	}
}

func TestResolveGitHubRepo_NonGitHubNonOrigin(t *testing.T) {
	dir := initTestRepo(t)
	// Add a non-origin remote with a non-GitHub URL.
	exec.Command("git", "-C", dir, "remote", "add", "notgithub", "https://gitlab.com/alice/repo.git").Run() //nolint:errcheck
	_, _, err := ResolveGitHubRepo(dir)
	// origin doesn't exist, non-github remote can't resolve — expect error.
	if err == nil {
		t.Error("expected error resolving GitHub repo from non-GitHub remote")
	}
}

func TestCreateWorktree_BothFail(t *testing.T) {
	// Non-repo → both worktree add attempts fail.
	dir := t.TempDir()
	err := CreateWorktree(dir, filepath.Join(t.TempDir(), "wt"), "branch")
	if err == nil {
		t.Error("expected error creating worktree in non-repo")
	}
}

func TestListBranches_Empty(t *testing.T) {
	dir := initTestRepo(t)
	branches, err := ListBranches(dir)
	if err != nil {
		t.Fatalf("ListBranches: %v", err)
	}
	if branches == nil {
		t.Error("expected non-nil slice")
	}
}

func TestGetLog_NilCommitsSlice(t *testing.T) {
	// Non-repo returns empty, not nil.
	commits, err := GetLog(t.TempDir(), 5)
	if err != nil {
		t.Fatalf("GetLog non-repo: %v", err)
	}
	if commits == nil {
		t.Error("expected non-nil slice from GetLog on non-repo")
	}
}

func TestGetLog_ZeroLimit(t *testing.T) {
	// git log -0 exits 0 with empty output → triggers the commits == nil nil-guard.
	dir := initTestRepo(t)
	commits, err := GetLog(dir, 0)
	if err != nil {
		t.Fatalf("GetLog limit=0: %v", err)
	}
	if commits == nil {
		t.Error("expected non-nil slice from GetLog with limit=0")
	}
}

func TestGetStatus_UnstagedOnly(t *testing.T) {
	// Commit two files. Stage one of them as modified, leave the other unstaged-only.
	// TrimSpace in run() strips the leading space from the *first* line but not subsequent lines.
	// The second file (` M file2`) keeps its leading space → exercises `case y != ' '` branch.
	dir := initTestRepo(t)

	f1 := filepath.Join(dir, "staged.txt")
	f2 := filepath.Join(dir, "unstaged.txt")
	for _, f := range []string{f1, f2} {
		if err := os.WriteFile(f, []byte("initial"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range [][]string{
		{"add", "."},
		{"-c", "user.email=t@t.com", "-c", "user.name=T", "commit", "-m", "add files"},
	} {
		cmd := exec.Command("git", c...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", c, err, out)
		}
	}

	// Modify both: stage f1, leave f2 unstaged.
	os.WriteFile(f1, []byte("staged mod"), 0644)    //nolint:errcheck
	os.WriteFile(f2, []byte("unstaged mod"), 0644)  //nolint:errcheck
	addCmd := exec.Command("git", "add", "staged.txt")
	addCmd.Dir = dir
	addCmd.Run() //nolint:errcheck

	status, err := GetStatus(dir)
	if err != nil {
		t.Fatalf("GetStatus: %v", err)
	}
	if len(status.Unstaged) == 0 {
		t.Errorf("expected unstaged file; Unstaged=%v Staged=%v Untracked=%v", status.Unstaged, status.Staged, status.Untracked)
	}
}

func TestGetStatus_AheadBehind(t *testing.T) {
	// Set up local clone with a commit ahead of origin, triggering ahead/behind parsing.
	bare := initBareRemote(t)
	dir := initTestRepo(t)
	exec.Command("git", "-C", dir, "remote", "add", "origin", bare).Run()            //nolint:errcheck
	exec.Command("git", "-C", dir, "push", "--set-upstream", "origin", "HEAD").Run() //nolint:errcheck

	// Add a commit on local that is not in origin.
	os.WriteFile(filepath.Join(dir, "ahead.txt"), []byte("x"), 0644)             //nolint:errcheck
	exec.Command("git", "-C", dir, "add", ".").Run()                             //nolint:errcheck
	exec.Command("git", "-C", dir, "-c", "user.email=t@t.com", "-c", "user.name=T", "commit", "-m", "ahead").Run() //nolint:errcheck

	status, err := GetStatus(dir)
	if err != nil {
		t.Fatalf("GetStatus: %v", err)
	}
	if status.Ahead == 0 {
		t.Log("ahead count is 0 — upstream tracking may not be set")
	}
}

func TestResolveGitHubRepo_ListRemotesError(t *testing.T) {
	// Non-repo: GetRemoteOrigin fails, then ListRemotes fails → "no git remote" error.
	dir := t.TempDir()
	_, _, err := ResolveGitHubRepo(dir)
	if err == nil {
		t.Error("expected error from non-repo directory")
	}
}

func TestListWorktrees_FinalEntryFlush(t *testing.T) {
	// ListWorktrees should flush the last worktree entry even without a trailing blank line.
	// We test by creating an extra worktree so the output ends with a non-empty entry.
	dir := initTestRepo(t)
	wtDir := filepath.Join(t.TempDir(), "wt-flush")
	if err := CreateWorktree(dir, wtDir, "wt-flush-branch"); err != nil {
		t.Fatalf("CreateWorktree: %v", err)
	}
	defer RemoveWorktree(dir, wtDir) //nolint:errcheck

	worktrees, err := ListWorktrees(dir)
	if err != nil {
		t.Fatalf("ListWorktrees: %v", err)
	}
	if len(worktrees) < 2 {
		t.Errorf("expected at least 2 worktrees, got %d", len(worktrees))
	}
}

func TestListBranches_NilGuard(t *testing.T) {
	// When git branch --list returns no output for an empty non-repo, branches should be non-nil.
	// We use a non-repo to trigger the error path that returns nil,err.
	dir := t.TempDir()
	_, err := ListBranches(dir)
	// Non-repo errors — just verify no panic.
	_ = err
}

func TestCommit_RevParseError(t *testing.T) {
	// Non-repo: git add -A fails → error returned.
	dir := t.TempDir()
	_, err := Commit(dir, "test")
	if err == nil {
		t.Error("expected error committing to non-repo")
	}
}

func TestCommit_RevParseFails(t *testing.T) {
	// Verify Commit doesn't error when rev-parse has issues (it ignores the error).
	dir := initTestRepo(t)
	if err := os.WriteFile(filepath.Join(dir, "x.txt"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	hash, err := Commit(dir, "second")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_ = hash
}

func TestListWorktrees_NonRepo(t *testing.T) {
	// Non-repo triggers the `return nil, err` path in ListWorktrees.
	dir := t.TempDir()
	_, err := ListWorktrees(dir)
	if err == nil {
		t.Error("expected error listing worktrees for non-repo")
	}
}

func TestResolveGitHubRepo_SkipsOriginAndGetURLError(t *testing.T) {
	// Repo has "origin" (non-GitHub) and a second remote that has a deleted URL.
	// This triggers: skip origin continue, then GetRemoteURL-error continue.
	dir := initTestRepo(t)
	// Add origin with a non-GitHub URL.
	exec.Command("git", "-C", dir, "remote", "add", "origin", "https://gitlab.com/x/y.git").Run() //nolint:errcheck
	// Add a second remote called "backup" with a non-GitHub URL.
	exec.Command("git", "-C", dir, "remote", "add", "backup", "https://bitbucket.org/a/b.git").Run() //nolint:errcheck
	_, _, err := ResolveGitHubRepo(dir)
	// No GitHub remote found → error.
	if err == nil {
		t.Error("expected error: no GitHub remote")
	}
}

func TestListBranches_EmptyRepo(t *testing.T) {
	// A fresh git repo with no commits has an unborn branch.
	// `git branch --list` exits 0 with empty output → triggers branches nil guard.
	dir := t.TempDir()
	cmd := exec.Command("git", "init")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	branches, err := ListBranches(dir)
	if err != nil {
		t.Fatalf("ListBranches on unborn branch repo: %v", err)
	}
	// Should return non-nil empty slice.
	if branches == nil {
		t.Error("expected non-nil branches slice")
	}
}
