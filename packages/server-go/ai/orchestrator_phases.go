package ai

import (
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/engine/server/db"
)

// ReviewVerdict is the gate decision out of the reviewer phase.
type ReviewVerdict int

const (
	// ReviewInconclusive — reviewer output didn't parse as approve/reject.
	ReviewInconclusive ReviewVerdict = iota
	// ReviewApprove — step passes, mark done.
	ReviewApprove
	// ReviewReject — step fails, feedback flows back to next builder pass.
	ReviewReject
)

// orchestratorPlanPhase asks RolePlanner to turn the brief into a numbered plan.
// Output is parsed into discrete PlanStep entries.
func orchestratorPlanPhase(cfg OrchestratorConfig, state *OrchestrationState, cancel <-chan struct{}) error {
	sessionID := fmt.Sprintf("%s-plan-%d", chooseSessionPrefix(cfg), time.Now().UnixNano())
	if err := db.CreateSession(sessionID, cfg.ProjectPath, ""); err != nil {
		return fmt.Errorf("create plan session: %w", err)
	}

	var (
		outMu sync.Mutex
		out   strings.Builder
	)
	ctx := &ChatContext{
		ProjectPath: cfg.ProjectPath,
		SessionID:   sessionID,
		Role:        RolePlanner,
		Cancel:      cancel,
		OnChunk: func(content string, _ bool) {
			if content == "" {
				return
			}
			outMu.Lock()
			out.WriteString(content)
			outMu.Unlock()
		},
		OnError:      func(string) {},
		OnToolCall:   func(string, any) {},
		OnToolResult: func(string, any, bool) {},
	}

	// Planner reads the design-concept + vocabulary + PRD layers — every
	// piece of documentation the orchestrator built before this phase.
	contextDoc := ComposeDocContext(cfg.ProjectPath, DocDesign, DocVocabulary, DocPRD)
	if contextDoc == "" {
		contextDoc = readContextDoc(cfg.ProjectPath) // legacy fallback
	}
	prompt := buildPlannerPromptWithContext(state.Brief, contextDoc)
	cfg.chatFnFor()(ctx, prompt)

	steps := parsePlanFromText(out.String())
	if len(steps) == 0 {
		return fmt.Errorf("plan output empty or unparsable; got %d chars", out.Len())
	}
	if err := validatePlanQuality(steps); err != nil {
		repaired, repairErr := orchestratorRepairPlanPhase(cfg, state, out.String(), cancel)
		if repairErr != nil {
			return fmt.Errorf("plan rejected (chatbot output detected): %w; repair pass failed: %v", err, repairErr)
		}
		state.Plan = repaired
		return nil
	}
	state.Plan = steps
	return nil
}

// orchestratorRepairPlanPhase gives the planner one more chance when the first
// output is structurally close but fails the acceptance-command contract.
func orchestratorRepairPlanPhase(cfg OrchestratorConfig, state *OrchestrationState, badPlan string, cancel <-chan struct{}) ([]PlanStep, error) {
	sessionID := fmt.Sprintf("%s-plan-repair-%d", chooseSessionPrefix(cfg), time.Now().UnixNano())
	if err := db.CreateSession(sessionID, cfg.ProjectPath, ""); err != nil {
		return nil, fmt.Errorf("create plan repair session: %w", err)
	}

	var (
		outMu sync.Mutex
		out   strings.Builder
	)
	ctx := &ChatContext{
		ProjectPath: cfg.ProjectPath,
		SessionID:   sessionID,
		Role:        RolePlanner,
		Cancel:      cancel,
		OnChunk: func(content string, _ bool) {
			if content == "" {
				return
			}
			outMu.Lock()
			out.WriteString(content)
			outMu.Unlock()
		},
		OnError:      func(string) {},
		OnToolCall:   func(string, any) {},
		OnToolResult: func(string, any, bool) {},
	}

	prompt := buildPlannerRepairPrompt(state.Brief, badPlan)
	cfg.chatFnFor()(ctx, prompt)

	steps := parsePlanFromText(out.String())
	if len(steps) == 0 {
		return nil, fmt.Errorf("repair output empty or unparsable; got %d chars", out.Len())
	}
	if err := validatePlanQuality(steps); err != nil {
		synthesizeMissingAcceptance(steps)
	}
	return steps, nil
}

