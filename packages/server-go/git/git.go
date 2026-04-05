package git

import (
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// GitStatus mirrors the TypeScript GitStatus type.
type GitStatus struct {
	Branch    string   `json:"branch"`
	Staged    []string `json:"staged"`
	Unstaged  []string `json:"unstaged"`
	Untracked []string `json:"untracked"`
	Ignored   []string `json:"ignored"`
	Ahead     int      `json:"ahead"`
	Behind    int      `json:"behind"`
}

// GitCommit mirrors the TypeScript GitCommit type.
type GitCommit struct {
	Hash    string `json:"hash"`
	Message string `json:"message"`
	Author  string `json:"author"`
	Date    string `json:"date"`
}

func run(cwd string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = cwd
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// GetCurrentBranch returns the name of the currently checked-out branch.
func GetCurrentBranch(cwd string) (string, error) {
	out, err := run(cwd, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return "unknown", nil
	}
	return out, nil
}

// GetStatus returns a full GitStatus for the repo at cwd.
func GetStatus(cwd string) (*GitStatus, error) {
	branch, _ := GetCurrentBranch(cwd)

	porcelain, err := run(cwd, "status", "--porcelain=v1")
	if err != nil {
		return &GitStatus{Branch: branch, Staged: []string{}, Unstaged: []string{}, Untracked: []string{}, Ignored: []string{}}, nil
	}

	status := &GitStatus{
		Branch:    branch,
		Staged:    []string{},
		Unstaged:  []string{},
		Untracked: []string{},
		Ignored:   []string{},
	}

	for _, line := range strings.Split(porcelain, "\n") {
		if len(line) < 4 {
			continue
		}
		x, y, file := line[0], line[1], strings.TrimSpace(line[3:])
		switch {
		case x == '?' && y == '?':
			status.Untracked = append(status.Untracked, file)
		case x != ' ' && x != '?':
			status.Staged = append(status.Staged, file)
			if y != ' ' && y != '?' {
				status.Unstaged = append(status.Unstaged, file)
			}
		case y != ' ' && y != '?':
			status.Unstaged = append(status.Unstaged, file)
		}
	}

	// Ahead/behind
	ab, err := run(cwd, "rev-list", "--left-right", "--count", "HEAD...@{upstream}")
	if err == nil {
		parts := strings.Fields(ab)
		if len(parts) == 2 {
			fmt.Sscan(parts[0], &status.Ahead)
			fmt.Sscan(parts[1], &status.Behind)
		}
	}

	return status, nil
}

// GetDiff returns the combined staged+unstaged diff, optionally for a single path.
func GetDiff(cwd string, path string) (string, error) {
	args := []string{"diff", "HEAD"}
	if path != "" {
		args = append(args, "--", path)
	}
	out, _ := run(cwd, args...)
	if out == "" {
		out = "(no changes)"
	}
	return out, nil
}

// GetLog returns the last n commits as structured data.
func GetLog(cwd string, limit int) ([]GitCommit, error) {
	format := "--pretty=format:%H\x1f%s\x1f%an\x1f%aI"
	out, err := run(cwd, "log", fmt.Sprintf("-%d", limit), format)
	if err != nil {
		return []GitCommit{}, nil
	}

	var commits []GitCommit
	for _, line := range strings.Split(out, "\n") {
		parts := strings.Split(line, "\x1f")
		if len(parts) != 4 {
			continue
		}
		// Parse ISO date and reformat
		t, err := time.Parse(time.RFC3339, parts[3])
		date := parts[3]
		if err == nil {
			date = t.UTC().Format(time.RFC3339)
		}
		commits = append(commits, GitCommit{
			Hash:    parts[0],
			Message: parts[1],
			Author:  parts[2],
			Date:    date,
		})
	}
	if commits == nil {
		commits = []GitCommit{}
	}
	return commits, nil
}

// Commit stages all changes and creates a commit with the given message.
// Returns the short commit hash.
func Commit(cwd, message string) (string, error) {
	if _, err := run(cwd, "add", "-A"); err != nil {
		return "", fmt.Errorf("git add: %w", err)
	}
	if _, err := run(cwd, "commit", "-m", message); err != nil {
		return "", fmt.Errorf("git commit: %w", err)
	}
	hash, err := run(cwd, "rev-parse", "--short", "HEAD")
	if err != nil {
		return "unknown", nil
	}
	return hash, nil
}

// GetRemoteOrigin returns the URL of the origin remote.
func GetRemoteOrigin(cwd string) (string, error) {
	return GetRemoteURL(cwd, "origin")
}

// GetRemoteURL returns the URL of a named remote.
func GetRemoteURL(cwd, name string) (string, error) {
	return run(cwd, "remote", "get-url", name)
}

// ListRemotes returns the configured git remotes for a repository.
func ListRemotes(cwd string) ([]string, error) {
	out, err := run(cwd, "remote")
	if err != nil {
		return nil, err
	}

	remotes := make([]string, 0)
	for _, line := range strings.Split(out, "\n") {
		remote := strings.TrimSpace(line)
		if remote != "" {
			remotes = append(remotes, remote)
		}
	}
	return remotes, nil
}

// ParseGitHubRepo extracts an owner/repo pair from a GitHub remote URL.
func ParseGitHubRepo(remoteURL string) (string, string, bool) {
	trimmed := strings.TrimSpace(strings.TrimSuffix(remoteURL, ".git"))
	if trimmed == "" {
		return "", "", false
	}

	var repoPath string
	switch {
	case strings.Contains(trimmed, "github.com:"):
		repoPath = trimmed[strings.Index(trimmed, "github.com:")+len("github.com:"):]
	case strings.Contains(trimmed, "github.com/"):
		repoPath = trimmed[strings.Index(trimmed, "github.com/")+len("github.com/"):]
	default:
		return "", "", false
	}

	repoPath = strings.Trim(repoPath, "/")
	parts := strings.Split(repoPath, "/")
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}

	return parts[0], parts[1], true
}

// ResolveGitHubRepo returns the first GitHub owner/repo found in repository remotes.
func ResolveGitHubRepo(cwd string) (string, string, error) {
	if remoteURL, err := GetRemoteOrigin(cwd); err == nil {
		if owner, repo, ok := ParseGitHubRepo(remoteURL); ok {
			return owner, repo, nil
		}
	}

	remotes, err := ListRemotes(cwd)
	if err != nil {
		return "", "", fmt.Errorf("no git remote")
	}

	for _, remote := range remotes {
		if remote == "origin" {
			continue
		}

		remoteURL, err := GetRemoteURL(cwd, remote)
		if err != nil {
			continue
		}
		if owner, repo, ok := ParseGitHubRepo(remoteURL); ok {
			return owner, repo, nil
		}
	}

	return "", "", fmt.Errorf("not a GitHub repo")
}
