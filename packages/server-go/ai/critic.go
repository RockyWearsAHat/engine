package ai

import (
	"strings"
)

// CriticVerdict is the outcome of a Verifier/Critic review pass.
type CriticVerdict int

const (
	CriticApprove CriticVerdict = iota
	CriticReject
)

// CriticResult contains the verifier's assessment of a completed AI response.
type CriticResult struct {
	Verdict  CriticVerdict
	Findings []string // Non-empty on CriticReject — one sentence per issue.
	Diff     string   // The diff that was reviewed (may be empty for non-code changes).
}

// String returns a human-readable label for the verdict.
func (v CriticVerdict) String() string {
	if v == CriticApprove {
		return "APPROVE"
	}
	return "REJECT"
}

// IsApproved returns true when the critic approved the output.
func (r CriticResult) IsApproved() bool { return r.Verdict == CriticApprove }

// FindingsText returns all findings joined as a single block for LLM injection.
func (r CriticResult) FindingsText() string {
	return strings.Join(r.Findings, "\n")
}

// maxCriticRejectLoops is the maximum number of auto-reject cycles before the
// critic stops blocking and surfaces the issue to the user instead.
const maxCriticRejectLoops = 2

// runCriticChatFn is injectable for tests so the LLM call can be mocked.
var runCriticChatFn = Chat

// RunCriticGate invokes RoleReviewer on the provided diff and returns a CriticResult.
// This is the systematic gate that fires after every structural implementation.
// When diff is empty, the critic auto-approves (no code to review).
func RunCriticGate(ctx *ChatContext, diff string) CriticResult {
	if diff == "" {
		return CriticResult{Verdict: CriticApprove}
	}

	// Guard against nil Cancel and OnError — Chat requires both.
	cancelChan := ctx.Cancel
	if cancelChan == nil {
		cancelChan = make(chan struct{})
	}
	onErr := ctx.OnError
	if onErr == nil {
		onErr = func(string) {}
	}

	// Build a focused reviewer context — read-only, no tool mutations.
	reviewerCtx := &ChatContext{
		ProjectPath:  ctx.ProjectPath,
		SessionID:    ctx.SessionID,
		Role:         RoleReviewer,
		Usage:        ctx.Usage,
		Quarantine:   ctx.Quarantine,
		Cancel:       cancelChan,
		GetOpenTabs:  ctx.GetOpenTabs,
		SendToClient: ctx.SendToClient,
		OnError:      onErr,
	}

	var reviewOutput strings.Builder
	reviewerCtx.OnChunk = func(content string, done bool) {
		reviewOutput.WriteString(content)
	}

	reviewerInput := "Review this diff:\n\n```diff\n" + diff + "\n```"
	runCriticChatFn(reviewerCtx, reviewerInput)

	return parseCriticOutput(reviewOutput.String(), diff)
}

// parseCriticOutput extracts the verdict and findings from the reviewer's response.
func parseCriticOutput(output, diff string) CriticResult {
	upper := strings.ToUpper(output)
	verdict := CriticApprove
	if strings.Contains(upper, "REJECT") {
		verdict = CriticReject
	}

	result := CriticResult{Verdict: verdict, Diff: diff}
	if verdict == CriticReject {
		result.Findings = extractFindings(output)
	}
	return result
}

// extractFindings pulls individual finding lines from a REJECT response.
// Looks for lines starting with "-", "•", "*", or numbered items.
func extractFindings(output string) []string {
	var findings []string
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// Skip the REJECT verdict line itself.
		if strings.ToUpper(line) == "REJECT" {
			continue
		}
		// Include numbered findings, bullets, or lines with ":" (file:line:reason pattern).
		if len(line) > 1 && (line[0] == '-' || line[0] == '*' || strings.HasPrefix(line, "•")) {
			trimmed := strings.TrimPrefix(strings.TrimPrefix(strings.TrimPrefix(line, "-"), "*"), "•")
			findings = append(findings, strings.TrimSpace(trimmed))
			continue
		}
		if len(line) >= 3 && line[0] >= '1' && line[0] <= '9' && (line[1] == '.' || line[1] == ')') {
			findings = append(findings, strings.TrimSpace(line[2:]))
			continue
		}
		if strings.Contains(line, ":") && !strings.HasPrefix(strings.ToUpper(line), "REJECT") {
			findings = append(findings, line)
		}
	}
	if len(findings) == 0 {
		findings = []string{"Reviewer rejected without specific findings — re-inspect the diff."}
	}
	return findings
}

// InjectCriticFindings formats a CriticResult as a high-priority context block
// to be prepended to the next implementer's input.
func InjectCriticFindings(result CriticResult) string {
	if result.IsApproved() {
		return ""
	}
	var b strings.Builder
	b.WriteString("<critic_findings priority=\"high\">\n")
	b.WriteString("The previous implementation was REJECTED. Fix these issues before proceeding:\n\n")
	for i, f := range result.Findings {
		b.WriteString(strings.TrimSpace(f))
		if i < len(result.Findings)-1 {
			b.WriteString("\n")
		}
	}
	b.WriteString("\n</critic_findings>")
	return b.String()
}
