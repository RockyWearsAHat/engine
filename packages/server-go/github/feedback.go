package github

import (
	"fmt"
	"net/url"
	"strings"
)

// PostCommitStatus writes a commit status to owner/repo at the given SHA.
// state must be one of: pending, success, failure, error.
// context is the status check name (e.g. "engine/orchestrator").
// Returns nil even when GITHUB_TOKEN is unset so feedback is best-effort.
func PostCommitStatus(owner, repo, sha, state, statusContext, description, targetURL string) error {
	if strings.TrimSpace(owner) == "" || strings.TrimSpace(repo) == "" || strings.TrimSpace(sha) == "" {
		return fmt.Errorf("commit-status: owner, repo and sha are required")
	}
	c, err := NewClient(owner, repo)
	if err != nil {
		// No token configured — best-effort, return nil so callers can ignore.
		return nil
	}
	payload := map[string]any{
		"state":       normaliseStatusState(state),
		"description": clampDescription(description),
		"context":     defaultIfEmpty(statusContext, "engine/orchestrator"),
	}
	if strings.TrimSpace(targetURL) != "" {
		payload["target_url"] = targetURL
	}
	endpoint := fmt.Sprintf("%s/repos/%s/%s/statuses/%s", apiBase(), owner, repo, url.PathEscape(sha))
	if _, err := c.doPost(endpoint, payload); err != nil {
		return fmt.Errorf("post commit status: %w", err)
	}
	return nil
}

// PostIssueComment posts a comment to the issue/PR number in owner/repo.
// Used to keep humans informed on the GitHub side without checking Discord.
// Returns nil when GITHUB_TOKEN is unset (best-effort).
func PostIssueComment(owner, repo string, issueNumber int, body string) error {
	if strings.TrimSpace(owner) == "" || strings.TrimSpace(repo) == "" || issueNumber <= 0 {
		return fmt.Errorf("issue-comment: owner, repo and issue number are required")
	}
	if strings.TrimSpace(body) == "" {
		return fmt.Errorf("issue-comment: empty body")
	}
	c, err := NewClient(owner, repo)
	if err != nil {
		return nil // best-effort
	}
	payload := map[string]any{"body": body}
	endpoint := fmt.Sprintf("%s/repos/%s/%s/issues/%d/comments", apiBase(), owner, repo, issueNumber)
	if _, err := c.doPost(endpoint, payload); err != nil {
		return fmt.Errorf("post issue comment: %w", err)
	}
	return nil
}

// FindHeadSHA returns the HEAD SHA for owner/repo at the given branch ref
// (e.g. "main", "HEAD"). Used by the orchestrator to attach commit status
// without making the caller pass a SHA around.
func FindHeadSHA(owner, repo, ref string) (string, error) {
	if strings.TrimSpace(ref) == "" {
		ref = "HEAD"
	}
	c, err := NewClient(owner, repo)
	if err != nil {
		return "", err
	}
	endpoint := fmt.Sprintf("%s/repos/%s/%s/commits/%s", apiBase(), owner, repo, url.PathEscape(ref))
	body, err := c.doGet(endpoint)
	if err != nil {
		return "", fmt.Errorf("find head sha: %w", err)
	}
	// Cheap parse — pull the "sha" field out without a full struct.
	const key = `"sha":"`
	idx := strings.Index(string(body), key)
	if idx < 0 {
		return "", fmt.Errorf("sha field not in response")
	}
	rest := string(body)[idx+len(key):]
	end := strings.IndexByte(rest, '"')
	if end < 0 {
		return "", fmt.Errorf("sha field truncated")
	}
	return rest[:end], nil
}

// ── Status normalization and formatting (internal) ────────────────────────────────────
// Helper functions to normalize and format status values for GitHub API calls.

// normaliseStatusState normalizes a status state string to GitHub's canonical values.
// Accepts variants like "in_progress", "plan", "execute" and maps them to standard states.
func normaliseStatusState(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "pending", "in_progress", "plan", "execute", "review", "validate":
		return "pending"
	case "success", "done", "approve", "approved":
		return "success"
	case "failure", "fail", "reject", "rejected", "blocked":
		return "failure"
	case "error":
		return "error"
	default:
		return "pending"
	}
}

// clampDescription truncates a status description to GitHub's 140-character limit.
func clampDescription(s string) string {
	const githubDescLimit = 140
	s = strings.TrimSpace(s)
	if len(s) <= githubDescLimit {
		return s
	}
	return s[:githubDescLimit-1] + "…"
}

// defaultIfEmpty returns the fallback string if s is empty or whitespace-only.
func defaultIfEmpty(s, fallback string) string {
	if strings.TrimSpace(s) == "" {
		return fallback
	}
	return s
}
