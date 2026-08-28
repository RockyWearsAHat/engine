package ai

import (
	"errors"
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

	ctx, oc := newChatContextForRole(cfg, sessionID, RolePlanner, cancel)

	// Planner reads the design-concept + vocabulary + PRD layers — every
	// piece of documentation the orchestrator built before this phase.
	//
	// Deliberately NOT narrowed, unlike the build and review steps. Retrieval
	// pays when a prompt needs one part of a document and is charged for all
	// of it; the planner needs all of it, because the one thing a plan must
	// not do is omit a requirement nobody thought to ask about. It also runs
	// once per project rather than twice per step, so the whole-document read
	// costs a rounding error against the run.
	contextDoc := ComposeDocContext(cfg.ProjectPath, DocDesign, DocVocabulary, DocPRD)
	if contextDoc == "" {
		contextDoc = readContextDoc(cfg.ProjectPath) // legacy fallback
	}
	prompt := buildPlannerPromptWithContext(state.Brief, contextDoc)
	cfg.chatFnFor()(ctx, prompt)

	steps := parsePlanFromText(oc.String())
	if len(steps) == 0 {
		return fmt.Errorf("plan output empty or unparsable; got %d chars", len(oc.String()))
	}
	if err := validatePlanQuality(steps); err != nil {
		repaired, repairErr := orchestratorRepairPlanPhase(cfg, state, oc.String(), cancel)
		if repairErr != nil {
			return fmt.Errorf("plan rejected (chatbot output detected): %w; repair pass failed: %v", err, repairErr)
		}
		state.Plan = repaired
		return nil
	}

	// Plan gate: decomposition into single-concern steps. Max 2 passes.
	splitCount := 0
	for splitCount < 2 {
		decompositionErr := validatePlanDecomposition(steps)
		if decompositionErr == nil {
			break
		}
		if splitCount > 0 {
			// Second rejection — fail the gate.
			emit(cfg.OnPhase, "plan", fmt.Sprintf("plan: %d steps, split %d (rejected twice)", len(steps), splitCount))
			return fmt.Errorf("plan gate rejected (step decomposition): %w", decompositionErr)
		}
		// First rejection — ask planner to split.
		emit(cfg.OnPhase, "plan", fmt.Sprintf("plan: %d steps, split %d (replan)", len(steps), splitCount+1))
		improved, repairErr := orchestratorSplitPlanPhase(cfg, state, oc.String(), decompositionErr.Error(), cancel)
		if repairErr != nil {
			emit(cfg.OnPhase, "plan", fmt.Sprintf("plan split failed: %v", repairErr))
			return fmt.Errorf("plan split pass failed: %w", repairErr)
		}
		steps = improved
		splitCount++
	}

	state.Plan = steps
	emit(cfg.OnPhase, "plan", fmt.Sprintf("plan: %d steps, split %d", len(steps), splitCount))

	// Structure is sound — now run ONE adversarial completeness critique. A
	// well-formatted but weak plan passes validatePlanQuality; this gate asks
	// whether the plan is actually ambitious enough to satisfy the brief.
	verdict, gaps := orchestratorPlanCritiquePhase(cfg, state, oc.String(), cancel)
	if verdict == "INCOMPLETE" && strings.TrimSpace(gaps) != "" {
		if improved := orchestratorRegeneratePlanForGaps(cfg, state, gaps, cancel); len(improved) > 0 {
			state.Plan = improved
		}
	}
	return nil
}

// orchestratorPlanCritiquePhase runs one adversarial critique of planText against
// the brief. It returns ("COMPLETE","") when the plan is judged sufficient and
// ("INCOMPLETE", gaps) when it misses capabilities/requirements/edge-cases the
// brief demands. Any unparsable result fails open as ("COMPLETE","") so a parse
// miss never blocks a build.
func orchestratorPlanCritiquePhase(cfg OrchestratorConfig, state *OrchestrationState, planText string, cancel <-chan struct{}) (verdict string, gaps string) {
	sessionID := fmt.Sprintf("%s-plan-critique-%d", chooseSessionPrefix(cfg), time.Now().UnixNano())
	if err := db.CreateSession(sessionID, cfg.ProjectPath, ""); err != nil {
		return "COMPLETE", ""
	}

	ctx, oc := newChatContextForRole(cfg, sessionID, RolePlanner, cancel)

	prompt := buildPlanCritiquePrompt(state.Brief, planText)
	cfg.chatFnFor()(ctx, prompt)

	return parsePlanCritiqueVerdict(oc.String())
}