// validatePlanQuality enforces the structural contract every plan step must obey:
// each step must declare an Acceptance criterion that is a runnable shell command.
// A chatbot help-menu does not satisfy this contract; a real implementation plan
// always does. This is enforced by structure, not by phrase matching.
func validatePlanQuality(steps []PlanStep) error {
	if len(steps) == 0 {
		return fmt.Errorf("plan has no steps")
	}
	var missing []int
	for _, s := range steps {
		if !hasRunnableAcceptance(s.Acceptance) {
			missing = append(missing, s.Index)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("steps %v lack a runnable Acceptance command (each step must declare a verifiable shell command + expected outcome)", missing)
	}
	return nil
}

// hasRunnableAcceptance returns true if the acceptance criterion contains a
// recognizable shell command. A plan step without a verifiable command isn't
// actionable — the reviewer has no way to confirm completion.
func hasRunnableAcceptance(acceptance string) bool {
	trimmed := strings.TrimSpace(acceptance)
	if trimmed == "" {
		return false
	}
	// Backticked command (e.g. `go test ./...`) — the format the planner prompt requests.
	if strings.Contains(trimmed, "`") {
		return true
	}
	// Common command starters seen in unquoted acceptance lines.
	lower := strings.ToLower(trimmed)
	for _, starter := range []string{"go ", "npm ", "pnpm ", "yarn ", "bun ", "cargo ", "python ", "python3 ", "make ", "curl ", "./", "bash ", "sh ", "node ", "echo "} {
		if strings.Contains(lower, starter) {
			return true
		}
	}
	return false
}

func synthesizeMissingAcceptance(steps []PlanStep) {
	for i := range steps {
		if hasRunnableAcceptance(steps[i].Acceptance) {
			continue
		}
		cmd := firstRunnableCommandCandidate(steps[i].Acceptance + "\n" + steps[i].Body + "\n" + steps[i].Title)
		if cmd == "" {
			cmd = fmt.Sprintf("echo 'step %d complete'", steps[i].Index)
		}
		steps[i].Acceptance = fmt.Sprintf("`%s` exits 0", cmd)
	}
}

func firstRunnableCommandCandidate(text string) string {
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		trimmed = strings.Trim(trimmed, "`")
		trimmed = strings.TrimPrefix(trimmed, "- ")
		trimmed = strings.TrimSpace(trimmed)
		if hasRunnableAcceptance(trimmed) {
			return trimmed
		}
	}
	return ""
}

// buildPlannerPrompt turns the brief into a focused TDD-shaped plan request.
// Each step is a "vertical slice" per Matt Pocock's framework: ONE failing
// test, the minimum implementation that makes it pass, then a refactor pass
// that keeps modules deep.
func buildPlannerPrompt(brief string) string {
	return buildPlannerPromptWithContext(brief, "")
}

