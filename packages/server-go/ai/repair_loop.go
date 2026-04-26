package ai

import (
	"fmt"
	"strings"
)

const (
	// maxRepairAttempts caps the repair loop to prevent infinite cycles.
	maxRepairAttempts = 3
)

// RepairPlan is a structured remediation plan generated from test failure evidence.
type RepairPlan struct {
	Issue       string       `json:"issue"`
	Diagnosis   string       `json:"diagnosis"`
	Steps       []RepairStep `json:"steps"`
	AttemptNum  int          `json:"attemptNum"`
}

// RepairStep is one targeted action in a repair plan.
type RepairStep struct {
	File        string `json:"file"`
	Action      string `json:"action"`
	Reason      string `json:"reason"`
}

// generateRepairPlanChatFn is injectable so tests can supply a mock Chat call.
var generateRepairPlanChatFn = Chat

// GenerateRepairPlan produces a structured repair plan from failure evidence.
// It injects the failure evidence into RolePlanner's prompt so the plan is
// targeted at the specific failure — not a continuation of the prior thread.
func GenerateRepairPlan(ctx *ChatContext, issue string, failureEvidence string, attemptNum int) RepairPlan {
	if ctx == nil {
		return RepairPlan{Issue: issue, AttemptNum: attemptNum}
	}

	// Build a focused prompt for the planner.
	plannerInput := strings.Join([]string{
		fmt.Sprintf("REPAIR ATTEMPT %d for: %s", attemptNum, issue),
		"",
		"FAILURE EVIDENCE:",
		failureEvidence,
		"",
		"Generate a targeted repair plan (3-5 steps) that directly addresses the failure evidence above.",
		"Each step: file to change, specific action, reason it addresses the failure.",
		"Do NOT repeat approaches that produced this failure.",
	}, "\n")

	// Run RolePlanner with the failure evidence as the sole input.
	// Guard against a nil Cancel channel — Chat requires a live channel.
	cancelChan := ctx.Cancel
	if cancelChan == nil {
		cancelChan = make(chan struct{})
	}
	// Provide a no-op OnError so Chat() never dereferences a nil error handler.
	onErr := ctx.OnError
	if onErr == nil {
		onErr = func(string) {}
	}
	plannerCtx := &ChatContext{
		ProjectPath:  ctx.ProjectPath,
		SessionID:    ctx.SessionID,
		Role:         RolePlanner,
		Usage:        ctx.Usage,
		Quarantine:   ctx.Quarantine,
		Cancel:       cancelChan,
		GetOpenTabs:  ctx.GetOpenTabs,
		SendToClient: ctx.SendToClient,
		OnError:      onErr,
	}

	var planOutput strings.Builder
	plannerCtx.OnChunk = func(content string, done bool) {
		planOutput.WriteString(content)
	}

	// Fire off the planner call and collect output.
	generateRepairPlanChatFn(plannerCtx, plannerInput)

	return parseRepairPlan(issue, planOutput.String(), attemptNum)
}

// parseRepairPlan extracts RepairSteps from the planner's numbered output.
// Falls back to a single "review evidence" step if parsing yields nothing.
func parseRepairPlan(issue, planText string, attemptNum int) RepairPlan {
	plan := RepairPlan{
		Issue:      issue,
		Diagnosis:  summarizeFirstLines(planText, 2),
		AttemptNum: attemptNum,
	}

	lines := strings.Split(planText, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// Look for numbered steps: "1. " or "1) "
		if len(line) >= 3 && line[0] >= '1' && line[0] <= '9' &&
			(line[1] == '.' || line[1] == ')') && line[2] == ' ' {
			plan.Steps = append(plan.Steps, RepairStep{Action: line[3:]})
		}
	}

	if len(plan.Steps) == 0 {
		plan.Steps = []RepairStep{{Action: "Review failure evidence and apply targeted fix", Reason: "no structured steps parsed"}}
	}
	return plan
}

func summarizeFirstLines(text string, n int) string {
	lines := strings.SplitN(strings.TrimSpace(text), "\n", n+1)
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, " ")
}

// RepairLoopResult reports the outcome of the repair loop.
type RepairLoopResult struct {
	Resolved    bool
	AttemptsMade int
	FinalEvidence string
}

// ExecuteRepairLoop runs GenerateRepairPlan → test → repeat until resolved or
// maxRepairAttempts is reached.
func ExecuteRepairLoop(
	ctx *ChatContext,
	ws *WorkingState,
	issue string,
	initialFailureEvidence string,
	runTest func() BehavioralResult,
) RepairLoopResult {
	evidence := initialFailureEvidence
	for attempt := 1; attempt <= maxRepairAttempts; attempt++ {
		plan := GenerateRepairPlan(ctx, issue, evidence, attempt)
		if ws != nil {
			ws.RecordAttempt(fmt.Sprintf("repair attempt %d: %s", attempt, plan.Diagnosis))
			PersistWorkingState(ctx.SessionID, ws)
		}

		// Deliver the plan to the client as a chat message so the user can see it.
		if ctx.SendToClient != nil {
			ctx.SendToClient("repair.plan", map[string]any{
				"issue":      issue,
				"attemptNum": attempt,
				"diagnosis":  plan.Diagnosis,
				"steps":      plan.Steps,
			})
		}

		// Re-run the test suite with the current working tree.
		result := runTest()
		if ws != nil {
			ws.AddObservation(fmt.Sprintf("repair %d: resolved=%v errors=%d", attempt, result.IssueResolved, result.ErrorCount))
		}

		if result.IssueResolved {
			return RepairLoopResult{Resolved: true, AttemptsMade: attempt, FinalEvidence: result.Evidence}
		}
		evidence = result.Evidence
	}
	return RepairLoopResult{Resolved: false, AttemptsMade: maxRepairAttempts, FinalEvidence: evidence}
}
