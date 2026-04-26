package ai

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	gogit "github.com/engine/server/git"
)

// worktreeCacheDir is where per-session worktrees are created.
// Uses ~/.engine/worktrees/<sessionID>/<repoName>.
func worktreeCacheDir(sessionID, repoPath string) string {
	home, _ := os.UserHomeDir()
	repoName := filepath.Base(repoPath)
	return filepath.Join(home, ".engine", "worktrees", sanitizeID(sessionID), repoName)
}

func sanitizeID(id string) string {
	r := strings.NewReplacer("/", "_", "\\", "_", ":", "_")
	return r.Replace(id)
}

// sessionBranchName returns the git branch name for a given session.
func sessionBranchName(sessionID string) string {
	// Truncate to keep branch names short.
	id := sanitizeID(sessionID)
	if len(id) > 20 {
		id = id[:20]
	}
	return fmt.Sprintf("engine/session/%s", id)
}

// EnsureSessionWorktree creates a git worktree for the given session at a
// deterministic path under ~/.engine/worktrees.
//
// Returns the worktree path to use as ProjectPath for the session.
// If worktrees are not supported (non-git repo, git < 2.5) falls back to repoPath.
func EnsureSessionWorktree(sessionID, repoPath string) (string, error) {
	wtPath := worktreeCacheDir(sessionID, repoPath)

	// If already exists, return it.
	if _, err := os.Stat(wtPath); err == nil {
		return wtPath, nil
	}

	// Verify the repo supports worktrees (git 2.5+).
	if _, err := os.Stat(filepath.Join(repoPath, ".git")); err != nil {
		// Not a git repo — fall back to the main repo path.
		return repoPath, nil
	}

	branchName := sessionBranchName(sessionID)

	// Create parent directory.
	_ = os.MkdirAll(filepath.Dir(wtPath), 0o755)

	if err := gogit.CreateWorktree(repoPath, wtPath, branchName); err != nil {
		// Fall back gracefully — worktrees failing must not break the session.
		return repoPath, nil
	}

	return wtPath, nil
}

// CleanupSessionWorktree removes the worktree for the given session and optionally
// merges its branch back to the baseline.
//
// merge=true triggers `git merge --no-ff <sessionBranch>` in the main repo.
func CleanupSessionWorktree(sessionID, repoPath string, merge bool) error {
	wtPath := worktreeCacheDir(sessionID, repoPath)

	if _, err := os.Stat(wtPath); err != nil {
		// Already gone.
		return nil
	}

	branchName := sessionBranchName(sessionID)

	if merge {
		base := gogit.GetBaseBranch(repoPath)
		if err := gogit.CheckoutBranch(repoPath, base); err != nil {
			return fmt.Errorf("checkout baseline %s: %w", base, err)
		}
		if _, err := runGit(repoPath, "merge", "--no-ff", branchName, "-m",
			fmt.Sprintf("Merge engine session %s", sessionID)); err != nil {
			return fmt.Errorf("merge session branch: %w", err)
		}
	}

	if err := gogit.RemoveWorktree(repoPath, wtPath); err != nil {
		return fmt.Errorf("remove worktree: %w", err)
	}

	return nil
}

// runGit is a thin wrapper so worktree_session.go doesn't need to import os/exec directly.
func runGit(cwd string, args ...string) (string, error) {
	return gogit.RunGit(cwd, args...)
}