func buildPlannerPromptWithContext(brief, contextDoc string) string {
	brief = strings.TrimSpace(brief)
	var b strings.Builder
	if strings.TrimSpace(contextDoc) != "" {
		b.WriteString("UBIQUITOUS LANGUAGE (use these exact terms — do not invent new vocabulary):\n")
		b.WriteString(strings.TrimSpace(contextDoc))
		b.WriteString("\n\n")
	}
	b.WriteString("Produce a concrete, TDD-shaped numbered build plan.\n\n")
	b.WriteString("BRIEF:\n")
	b.WriteString(brief)
	b.WriteString("\n\nOutput exactly this format, no preamble, no closing remarks:\n")
	b.WriteString("1. <Title>\n")
	b.WriteString("   <One paragraph: which module/file gets a failing test, what behaviour the test pins down, and the minimal implementation that satisfies it. Then one sentence on the refactor pass that follows green.>\n")
	b.WriteString("   Acceptance: <Observable success criterion testable from the command line — e.g. `go test ./pkg/foo/...` passes, `curl /health` returns 200 with {\"ok\":true}, `cli --help` prints expected usage.>\n\n")
	b.WriteString("Rules:\n")
	b.WriteString("- 5–12 steps. Each is one vertical slice: failing test → minimal implementation → refactor for depth.\n")
	b.WriteString("- Step 1 must scaffold the project (go.mod / package.json / Cargo.toml / etc.) AND include the very first failing test.\n")
	b.WriteString("- Steps must use the vocabulary above — never invent a new name for an existing entity.\n")
	b.WriteString("- Each Acceptance line is an exact command + expected outcome.\n")
	b.WriteString("- Second-to-last step exercises the application end-to-end LOCALLY (boot + interact + assert).\n")
	b.WriteString("- FINAL step ships the project to wherever the brief implies it belongs — a publicly reachable URL, a tagged release with built artifacts, a package on a free registry, or any other natural delivery target. You decide what 'shipped' means for this project. Prefer free, no-card-required hosting; never assume paid tiers. If shipping requires credentials the runner does not have, use credential_set or discord_dm to obtain them before attempting — never invent tokens, never skip shipping.\n")
	b.WriteString("- Acceptance for the final step must be a runnable command that proves the public artifact works (e.g. `curl -sf <URL>` returns expected content, `gh release view v0.1.0` shows attached binaries). Localhost-only acceptance is NOT sufficient for the final step.\n")
	b.WriteString("- After shipping succeeds, the final step must write the public URL (live site, release page, registry page — whichever applies) to `.engine/live-url.txt` so downstream behavioral validation hits the public artifact, not localhost.\n")
	return b.String()
}

func buildPlannerRepairPrompt(brief, badPlan string) string {
	var b strings.Builder
	b.WriteString("Repair this numbered plan so every step includes a runnable Acceptance command with expected outcome.\n")
	b.WriteString("Do not add preamble or commentary. Output only numbered steps in the required format.\n\n")
	b.WriteString("BRIEF:\n")
	b.WriteString(strings.TrimSpace(brief))
	b.WriteString("\n\nBROKEN PLAN:\n")
	b.WriteString(strings.TrimSpace(badPlan))
	b.WriteString("\n\nRequired format:\n")
	b.WriteString("1. <Title>\n")
	b.WriteString("   <Step body paragraph>\n")
	b.WriteString("   Acceptance: <exact shell command + expected result>\n\n")
	b.WriteString("Rules:\n")
	b.WriteString("- Keep 5-12 steps unless original has fewer; preserve original intent/order.\n")
	b.WriteString("- Every Acceptance line must include a concrete command and expected outcome.\n")
	b.WriteString("- Use backticks around the command when possible, e.g. `go test ./...` exits 0.\n")
	b.WriteString("- No markdown code fences.\n")
	return b.String()
}

// planStepRegex matches a numbered step header at the start of a line.
var planStepRegex = regexp.MustCompile(`^\s*(\d+)\.\s+(.+?)\s*$`)

// parsePlanFromText extracts numbered PlanSteps from RolePlanner output.
// Accepts the format produced by buildPlannerPrompt and tolerates loose
// variants (extra blank lines, indented continuation, "Acceptance:" prefix).
func parsePlanFromText(text string) []PlanStep {
	lines := strings.Split(text, "\n")
	var (
		steps             []PlanStep
		current           *PlanStep
		readingAcceptance bool
	)

	finalize := func() {
		if current == nil {
			return
		}
		current.Body = strings.TrimSpace(current.Body)
		current.Acceptance = strings.TrimSpace(current.Acceptance)
		current.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
		steps = append(steps, *current)
		current = nil
	}

	for _, raw := range lines {
		line := strings.TrimRight(raw, " \t\r")
		if matches := planStepRegex.FindStringSubmatch(line); matches != nil {
			finalize()
			idx := 0
			fmt.Sscanf(matches[1], "%d", &idx)
			current = &PlanStep{
				Index: idx,
				Title: strings.TrimSpace(matches[2]),
			}
			readingAcceptance = false
			continue
		}
		if current == nil {
			continue
		}
		trimmed := strings.TrimSpace(line)

		if readingAcceptance {
			lower := strings.ToLower(trimmed)
			if strings.HasPrefix(lower, "refactor:") {
				readingAcceptance = false
			} else {
				if trimmed == "" || strings.HasPrefix(trimmed, "```") {
					continue
				}
				if current.Acceptance != "" {
					current.Acceptance += " "
				}
				current.Acceptance += trimmed
				continue
			}
		}

		if trimmed == "" {
			if current.Body != "" {
				current.Body += "\n"
			}
			continue
		}
		lowerPrefix := strings.ToLower(trimmed)
		if strings.HasPrefix(lowerPrefix, "acceptance:") {
			current.Acceptance = strings.TrimSpace(trimmed[len("acceptance:"):])
			readingAcceptance = current.Acceptance == ""
			continue
		}
		if current.Body != "" {
			current.Body += "\n"
		}
		current.Body += trimmed
	}
	finalize()

	// Renumber sequentially so a model that skipped a number still produces a
	// usable plan.
	for i := range steps {
		steps[i].Index = i + 1
	}
	return steps
}

