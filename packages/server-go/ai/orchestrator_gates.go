package ai

import (
	"fmt"
	"strings"
	"time"

	"github.com/engine/server/db"
	gogit "github.com/engine/server/git"
)

// createSessionFn is injectable so the gates' session-failure paths are
// testable, matching the pattern used for the working-state helpers.
var createSessionFn = db.CreateSession

// gitNoChangesSentinel is what gogit.GetDiff returns instead of an empty
// string when the working tree is clean.
const gitNoChangesSentinel = "(no changes)"

// orchestratorCriticStep runs the diff-only critic over the working tree.
//
// This is deliberately the cheap half of a two-stage gate. RunCriticGate is a
// single focused pass over the diff with no tools; orchestratorReviewStep is a
// twelve-turn reviewer that boots the project and runs its test suite. Putting
// the cheap one first means a diff with an obvious defect goes back to the
// builder without ever paying for the expensive one, and a diff that survives
// both has been judged twice by independent means.
//
// Returns CriticApprove when there is nothing to judge — an empty diff, or a
// git/session failure. A gate that cannot run must not block the pipeline; the
// full reviewer still gets its say.
func orchestratorCriticStep(cfg OrchestratorConfig, step *PlanStep, cancel <-chan struct{}) (CriticVerdict, string) {
	if cancelClosed(cancel) {
		return CriticApprove, ""
	}

	// gogit.GetDiff is lossy: it discards git's error, returns combined
	// stdout+stderr, and substitutes the literal "(no changes)" for an empty
	// diff. So a clean tree yields that sentinel and a non-repository yields
	// git's error text — both non-empty, neither reviewable. Require the output
	// to actually look like a unified diff before spending a model call on it.
	diff, err := gogit.GetDiff(cfg.ProjectPath, "")
	diff = strings.TrimSpace(diff)
	if err != nil || diff == "" || diff == gitNoChangesSentinel || !strings.Contains(diff, "diff --git") {
		return CriticApprove, ""
	}

	sessionID := fmt.Sprintf("%s-critic-%d-%d", chooseSessionPrefix(cfg), step.Index, time.Now().UnixNano())
	if err := createSessionFn(sessionID, cfg.ProjectPath, ""); err != nil {
		return CriticApprove, ""
	}

	ctx, _ := newChatContextForRole(cfg, sessionID, RoleReviewer, cancel)
	result := RunCriticGate(ctx, diff)
	if result.IsApproved() {
		return CriticApprove, ""
	}
	return CriticReject, result.FindingsText()
}

// orchestratorRepairStep tries to fix a failing behavioural validation in place
// rather than handing the failure back to the builder.
//
// The old behaviour on a failed validation was to reopen the most recent step
// and re-run the whole builder against it, which is both expensive and blunt:
// the builder re-derives what went wrong from a one-line feedback string. The
// repair loop instead diagnoses from the failure evidence, applies a targeted
// fix, re-validates, and gives up after maxRepairAttempts.
//
// Returns true only when validation actually passed on a repair attempt. The
// caller re-enters the normal loop on success rather than completing inline, so
// every completion guard (skipped-step blocking in particular) still runs.
func orchestratorRepairStep(cfg OrchestratorConfig, state *OrchestrationState, validateErr error, cancel <-chan struct{}) bool {
	if cancelClosed(cancel) || validateErr == nil {
		return false
	}

	sessionID := fmt.Sprintf("%s-repair-%d", chooseSessionPrefix(cfg), time.Now().UnixNano())
	if err := createSessionFn(sessionID, cfg.ProjectPath, ""); err != nil {
		return false
	}

	policy := ResolveAutonomousPolicy(cfg.ProjectPath)
	// The repair loop has to be able to land its own fix, or it can only ever
	// describe the problem.
	policy.AutoCommit = true

	ctx, _ := newChatContextForRole(cfg, sessionID, RoleAutonomousBuilder, cancel)
	ctx.AutonomousPolicy = &policy

	ws := LoadWorkingStateForSession(sessionID)

	// Each attempt re-runs the real behavioural validation. That is the same
	// check the orchestrator trusts to declare the project done, so a repair
	// that satisfies it is genuinely resolved rather than self-reported.
	runTest := func() BehavioralResult {
		if cancelClosed(cancel) {
			return BehavioralResult{Evidence: "cancelled"}
		}
		summary, err := orchestratorValidatePhase(cfg, state, cancel)
		state.LastValidation = summary
		if err != nil {
			return BehavioralResult{
				ErrorCount: 1,
				Evidence:   strings.TrimSpace(summary + "\n" + err.Error()),
			}
		}
		return BehavioralResult{IssueResolved: true, TestPassed: true, Evidence: summary}
	}

	result := ExecuteRepairLoop(ctx, &ws, "behavioural validation failed", validateErr.Error(), runTest)
	return result.Resolved
}
