package github

// EngineEngagement provides rich, structured GitHub comments so the user can
// follow exactly what Engine is doing directly from the GitHub issue/PR page.
//
// Design: Engine pins one "status comment" per issue and edits it in-place as
// phases advance.  This avoids comment spam while keeping the issue timeline
// clean.  A new comment is posted only when the status meaningfully changes
// (kickoff, completion, blocked) and the edit replaces the prior one.

import (
	"fmt"
	"log"
	"strings"
	"time"
)

// EngageOnIssuePickup is called the moment Engine decides to work on an issue.
// It self-assigns Engine, adds the issue to the project board, and pins a
// kickoff comment so the user sees immediate activity on GitHub.
// projectPath is the local clone root (used for the comment store).
func EngageOnIssuePickup(projectPath, owner, repo string, issueNumber int, sessionID string) {
	// 1. Self-assign.
	if err := AssignEngine(owner, repo, issueNumber); err != nil {
		// logged inside AssignEngine — don't shadow it
	}

	// 2. Add to project board.
	if _, err := AddIssueToEngineProject(owner, repo, issueNumber); err != nil {
		log.Printf("engagement: add issue %s/%s#%d to project failed: %v", owner, repo, issueNumber, err)
	}
	if err := UpdateProjectItemStatus(owner, "", "In Progress"); err != nil {
		log.Printf("engagement: set project item status In Progress failed for %s/%s#%d: %v", owner, repo, issueNumber, err)
	}

	// 3. Pin kickoff comment.
	body := kickoffCommentBody(owner, repo, issueNumber, sessionID)
	c, err := EngineClient(owner, repo)
	if err != nil {
		return
	}
	comment, err := c.AddComment(issueNumber, body)
	if err != nil {
		return
	}
	store := IssueCommentStoreFor(projectPath)
	store.Set(owner, repo, issueNumber, comment.ID)
}

// EngageOnIssueProgress updates the pinned status comment with phase/step info.
// stepsDone and stepsTotal may both be 0 when the plan has not been produced yet.
func EngageOnIssueProgress(projectPath, owner, repo string, issueNumber int, phase, detail string, stepsDone, stepsTotal int) {
	store := IssueCommentStoreFor(projectPath)
	commentID, ok := store.Get(owner, repo, issueNumber)
	if !ok {
		return
	}
	c, err := EngineClient(owner, repo)
	if err != nil {
		return
	}
	body := progressCommentBody(phase, detail, stepsDone, stepsTotal)
	if err := c.EditComment(commentID, body); err != nil {
		log.Printf("engagement: edit progress comment failed for %s/%s#%d: %v", owner, repo, issueNumber, err)
	}
	if err := UpdateProjectItemStatus(owner, "", phaseToProjectStatus(phase)); err != nil {
		log.Printf("engagement: update project item status failed for %s/%s#%d: %v", owner, repo, issueNumber, err)
	}
}

// EngageOnIssueComplete posts a final comment, unassigns Engine (optional),
// and marks the project board item as "Done".
func EngageOnIssueComplete(projectPath, owner, repo string, issueNumber, stepsTotal int, prURL string) {
	c, err := EngineClient(owner, repo)
	if err != nil {
		return
	}
	store := IssueCommentStoreFor(projectPath)
	commentID, ok := store.Get(owner, repo, issueNumber)
	body := completionCommentBody(stepsTotal, prURL)
	if ok {
		_ = c.EditComment(commentID, body)
	} else {
		if comment, err := c.AddComment(issueNumber, body); err == nil {
			store.Set(owner, repo, issueNumber, comment.ID)
		}
	}
	if err := UpdateProjectItemStatus(owner, "", "Done"); err != nil {
		log.Printf("engagement: set project item Done failed for %s/%s#%d: %v", owner, repo, issueNumber, err)
	}
}