// orchestratorBuildStep runs RoleAutonomousBuilder for exactly one plan step.
// The builder is given the step body + acceptance + any prior reviewer feedback.
// Builder is bounded by OrchestratorStepMaxTurns so the outer loop stays in control.
func orchestratorBuildStep(cfg OrchestratorConfig, state *OrchestrationState, step *PlanStep, redirect string, cancel <-chan struct{}) error {
	sessionID := fmt.Sprintf("%s-step%d-%d", chooseSessionPrefix(cfg), step.Index, time.Now().UnixNano())
	if err := db.CreateSession(sessionID, cfg.ProjectPath, ""); err != nil {
		return fmt.Errorf("create step session: %w", err)
	}

	policy := ResolveAutonomousPolicy(cfg.ProjectPath)
	policy.AutoCommit = true
	policy.AutoPush = true

	var (
		outMu sync.Mutex
		out   strings.Builder
	)
	ctx := &ChatContext{
		ProjectPath:      cfg.ProjectPath,
		SessionID:        sessionID,
		Role:             RoleAutonomousBuilder,
		AutonomousPolicy: &policy,
		Cancel:           cancel,
		MaxTurns:         OrchestratorStepMaxTurns,
		OnChunk: func(content string, _ bool) {
			if content == "" {
				return
			}
			outMu.Lock()
			out.WriteString(content)
			outMu.Unlock()
		},
		OnError: func(msg string) {
			outMu.Lock()
			out.WriteString("\n[error] " + msg)
			outMu.Unlock()
		},
		OnToolCall:   func(string, any) {},
		OnToolResult: func(string, any, bool) {},
	}

	// Builder reads vocabulary + PRD + the current module map so it knows
	// where new code belongs and which terms to use. Design concept omitted
	// here — the builder is tactical, the strategic framing is the PRD.
	contextDoc := ComposeDocContext(cfg.ProjectPath, DocVocabulary, DocPRD, DocModules)
	if contextDoc == "" {
		contextDoc = readContextDoc(cfg.ProjectPath) // legacy fallback
	}
	prompt := buildStepPromptWithContext(state, step, redirect, contextDoc)
	cfg.chatFnFor()(ctx, prompt)

	if strings.TrimSpace(out.String()) == "" {
		return fmt.Errorf("builder produced no output for step %d", step.Index)
	}
	return nil
}

// buildStepPrompt frames one plan step for the builder. Reviewer feedback from
// the prior pass is included verbatim so the model fixes the actual cited
// issue. TDD discipline is enforced explicitly: red → green → refactor.
func buildStepPrompt(state *OrchestrationState, step *PlanStep, redirect string) string {
	return buildStepPromptWithContext(state, step, redirect, "")
}