// parsePlanCritiqueVerdict inspects the final non-empty line of a critique
// response. "COMPLETE" → ("COMPLETE",""); "INCOMPLETE: <gaps>" → ("INCOMPLETE",
// gaps). Anything else fails open to ("COMPLETE","").
func parsePlanCritiqueVerdict(out string) (string, string) {
	lines := strings.Split(out, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		// The prompt asks for a backticked verdict line; tolerate stray backticks.
		line = strings.TrimSpace(strings.Trim(line, "`"))
		upper := strings.ToUpper(line)
		if upper == "COMPLETE" {
			return "COMPLETE", ""
		}
		if strings.HasPrefix(upper, "INCOMPLETE") {
			gaps := ""
			if idx := strings.Index(line, ":"); idx > 0 && idx < len(line)-1 {
				gaps = strings.TrimSpace(line[idx+1:])
			}
			return "INCOMPLETE", gaps
		}
		// Terminal line is neither verdict — fail open, do not block the build.
		return "COMPLETE", ""
	}
	return "COMPLETE", ""
}

// orchestratorRegeneratePlanForGaps regenerates the plan ONCE, appending the
// critique's gaps to the planner context. Returns the regenerated steps only if
// they pass structural validation; an empty result tells the caller to keep the
// original plan. Bounded to a single pass — never loops.
func orchestratorRegeneratePlanForGaps(cfg OrchestratorConfig, state *OrchestrationState, gaps string, cancel <-chan struct{}) []PlanStep {
	sessionID := fmt.Sprintf("%s-plan-regen-%d", chooseSessionPrefix(cfg), time.Now().UnixNano())
	if err := db.CreateSession(sessionID, cfg.ProjectPath, ""); err != nil {
		return nil
	}

	ctx, oc := newChatContextForRole(cfg, sessionID, RolePlanner, cancel)

	contextDoc := ComposeDocContext(cfg.ProjectPath, DocDesign, DocVocabulary, DocPRD)
	if contextDoc == "" {
		contextDoc = readContextDoc(cfg.ProjectPath) // legacy fallback
	}
	briefWithGaps := state.Brief + "\n\nADDRESS THESE GAPS THE PRIOR PLAN MISSED:\n" + strings.TrimSpace(gaps)
	prompt := buildPlannerPromptWithContext(briefWithGaps, contextDoc)
	cfg.chatFnFor()(ctx, prompt)

	steps := parsePlanFromText(oc.String())
	if len(steps) == 0 {
		return nil
	}
	if err := validatePlanQuality(steps); err != nil {
		return nil
	}
	return steps
}

// buildPlanCritiquePrompt frames the adversarial completeness review of a plan
// against its brief.
func buildPlanCritiquePrompt(brief, planText string) string {
	var b strings.Builder
	b.WriteString("You are reviewing a build plan against its brief. Identify any capability, requirement, edge case, or quality bar in the brief that the plan fails to deliver, and judge whether the plan is ambitious enough to fully satisfy the brief.\n\n")
	b.WriteString("BRIEF:\n")
	b.WriteString(strings.TrimSpace(brief))
	b.WriteString("\n\nPLAN:\n")
	b.WriteString(strings.TrimSpace(planText))
	b.WriteString("\n\nEnd your response with exactly one line: `COMPLETE` or `INCOMPLETE: <comma-separated concrete gaps>`.\n")
	return b.String()
}