// EngageOnIssueBlocked posts a "help needed" update to the pinned comment.
func EngageOnIssueBlocked(projectPath, owner, repo string, issueNumber int, reason string) {
	store := IssueCommentStoreFor(projectPath)
	commentID, ok := store.Get(owner, repo, issueNumber)
	body := blockedCommentBody(reason)
	c, err := EngineClient(owner, repo)
	if err != nil {
		return
	}
	if ok {
		_ = c.EditComment(commentID, body)
	} else {
		if comment, err := c.AddComment(issueNumber, body); err == nil {
			store.Set(owner, repo, issueNumber, comment.ID)
		}
	}
}

// ── Comment bodies ────────────────────────────────────────────────────────────

func engineHeader() string {
	return fmt.Sprintf("<!-- engine-status-comment -->\n**%s is on it** 🤖", EngineDisplayName())
}

func kickoffCommentBody(owner, repo string, issueNumber int, sessionID string) string {
	login := EngineLogin()
	loginLine := ""
	if login != "" {
		loginLine = fmt.Sprintf("\n- **Assigned:** @%s", login)
	}
	return fmt.Sprintf(
		"%s\n\nI've picked up this issue and started working on it.%s\n"+
			"- **Session:** %s\n- **Started:** %s UTC\n- **Status:** 🔄 Planning…\n\n"+
			"_I'll update this comment as I make progress. "+
			"Use Discord `!stop` or comment `@engine stop` to intervene._",
		engineHeader(),
		loginLine,
		sessionID,
		time.Now().UTC().Format("2006-01-02 15:04"),
	)
}

func progressCommentBody(phase, detail string, done, total int) string {
	icon := phaseEmoji(phase)
	progress := ""
	if total > 0 {
		progress = fmt.Sprintf("\n- **Progress:** %d / %d steps complete", done, total)
	}
	detail = strings.TrimSpace(detail)
	if len(detail) > 200 {
		detail = detail[:197] + "…"
	}
	return fmt.Sprintf(`%s

- **Phase:** %s %s%s
- **Detail:** %s
- **Updated:** %s UTC

_Actively working — I'll edit this comment when the phase changes._`,
		engineHeader(),
		icon, strings.Title(strings.ToLower(phase)), //nolint:staticcheck
		progress,
		detail,
		time.Now().UTC().Format("2006-01-02 15:04"),
	)
}

func completionCommentBody(steps int, prURL string) string {
	prLine := ""
	if strings.TrimSpace(prURL) != "" {
		prLine = fmt.Sprintf("\n- **Pull request:** %s", prURL)
	}
	stepsLine := ""
	if steps > 0 {
		stepsLine = fmt.Sprintf("\n- **Steps completed:** %d", steps)
	}
	return fmt.Sprintf(`%s

✅ **Done!** All acceptance criteria met.%s%s
- **Finished:** %s UTC

_Review the PR, run the tests, and close this issue when satisfied._`,
		engineHeader(),
		stepsLine,
		prLine,
		time.Now().UTC().Format("2006-01-02 15:04"),
	)
}

func blockedCommentBody(reason string) string {
	reason = strings.TrimSpace(reason)
	if len(reason) > 400 {
		reason = reason[:397] + "…"
	}
	return fmt.Sprintf(
		"%s\n\n🚧 **Blocked — human help needed**\n\n%s\n\n- **At:** %s UTC\n\n"+
			"_Reply with guidance, or use Discord `!redirect` to redirect the agent._",
		engineHeader(),
		reason,
		time.Now().UTC().Format("2006-01-02 15:04"),
	)
}

func phaseEmoji(phase string) string {
	switch strings.ToLower(phase) {
	case "plan":
		return "📋"
	case "execute":
		return "🛠️"
	case "review":
		return "🔍"
	case "validate":
		return "🧪"
	case "done":
		return "✅"
	case "failure", "error":
		return "❌"
	default:
		return "🔄"
	}
}

func phaseToProjectStatus(phase string) string {
	switch strings.ToLower(phase) {
	case "done", "success":
		return "Done"
	case "failure", "error", "blocked":
		return "Blocked"
	default:
		return "In Progress"
	}
}