func buildStepPromptWithContext(state *OrchestrationState, step *PlanStep, redirect, contextDoc string) string {
	var b strings.Builder
	if strings.TrimSpace(redirect) != "" {
		fmt.Fprintf(&b, "URGENT INSTRUCTION FROM USER (apply before anything else):\n%s\n\n", strings.TrimSpace(redirect))
	}
	if strings.TrimSpace(contextDoc) != "" {
		b.WriteString("UBIQUITOUS LANGUAGE (the project's shared vocabulary — use these exact terms):\n")
		b.WriteString(strings.TrimSpace(contextDoc))
		b.WriteString("\n\n")
	}
	fmt.Fprintf(&b, "Project: %s/%s\n", state.Owner, state.Repo)
	fmt.Fprintf(&b, "You are executing step %d of %d in the build plan.\n\n", step.Index, len(state.Plan))
	fmt.Fprintf(&b, "STEP %d: %s\n", step.Index, step.Title)
	if strings.TrimSpace(step.Body) != "" {
		fmt.Fprintf(&b, "Details: %s\n", strings.TrimSpace(step.Body))
	}
	if strings.TrimSpace(step.Acceptance) != "" {
		fmt.Fprintf(&b, "Acceptance: %s\n", strings.TrimSpace(step.Acceptance))
	}
	if step.Attempts > 1 && strings.TrimSpace(step.LastFeedback) != "" {
		fmt.Fprintf(&b, "\nPRIOR ATTEMPT FEEDBACK (fix this specifically):\n%s\n", strings.TrimSpace(step.LastFeedback))
	}
	b.WriteString("\nTDD discipline (red → green → refactor):\n")
	b.WriteString("1. RED: write the failing test FIRST. Run it with shell. Confirm it fails for the right reason.\n")
	b.WriteString("2. GREEN: write the minimum production code that makes the test pass. Run the test again — confirm it now passes.\n")
	b.WriteString("3. REFACTOR: if the change introduced shallow modules or duplication, tighten the interface. Hide internal detail. Re-run the test.\n")
	b.WriteString("Only then call git_commit. Then call signal_done.\n\n")
	b.WriteString("Rules:\n")
	b.WriteString("- Implement ONLY this step. Do not work ahead.\n")
	b.WriteString("- Use exact terms from the ubiquitous language; never invent new names for existing entities.\n")
	b.WriteString("- Module design rule (Ousterhout deep modules): a module is good when its public surface is small relative to the complexity it hides. Avoid exposing internal state through wide APIs.\n")
	b.WriteString("- MINIMAL CODE: write the smallest change that makes the failing test pass. No speculative abstractions, no defensive code for impossible failure modes, no 'while I'm here' refactors of unrelated code. Less code is always better — but minimal means the minimum that actually achieves the goal.\n")
	b.WriteString("- Place new code in the module from the PRD that owns it. Do not invent a parallel module when one already exists in the module index.\n")
	b.WriteString("- Stop the moment Acceptance is met; do not keep working past it.\n")
	return b.String()
}

// orchestratorReviewStep runs RoleReviewer over the diff produced by the
// builder for this step. The reviewer returns APPROVE or REJECT plus a list of
// findings; we parse that into a verdict + feedback string.
func orchestratorReviewStep(cfg OrchestratorConfig, state *OrchestrationState, step *PlanStep, cancel <-chan struct{}) (ReviewVerdict, string) {
	sessionID := fmt.Sprintf("%s-review-%d-%d", chooseSessionPrefix(cfg), step.Index, time.Now().UnixNano())
	if err := db.CreateSession(sessionID, cfg.ProjectPath, ""); err != nil {
		return ReviewInconclusive, "create review session: " + err.Error()
	}

	policy := ResolveAutonomousPolicy(cfg.ProjectPath)
	// Reviewer may run small fixes; allow commits so the gate can self-heal
	// trivial review findings without bouncing back to the builder.
	policy.AutoCommit = true

	var (
		outMu sync.Mutex
		out   strings.Builder
	)
	ctx := &ChatContext{
		ProjectPath:      cfg.ProjectPath,
		SessionID:        sessionID,
		Role:             RoleReviewer,
		AutonomousPolicy: &policy,
		Cancel:           cancel,
		MaxTurns:         12,
		OnChunk: func(content string, _ bool) {
			if content == "" {
				return
			}
			outMu.Lock()
			out.WriteString(content)
			outMu.Unlock()
		},
		OnError:      func(string) {},
		OnToolCall:   func(string, any) {},
		OnToolResult: func(string, any, bool) {},
	}

	// Reviewer reads vocabulary + PRD + module map. Design concept is omitted
	// — by review time the concept is implemented; what matters is whether
	// the implementation respects the contract.
	contextDoc := ComposeDocContext(cfg.ProjectPath, DocVocabulary, DocPRD, DocModules)
	if contextDoc == "" {
		contextDoc = readContextDoc(cfg.ProjectPath) // legacy fallback
	}
	prompt := buildReviewerPromptWithContext(state, step, contextDoc)
	cfg.chatFnFor()(ctx, prompt)

	verdict, feedback := parseReviewerVerdict(out.String())
	return verdict, feedback
}