// orchestratorRepairPlanPhase gives the planner one more chance when the first
// output is structurally close but fails the acceptance-command contract.
func orchestratorRepairPlanPhase(cfg OrchestratorConfig, state *OrchestrationState, badPlan string, cancel <-chan struct{}) ([]PlanStep, error) {
	sessionID := fmt.Sprintf("%s-plan-repair-%d", chooseSessionPrefix(cfg), time.Now().UnixNano())
	if err := db.CreateSession(sessionID, cfg.ProjectPath, ""); err != nil {
		return nil, fmt.Errorf("create plan repair session: %w", err)
	}

	ctx, oc := newChatContextForRole(cfg, sessionID, RolePlanner, cancel)

	prompt := buildPlannerRepairPrompt(state.Brief, badPlan)
	cfg.chatFnFor()(ctx, prompt)

	steps := parsePlanFromText(oc.String())
	if len(steps) == 0 {
		return nil, fmt.Errorf("repair output empty or unparsable; got %d chars", len(oc.String()))
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

// validatePlanDecomposition enforces the task decomposition contract:
// each step is ≤1 file cluster, ≤1 acceptance check, doable by haiku in 30 min.
// Rejects steps that combine multiple concerns with "and then"/"also" or >3 verbs.
func validatePlanDecomposition(steps []PlanStep) error {
	var issues []string

	for _, s := range steps {
		combined := s.Title + " " + s.Body

		// Check for "and then" or "also" — signals of combined concerns.
		if strings.Contains(strings.ToLower(combined), " and then ") || strings.Contains(strings.ToLower(combined), " also ") {
			issues = append(issues, fmt.Sprintf("step %d: contains 'and then' or 'also' (split into separate steps)", s.Index))
		}

		// Count action verbs (heuristic: simple words likely verbs + common action starters).
		verbCount := countActionVerbs(combined)
		if verbCount > 3 {
			issues = append(issues, fmt.Sprintf("step %d: %d verbs (max 3 per step; split into smaller steps)", s.Index, verbCount))
		}
	}

	if len(issues) > 0 {
		return fmt.Errorf("plan decomposition violations:\n  %s", strings.Join(issues, "\n  "))
	}
	return nil
}

// countActionVerbs estimates action verbs in text. Counts common TDD-shaped verbs
// and known verb starters to detect oversized steps. Not exhaustive — aims to catch
// multi-concern steps like "add migration and then wire UI and write docs".
func countActionVerbs(text string) int {
	lower := strings.ToLower(text)
	verbs := []string{
		"add", "write", "create", "build", "implement", "scaffold", "wire",
		"modify", "update", "fix", "patch", "refactor", "optimize",
		"test", "verify", "validate", "review", "check", "assert",
		"run", "execute", "deploy", "publish", "ship",
		"read", "fetch", "load", "parse", "extract",
		"handle", "process", "transform", "convert", "map",
	}

	count := 0
	for _, verb := range verbs {
		// Count each occurrence of " verb " (word boundaries).
		pattern := " " + verb + " "
		count += strings.Count(" "+lower+" ", pattern)
	}
	return count
}

// orchestratorSplitPlanPhase asks the planner to split a plan that failed decomposition.
// The planner receives the original plan, the decomposition error, and the brief;
// it must produce a new plan where each step is ≤1 concern.
func orchestratorSplitPlanPhase(cfg OrchestratorConfig, state *OrchestrationState, badPlanText, decompositionError string, cancel <-chan struct{}) ([]PlanStep, error) {
	sessionID := fmt.Sprintf("%s-plan-split-%d", chooseSessionPrefix(cfg), time.Now().UnixNano())
	if err := db.CreateSession(sessionID, cfg.ProjectPath, ""); err != nil {
		return nil, fmt.Errorf("create plan split session: %w", err)
	}

	ctx, oc := newChatContextForRole(cfg, sessionID, RolePlanner, cancel)

	prompt := buildPlanSplitPrompt(state.Brief, badPlanText, decompositionError)
	cfg.chatFnFor()(ctx, prompt)

	steps := parsePlanFromText(oc.String())
	if len(steps) == 0 {
		return nil, fmt.Errorf("split output empty or unparsable; got %d chars", len(oc.String()))
	}
	if err := validatePlanQuality(steps); err != nil {
		return nil, fmt.Errorf("split plan lacks acceptance commands: %w", err)
	}
	return steps, nil
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
	b.WriteString("Numbered build plan. TDD shape. Caveman style.\n\n")
	b.WriteString("BRIEF:\n")
	b.WriteString(brief)
	b.WriteString("\n\nFormat (no preamble, no remarks):\n")
	b.WriteString("1. <Title>\n")
	b.WriteString("   <One paragraph: module/file, failing test, minimal impl, refactor.>\n")
	b.WriteString("   Acceptance: <Shell command + expected outcome.>\n\n")
	b.WriteString("Rules:\n")
	b.WriteString("- 5–12 steps. Each is one vertical slice: failing test → minimal implementation → refactor for depth.\n")
	b.WriteString("- Each step ≤ 30 min for haiku, ≤ 1 file cluster, ≤ 1 acceptance check. NO 'and then', NO 'also'. Max 3 action verbs per step.\n")
	b.WriteString("- Step 1 scaffolds the project (go.mod / package.json / Cargo.toml / etc.) AND the very first failing test.\n")
	b.WriteString("- Use vocabulary above — never invent new names.\n")
	b.WriteString("- Each Acceptance is an exact shell command + outcome.\n")
	b.WriteString("- Second-to-last step: full end-to-end locally (boot + interact + assert).\n")
	b.WriteString("- FINAL step ships public (URL, release, registry, etc.). Acceptance must prove public artifact works (e.g. `curl -sf <URL>` or `gh release view v0.1.0`). NOT localhost-only. After success: write URL to `.engine/live-url.txt`.\n")
	return b.String()
}

func buildPlanSplitPrompt(brief, badPlanText, decompositionError string) string {
	var b strings.Builder
	b.WriteString("Split this plan. Each step must be ≤ 1 concern, ≤ 30 min haiku, ≤ 1 acceptance check. NO 'and then', NO 'also', max 3 action verbs per step.\n\n")
	b.WriteString("BRIEF:\n")
	b.WriteString(strings.TrimSpace(brief))
	b.WriteString("\n\nPLAN TO SPLIT:\n")
	b.WriteString(strings.TrimSpace(badPlanText))
	b.WriteString("\n\nREASONS TO SPLIT:\n")
	b.WriteString(strings.TrimSpace(decompositionError))
	b.WriteString("\n\nOutput only numbered steps in required format. No preamble.\n")
	b.WriteString("1. <Title>\n")
	b.WriteString("   <One paragraph.>\n")
	b.WriteString("   Acceptance: <Shell command + outcome.>\n\n")
	b.WriteString("Keep 5-12 steps; split oversized steps into smaller ones. Preserve original intent/order. Every step must pass decomposition (single concern, no 'and then'/'also', ≤3 verbs).\n")
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
// ErrProviderZeroOutput: builder run died in under zeroOutputWindow with zero
// output tokens. Provider/CLI fault, not model fault. Outer loop refunds the
// attempt (err-…002: haiku "zero-output 1s" after REJECT burned attempts).
var ErrProviderZeroOutput = errors.New("provider returned zero output tokens")

// zeroOutputWindow: a run that ends this fast with no tokens never reached a
// model. zeroOutputBackoff: waits between retries. Vars so tests pin them.
var (
	zeroOutputWindow  = time.Second
	zeroOutputBackoff = []time.Duration{time.Second, 5 * time.Second, 15 * time.Second}
	// buildSleepFn is the backoff sleep; cancel-aware. Tests stub it.
	buildSleepFn = func(d time.Duration, cancel <-chan struct{}) bool {
		select {
		case <-time.After(d):
			return true
		case <-cancel:
			return false
		}
	}
)

// runBuilderOnce: fresh session + fresh ChatContext, one provider run. Returns
// collected output and the run's stats (Seen=false when provider gave none).
func runBuilderOnce(cfg OrchestratorConfig, state *OrchestrationState, step *PlanStep, redirect string, cancel <-chan struct{}) (string, RunStats, time.Duration, error) {
	sessionID := fmt.Sprintf("%s-step%d-%d", chooseSessionPrefix(cfg), step.Index, time.Now().UnixNano())
	if err := db.CreateSession(sessionID, cfg.ProjectPath, ""); err != nil {
		return "", RunStats{}, 0, fmt.Errorf("create step session: %w", err)
	}

	policy := ResolveAutonomousPolicy(cfg.ProjectPath)
	policy.AutoCommit = true
	policy.AutoPush = true

	ctx, oc := newChatContextForRole(cfg, sessionID, RoleAutonomousBuilder, cancel)
	ctx.AutonomousPolicy = &policy
	ctx.MaxTurns = OrchestratorStepMaxTurns
	ctx.OnError = func(msg string) {
		oc.Write("\n[error] " + msg)
	}
	var stats RunStats
	var statsMu sync.Mutex
	outer := ctx.OnRunStats
	ctx.OnRunStats = func(s RunStats) {
		statsMu.Lock()
		stats = s
		statsMu.Unlock()
		if outer != nil {
			outer(s)
		}
	}

	// Builder reads vocabulary + PRD + module map, narrowed to this step. The
	// step is the query: most of the PRD describes work this step is not doing.
	contextDoc := ComposeDocContextFocused(cfg.ProjectPath, stepQuery(step), DocVocabulary, DocPRD, DocModules)
	if contextDoc == "" {
		contextDoc = readContextDoc(cfg.ProjectPath) // legacy fallback
	}
	prompt := buildStepPromptWithContext(state, step, redirect, contextDoc)
	started := time.Now()
	cfg.chatFnFor()(ctx, prompt)
	statsMu.Lock()
	defer statsMu.Unlock()
	return oc.String(), stats, time.Since(started), nil
}

// orchestratorBuildStep runs the builder for one step.
//
// Every attempt is a fresh session and fresh context; reviewer notes ride in
// via step.LastFeedback (see buildStepPromptWithContext). Token count logged
// per run. A run with zero output tokens inside zeroOutputWindow is a provider
// fault: back off (zeroOutputBackoff), retry, then ErrProviderZeroOutput —
// caller refunds the attempt.
func orchestratorBuildStep(cfg OrchestratorConfig, state *OrchestrationState, step *PlanStep, redirect string, cancel <-chan struct{}) error {
	for try := 0; ; try++ {
		out, stats, elapsed, err := runBuilderOnce(cfg, state, step, redirect, cancel)
		if err != nil {
			return err
		}
		emit(cfg.OnPhase, "tokens", fmt.Sprintf("step %d run %d: model=%s in=%d out=%d elapsed=%s",
			step.Index, try+1, stats.Model, stats.InputTokens, stats.OutputTokens, elapsed.Round(time.Millisecond)))

		// Only a provider that REPORTED usage can be judged a provider fault.
		// Stubs/providers without usage keep the old "no output" path.
		providerFault := stats.Seen && stats.OutputTokens == 0 && elapsed < zeroOutputWindow && !cancelClosed(cancel)
		if !providerFault {
			if strings.TrimSpace(out) == "" {
				return fmt.Errorf("builder produced no output for step %d", step.Index)
			}
			return nil
		}
		if try >= len(zeroOutputBackoff) {
			return fmt.Errorf("step %d: %w after %d retries", step.Index, ErrProviderZeroOutput, try)
		}
		wait := zeroOutputBackoff[try]
		emit(cfg.OnPhase, "provider", fmt.Sprintf("step %d: zero output in %s — provider fault, backoff %s (retry %d/%d, attempt not spent)",
			step.Index, elapsed.Round(time.Millisecond), wait, try+1, len(zeroOutputBackoff)))
		if !buildSleepFn(wait, cancel) {
			return fmt.Errorf("step %d: %w (cancelled during backoff)", step.Index, ErrProviderZeroOutput)
		}
	}
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
		fmt.Fprintf(&b, "\nREVIEWER NOTES from the prior attempt (fix these first, then re-check Acceptance):\n%s\n", strings.TrimSpace(step.LastFeedback))
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
	return orchestratorReviewStepBatch(cfg, state, step, 1, cancel)
}

// orchestratorReviewStepBatch is orchestratorReviewStep generalised to cover
// more than one already-committed step in a single review call, for callers
// that want to consolidate review work. Not currently exercised with
// stepsCovered > 1 — every step is reviewed individually — but kept general
// since the builder auto-commits after every step, so a caller reviewing N
// steps at once must be told explicitly to inspect the last N commits (git
// log/diff HEAD~N..HEAD) rather than rely on the git_diff tool, which only
// ever sees uncommitted changes.
func orchestratorReviewStepBatch(cfg OrchestratorConfig, state *OrchestrationState, step *PlanStep, stepsCovered int, cancel <-chan struct{}) (ReviewVerdict, string) {
	sessionID := fmt.Sprintf("%s-review-%d-%d", chooseSessionPrefix(cfg), step.Index, time.Now().UnixNano())
	if err := db.CreateSession(sessionID, cfg.ProjectPath, ""); err != nil {
		return ReviewInconclusive, "create review session: " + err.Error()
	}

	policy := ResolveAutonomousPolicy(cfg.ProjectPath)
	// Reviewer may run small fixes; allow commits so the gate can self-heal
	// trivial review findings without bouncing back to the builder.
	policy.AutoCommit = true

	ctx, oc := newChatContextForRole(cfg, sessionID, RoleReviewer, cancel)
	ctx.AutonomousPolicy = &policy
	ctx.MaxTurns = 12

	// Reviewer reads vocabulary + PRD + module map. Design concept is omitted
	// — by review time the concept is implemented; what matters is whether
	// the implementation respects the contract. Narrowed to the step under
	// review, for the same reason the builder's is.
	contextDoc := ComposeDocContextFocused(cfg.ProjectPath, stepQuery(step), DocVocabulary, DocPRD, DocModules)
	if contextDoc == "" {
		contextDoc = readContextDoc(cfg.ProjectPath) // legacy fallback
	}
	prompt := buildReviewerPromptWithContextBatch(state, step, contextDoc, stepsCovered)
	cfg.chatFnFor()(ctx, prompt)

	verdict, feedback := parseReviewerVerdict(oc.String())
	return verdict, feedback
}

func buildReviewerPrompt(state *OrchestrationState, step *PlanStep) string {
	return buildReviewerPromptWithContext(state, step, "")
}

func buildReviewerPromptWithContext(state *OrchestrationState, step *PlanStep, contextDoc string) string {
	return buildReviewerPromptWithContextBatch(state, step, contextDoc, 1)
}

func buildReviewerPromptWithContextBatch(state *OrchestrationState, step *PlanStep, contextDoc string, stepsCovered int) string {
	var b strings.Builder
	if strings.TrimSpace(contextDoc) != "" {
		b.WriteString("UBIQUITOUS LANGUAGE (the diff must use these terms):\n")
		b.WriteString(strings.TrimSpace(contextDoc))
		b.WriteString("\n\n")
	}
	if stepsCovered > 1 {
		fmt.Fprintf(&b, "Review the last %d commits (run `git log -%d --stat` then `git diff HEAD~%d..HEAD` with shell — the builder auto-commits every step, so the git_diff tool alone will only show you the most recent one) covering steps ending at step %d of %d in %s/%s.\n", stepsCovered, stepsCovered, stepsCovered, step.Index, len(state.Plan), state.Owner, state.Repo)
	} else {
		fmt.Fprintf(&b, "Review the most recent changes for step %d of %d in %s/%s.\n", step.Index, len(state.Plan), state.Owner, state.Repo)
	}
	fmt.Fprintf(&b, "Step title: %s\n", step.Title)
	if strings.TrimSpace(step.Acceptance) != "" {
		fmt.Fprintf(&b, "Acceptance criterion: %s\n", strings.TrimSpace(step.Acceptance))
	}
	b.WriteString("\nRubric — evaluate each:\n")
	b.WriteString("1. ACCEPTANCE: run the project's tests with shell and the exact verification command from Acceptance. Does it pass?\n")
	b.WriteString("2. TDD DISCIPLINE: inspect the diff (see above for how to view it across multiple commits). Did the change include a NEW test that exercises the new behaviour? Tests must come with implementation, not after.\n")
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

	ctx, oc := newChatContextForRole(cfg, sessionID, RoleReviewer, cancel)
	ctx.AutonomousPolicy = &policy
	ctx.MaxTurns = 16

	prompt := buildBehavioralValidatorPrompt(state)
	cfg.chatFnFor()(ctx, prompt)

	verdict, feedback := parseReviewerVerdict(oc.String())
	if verdict == ReviewApprove {
		return strings.TrimSpace(oc.String()), nil
	}
	return strings.TrimSpace(oc.String()), fmt.Errorf("%s", strings.TrimSpace(feedback))
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