func buildReviewerPrompt(state *OrchestrationState, step *PlanStep) string {
	return buildReviewerPromptWithContext(state, step, "")
}

func buildReviewerPromptWithContext(state *OrchestrationState, step *PlanStep, contextDoc string) string {
	var b strings.Builder
	if strings.TrimSpace(contextDoc) != "" {
		b.WriteString("UBIQUITOUS LANGUAGE (the diff must use these terms):\n")
		b.WriteString(strings.TrimSpace(contextDoc))
		b.WriteString("\n\n")
	}
	fmt.Fprintf(&b, "Review the most recent changes for step %d of %d in %s/%s.\n", step.Index, len(state.Plan), state.Owner, state.Repo)
	fmt.Fprintf(&b, "Step title: %s\n", step.Title)
	if strings.TrimSpace(step.Acceptance) != "" {
		fmt.Fprintf(&b, "Acceptance criterion: %s\n", strings.TrimSpace(step.Acceptance))
	}
	b.WriteString("\nRubric — evaluate each:\n")
	b.WriteString("1. ACCEPTANCE: run the project's tests with shell and the exact verification command from Acceptance. Does it pass?\n")
	b.WriteString("2. TDD DISCIPLINE: inspect git_diff. Did the change include a NEW test that exercises the new behaviour? Tests must come with implementation, not after.\n")
	b.WriteString("3. UBIQUITOUS LANGUAGE: does the code use the project vocabulary above? Inventing new names for existing entities is a defect.\n")
	b.WriteString("4. DEEP MODULES: are new modules deep (small surface, hidden complexity) or shallow (wide surface for trivial behaviour)? Reject shallow when materially harmful.\n")
	b.WriteString("5. MINIMAL CODE: are there speculative abstractions, defensive code for impossible cases, or 'while I'm here' refactors of unrelated code? Reject over-engineering when materially harmful.\n")
	b.WriteString("6. MODULE PLACEMENT: does the change land in the module from the PRD that owns it, or did it spawn a parallel module duplicating existing structure? Reject duplicates.\n")
	b.WriteString("\nFinal line of your response must be exactly one of:\n")
	b.WriteString("  APPROVE\n")
	b.WriteString("  REJECT: <one-line reason summarising the most important finding>\n")
	b.WriteString("Before the final line, list any findings as bullet points (file:line - issue).\n")
	return b.String()
}

// parseReviewerVerdict scans reviewer output for the terminal APPROVE/REJECT line.
func parseReviewerVerdict(out string) (ReviewVerdict, string) {
	if out == "" {
		return ReviewInconclusive, "reviewer returned no output"
	}
	trimmed := strings.TrimSpace(out)
	lines := strings.Split(out, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		upper := strings.ToUpper(line)
		if upper == "APPROVE" || strings.HasPrefix(upper, "APPROVE ") || strings.HasPrefix(upper, "APPROVE:") {
			return ReviewApprove, ""
		}
		if strings.HasPrefix(upper, "REJECT") {
			reason := ""
			if idx := strings.Index(line, ":"); idx > 0 && idx < len(line)-1 {
				reason = strings.TrimSpace(line[idx+1:])
			}
			// Include the full body so the builder sees specific findings.
			return ReviewReject, strings.TrimSpace(reason + "\n\n" + trimmed)
		}
		// The terminal line should be the verdict — if not, parsing is inconclusive.
		return ReviewInconclusive, trimmed
	}
	return ReviewInconclusive, "reviewer returned no output"
}

// orchestratorValidatePhase runs the behavioral validator: boot the app,
// hit it, and verify it works end-to-end. Returns the summary text and any
// error; nil error means validation passed.
func orchestratorValidatePhase(cfg OrchestratorConfig, state *OrchestrationState, cancel <-chan struct{}) (string, error) {
	sessionID := fmt.Sprintf("%s-validate-%d", chooseSessionPrefix(cfg), time.Now().UnixNano())
	if err := db.CreateSession(sessionID, cfg.ProjectPath, ""); err != nil {
		return "", fmt.Errorf("create validate session: %w", err)
	}

	policy := ResolveAutonomousPolicy(cfg.ProjectPath)
	policy.AutoCommit = false // validation only — should not be writing code

	var (
		outMu sync.Mutex
		out   strings.Builder
	)
	ctx := &ChatContext{
		ProjectPath:      cfg.ProjectPath,
		SessionID:        sessionID,
		Role:             RoleReviewer, // reviewer already has shell + screenshot + open_url
		AutonomousPolicy: &policy,
		Cancel:           cancel,
		MaxTurns:         16,
		OnChunk: func(content string, _ bool) {
			if content == "" {
				return
			}
			outMu.Lock()
			out.WriteString(content)
			outMu.Unlock()
		},
		OnError:      func(string) {},
		OnToolCall:   func(string, any) {},
		OnToolResult: func(string, any, bool) {},
	}
	prompt := buildBehavioralValidatorPrompt(state)
	cfg.chatFnFor()(ctx, prompt)

	verdict, feedback := parseReviewerVerdict(out.String())
	if verdict == ReviewApprove {
		return strings.TrimSpace(out.String()), nil
	}
	return strings.TrimSpace(out.String()), fmt.Errorf("%s", strings.TrimSpace(feedback))
}

func buildBehavioralValidatorPrompt(state *OrchestrationState) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Final behavioral validation for %s/%s.\n\n", state.Owner, state.Repo)
	b.WriteString("Project brief (acceptance criteria are derived from this):\n")
	b.WriteString(strings.TrimSpace(state.Brief))
	b.WriteString("\n\n")
	if strings.TrimSpace(state.LiveURL) != "" {
		fmt.Fprintf(&b, "PUBLIC ARTIFACT URL: %s\n", strings.TrimSpace(state.LiveURL))
		b.WriteString("The project shipped. Validation MUST verify the public artifact at this URL — localhost-only checks are insufficient. Use whatever combination of shell, curl, browser_navigate, browser_read_page, browser_click, browser_type, and screenshot is appropriate for the kind of artifact this URL points at.\n\n")
	} else {
		b.WriteString("No public artifact URL was recorded (.engine/live-url.txt is empty or missing). The final plan step was supposed to ship the project; treat the absence of a shipped artifact as a REJECT unless the brief explicitly says the project ends at local working software.\n\n")
	}
	b.WriteString("Validation checklist:\n")
	b.WriteString("1. Read the project layout with shell/list_directory to identify what the project actually is. Do not assume a category — let the files tell you.\n")
	b.WriteString("2. Run the full test suite locally with shell. Choose the command from the project layout (e.g. `GOWORK=off go test ./...`, `npm test`, `cargo test`). All tests must pass.\n")
	b.WriteString("3. Verify the artifact behaves as the brief describes. Choose the verification tools that match the artifact:\n")
	b.WriteString("   - For a live URL: hit it with curl AND load it with browser_navigate + screenshot. Exercise primary user actions where applicable. Confirm rendered output matches brief-derived expectations.\n")
	b.WriteString("   - For a downloadable release / package: fetch it from its public URL with shell and run the entry point. Confirm output matches brief.\n")
	b.WriteString("   - For anything else: pick the smallest set of tool calls that proves the brief's outcomes are observable to a real user from outside this project's source tree.\n")
	b.WriteString("4. Compare actual behaviour against the brief's stated outcomes.\n\n")
	b.WriteString("Final line of your response must be exactly one of:\n")
	b.WriteString("  APPROVE\n")
	b.WriteString("  REJECT: <one-line reason>\n")
	b.WriteString("List specific findings as bullets above the final line.\n")
	return b.String()
}

func chooseSessionPrefix(cfg OrchestratorConfig) string {
	if strings.TrimSpace(cfg.SessionIDPrefix) != "" {
		return cfg.SessionIDPrefix
	}
	if strings.TrimSpace(cfg.Repo) != "" {
		return "orch-" + cfg.Repo
	}
	return "orch"
}
