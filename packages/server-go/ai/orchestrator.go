package ai

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// OrchestratorMaxOuterIterations bounds the planner→builder→critic→test loop
// so a permanently failing project gives up instead of looping forever.
// 200 is roughly 10x the old per-attempt cap, large enough for real projects
// while still bounded.
var OrchestratorMaxOuterIterations = 200

// OrchestratorStepMaxTurns bounds how long a single builder call works on one
// plan step before the orchestrator takes back control. Keeps each builder
// turn focused and prevents runaway sessions.
var OrchestratorStepMaxTurns = 24

// OrchestratorMaxStepAttempts caps how many times the orchestrator will retry
// a single plan step before skipping it. Without this, a step the model can't
// satisfy (e.g. because of an XML/tool-call mismatch or a flaky acceptance
// criterion) eats every remaining outer iteration and the engine sits churning
// on one step for an hour with nothing reaching disk. After this many attempts
// the step is marked done with a "skipped" feedback note so the loop advances;
// the behavioral validator at the end will surface the gap.
var OrchestratorMaxStepAttempts = 5

var orchestratorPausePollInterval = 2 * time.Second

// orchestrationFile is the persisted plan + step state. Lives at
// <project>/.engine/orchestration.json. Survives restarts.
const orchestrationFile = "orchestration.json"

// PlanStep is one numbered, acceptance-criteria-bearing step in the plan.
type PlanStep struct {
	Index       int    `json:"index"`
	Title       string `json:"title"`
	Body        string `json:"body"`
	Acceptance  string `json:"acceptance,omitempty"`
	Done        bool   `json:"done"`
	Attempts    int    `json:"attempts"`
	LastFeedback string `json:"lastFeedback,omitempty"`
	UpdatedAt   string `json:"updatedAt,omitempty"`
}

// OrchestrationState is the persistent project-level orchestration record.
type OrchestrationState struct {
	Repo            string     `json:"repo"`
	Owner           string     `json:"owner,omitempty"`
	Brief           string     `json:"brief,omitempty"`
	Plan            []PlanStep `json:"plan"`
	OuterIterations int        `json:"outerIterations"`
	StartedAt       string     `json:"startedAt"`
	UpdatedAt       string     `json:"updatedAt"`
	CompletedAt     string     `json:"completedAt,omitempty"`
	LastValidation  string     `json:"lastValidation,omitempty"`
	// LiveURL is the publicly reachable URL produced by the final deploy step.
	// Written by the builder via .engine/live-url.txt and lifted into state so
	// the behavioral validator hits the deployed instance instead of localhost.
	LiveURL string `json:"liveUrl,omitempty"`
	// Conversational marks a run that the orchestrator routed to a single
	// interactive chat turn instead of the build pipeline. It is transient —
	// never persisted — and tells the caller to suppress the build summary.
	Conversational bool `json:"-"`
}

// OrchestratorConfig is the per-run knobs and callbacks.
type OrchestratorConfig struct {
	ProjectPath string
	Owner       string
	Repo        string
	Brief       string
	// SessionIDPrefix becomes the session id base; per-phase sessions append a suffix.
	SessionIDPrefix string

	// MaxOuterIterations overrides the package default when > 0.
	MaxOuterIterations int

	// Cancel, when closed, stops the orchestrator at the next safe checkpoint.
	Cancel <-chan struct{}

	// ChatFn is the per-call chat dispatch. When nil, the orchestrator uses
	// the package default (ai.Chat). Tests override this to substitute a stub.
	// Wiring it through the config keeps a single injection seam reachable
	// from main.go so its aiChatFn variable is still the test-stub point.
	ChatFn func(ctx *ChatContext, userMessage string)

	// InteractiveChat handles a RouteConversational brief. When the orchestrator
	// triages a brief as conversational, it calls this instead of running the
	// build pipeline, handing back full control to the caller's interactive
	// chat surface (streaming tokens, tool calls, approvals straight to the
	// client). When nil, the orchestrator falls back to runConversationalTurn,
	// a headless single-turn reply used by tests and non-UI callers.
	InteractiveChat func(brief string, cancel <-chan struct{})

	// OnProgress is called with human-readable progress lines (Discord-grade).
	OnProgress func(message string)
	// OnPhase is called when entering a new phase: "plan", "execute", "review", "validate", "done".
	OnPhase func(phase, detail string)
	// OnPlanUpdate is called whenever the plan file is rewritten.
	OnPlanUpdate func(state *OrchestrationState)
	// OnError is called on terminal failures.
	OnError func(message string)
}

// chatFnFor returns the effective chat dispatch for cfg, falling back to the
// package default. Centralised so phases all resolve it the same way.
func (cfg OrchestratorConfig) chatFnFor() func(*ChatContext, string) {
	if cfg.ChatFn != nil {
		return cfg.ChatFn
	}
	return runChatFn
}

// orchestratorSessionRegistry tracks live orchestrator sessions so Discord
// (or any other control plane) can stop / pause / redirect them by project.
var (
	orchSessionsMu sync.Mutex
	orchSessions   = map[string]*OrchestratorHandle{}
)

type OrchestratorHandle struct {
	projectPath  string
	cancel       chan struct{}
	cancelOnce   sync.Once
	paused       bool
	redirectMu   sync.Mutex
	redirectQueue []string
	approveCh    chan bool
}

const maxQueuedRedirects = 24

// NewHandle returns a freshly-initialized OrchestratorHandle for projectPath.
// Useful for tests and external orchestration tooling that need a stoppable handle.
func NewHandle(projectPath string) *OrchestratorHandle {
	return &OrchestratorHandle{
		projectPath: projectPath,
		cancel:      make(chan struct{}),
		approveCh:   make(chan bool, 1),
	}
}

// GetOrchestratorHandle returns the live handle for a project, or nil.
// Discord commands use this to cancel/pause/redirect.
func GetOrchestratorHandle(projectPath string) *OrchestratorHandle {
	orchSessionsMu.Lock()
	defer orchSessionsMu.Unlock()
	return orchSessions[strings.TrimSpace(projectPath)]
}

// ListOrchestratorProjects returns the project paths with active orchestrators.
func ListOrchestratorProjects() []string {
	orchSessionsMu.Lock()
	defer orchSessionsMu.Unlock()
	out := make([]string, 0, len(orchSessions))
	for p := range orchSessions {
		out = append(out, p)
	}
	return out
}

// Stop closes the cancel channel; the orchestrator exits at the next checkpoint.
func (h *OrchestratorHandle) Stop() {
	if h == nil {
		return
	}
	h.cancelOnce.Do(func() { close(h.cancel) })
}

// Pause asks the orchestrator to hold at the next checkpoint until Resume.
func (h *OrchestratorHandle) Pause() {
	if h == nil {
		return
	}
	h.redirectMu.Lock()
	defer h.redirectMu.Unlock()
	h.paused = true
}

// Resume clears a Pause.
func (h *OrchestratorHandle) Resume() {
	if h == nil {
		return
	}
	h.redirectMu.Lock()
	defer h.redirectMu.Unlock()
	h.paused = false
}

// Redirect injects a new instruction the orchestrator picks up at the next
// step boundary. The instruction is prepended to the next builder prompt.
func (h *OrchestratorHandle) Redirect(message string) {
	if h == nil {
		return
	}
	trimmed := strings.TrimSpace(message)
	if trimmed == "" {
		return
	}
	h.redirectMu.Lock()
	defer h.redirectMu.Unlock()
	if n := len(h.redirectQueue); n > 0 && h.redirectQueue[n-1] == trimmed {
		return
	}
	h.redirectQueue = append(h.redirectQueue, trimmed)
	if len(h.redirectQueue) > maxQueuedRedirects {
		h.redirectQueue = h.redirectQueue[len(h.redirectQueue)-maxQueuedRedirects:]
	}
}

func (h *OrchestratorHandle) takeRedirect() string {
	if h == nil {
		return ""
	}
	h.redirectMu.Lock()
	defer h.redirectMu.Unlock()
	if len(h.redirectQueue) == 0 {
		return ""
	}
	if len(h.redirectQueue) == 1 {
		msg := h.redirectQueue[0]
		h.redirectQueue = nil
		return msg
	}
	var b strings.Builder
	b.WriteString("Queued external directives (apply in order):")
	for i, msg := range h.redirectQueue {
		b.WriteString("\n")
		b.WriteString(fmt.Sprintf("%d. %s", i+1, msg))
	}
	h.redirectQueue = nil
	return b.String()
}

func (h *OrchestratorHandle) isPaused() bool {
	if h == nil {
		return false
	}
	h.redirectMu.Lock()
	defer h.redirectMu.Unlock()
	return h.paused
}

func orchestratorPauseStep(cancel <-chan struct{}, handle *OrchestratorHandle) (bool, error) {
	if cancelClosed(cancel) {
		return false, fmt.Errorf("orchestrator: cancelled")
	}
	return orchestratorPausedStep(cancel, handle)
}

func orchestratorPausedStep(cancel <-chan struct{}, handle *OrchestratorHandle) (bool, error) {
	if !handle.isPaused() {
		return false, nil
	}
	if cancelClosed(cancel) {
		return false, fmt.Errorf("orchestrator: cancelled while paused")
	}
	time.Sleep(orchestratorPausePollInterval)
	return true, nil
}

func registerOrchestratorHandle(projectPath string, h *OrchestratorHandle) {
	orchSessionsMu.Lock()
	defer orchSessionsMu.Unlock()
	orchSessions[strings.TrimSpace(projectPath)] = h
}

func deregisterOrchestratorHandle(projectPath string) {
	orchSessionsMu.Lock()
	defer orchSessionsMu.Unlock()
	delete(orchSessions, strings.TrimSpace(projectPath))
}

// runChatFn is the injectable Chat invocation point. Tests replace it; the
// orchestrator never calls Chat directly through the global symbol.
var runChatFn = Chat

// RunAutonomousProject is the new top-level autonomous-build entry point.
// It replaces the inline 2-attempt loop in main.go's triggerScaffoldSession.
//
// Phases:
//
//  1. Plan: invoke RolePlanner with the brief; persist the numbered plan to
//     <project>/.engine/plan.md and orchestration.json.
//  2. Execute: for each unchecked plan step, invoke RoleAutonomousBuilder
//     bounded to that step. Reviewer + behavioral checks gate completion.
//  3. Validate: behavioral validation runs after the plan is fully checked.
//  4. Done: write completion timestamp + post a final summary.
//
// The function blocks until the project is complete, the cancel channel fires,
// or MaxOuterIterations is hit. Resumability: when called against a project
// with an existing orchestration.json, it picks up at the first unchecked step.
func RunAutonomousProject(cfg OrchestratorConfig) (*OrchestrationState, error) {
	if strings.TrimSpace(cfg.ProjectPath) == "" {
		return nil, fmt.Errorf("orchestrator: project path is required")
	}

	// Routing is the orchestrator's first job — and it happens before any
	// build machinery spins up. Reason about whether this brief is a
	// conversational turn or a build directive. A conversational route answers
	// in one interactive chat turn and never registers a handle, loads
	// orchestration state, or writes to .engine — so a plain "hi" leaves no
	// build artifacts behind. Empty briefs fall through to the build pipeline
	// (the historical default), preserving callers that drive state only.
	if strings.TrimSpace(cfg.Brief) != "" {
		if orchestratorTriageFn(cfg, cfg.Brief, cfg.Cancel) == RouteConversational {
			emit(cfg.OnPhase, "chat", "answering conversationally — skipping build pipeline")
			if cfg.InteractiveChat != nil {
				cfg.InteractiveChat(cfg.Brief, cfg.Cancel)
			} else {
				runConversationalTurn(cfg, cfg.Brief, cfg.Cancel)
			}
			return &OrchestrationState{
				Owner:          cfg.Owner,
				Repo:           cfg.Repo,
				Brief:          cfg.Brief,
				Conversational: true,
			}, nil
		}
	}

	if strings.TrimSpace(cfg.Owner) != "" && strings.TrimSpace(cfg.Repo) != "" {
		activeTeam := strings.TrimSpace(os.Getenv("ENGINE_ACTIVE_TEAM"))
		resolvedTeam, teamProvider, teamModel, ok := ResolveAutonomousStartupTeam(cfg.ProjectPath, activeTeam)
		if ok {
			// Startup bootstrap is lead-owned: pin the autonomous session to the
			// selected team's orchestrator runtime before any planning starts.
			os.Setenv("ENGINE_ACTIVE_TEAM", resolvedTeam)    //nolint:errcheck
			os.Setenv("ENGINE_MODEL_PROVIDER", teamProvider) //nolint:errcheck
			os.Setenv("ENGINE_MODEL", teamModel)             //nolint:errcheck
		}
	}
	if cfg.MaxOuterIterations <= 0 {
		cfg.MaxOuterIterations = OrchestratorMaxOuterIterations
	}

	// Route through event orchestrator if enabled (feature flag)
	if os.Getenv("USE_EVENT_ORCHESTRATOR") == "1" {
		return RunEventOrchestratorAsState(cfg)
	}

	handle := &OrchestratorHandle{
		projectPath: cfg.ProjectPath,
		cancel:      make(chan struct{}),
		approveCh:   make(chan bool, 1),
	}
	registerOrchestratorHandle(cfg.ProjectPath, handle)
	defer deregisterOrchestratorHandle(cfg.ProjectPath)

	// Combine the caller's cancel with the handle's internal cancel into one
	// channel the inner loop watches.
	cancel := orchestratorMergedCancel(cfg.Cancel, handle.cancel)

	state, err := loadOrCreateOrchestrationState(cfg.ProjectPath, cfg.Owner, cfg.Repo, cfg.Brief)
	if err != nil {
		return nil, fmt.Errorf("orchestrator: load state: %w", err)
	}

	// Intake / grill — walks the design tree, persists .engine/design.md.
	// PRD distillation — splits design.md into vocabulary.md + prd.md.
	// Both run once per project; resumed sessions reuse the existing files.
	// Failures are non-fatal — the orchestrator continues with whatever
	// layers exist, just less precisely.
	emit(cfg.OnPhase, "intake", "walking the design tree")
	designDoc, intakeErr := orchestratorIntakePhase(cfg, state, cancel)
	if intakeErr != nil {
		emitErr(cfg.OnError, fmt.Sprintf("intake phase: %v", intakeErr))
	}
	if strings.TrimSpace(designDoc) != "" {
		emit(cfg.OnPhase, "intake", fmt.Sprintf("design.md ready (%d chars)", len(designDoc)))
	}

	emit(cfg.OnPhase, "prd", "distilling vocabulary and module-aware PRD")
	if prdErr := orchestratorPRDPhase(cfg, cancel); prdErr != nil {
		emitErr(cfg.OnError, fmt.Sprintf("PRD phase: %v", prdErr))
	} else {
		emit(cfg.OnPhase, "prd", "vocabulary.md + prd.md ready")
	}

	// If a plan was loaded from disk, validate its quality before resuming.
	// A low-quality plan (e.g. chatbot help-menu) must be regenerated.
	if len(state.Plan) > 0 {
		if err := validatePlanQuality(state.Plan); err != nil {
			emitErr(cfg.OnError, fmt.Sprintf("loaded plan failed quality check (%v) — regenerating", err))
			state.Plan = nil
		}
	}

	if len(state.Plan) == 0 {
		emit(cfg.OnPhase, "plan", "generating TDD-style plan from brief + context")
		if err := orchestratorPlanPhase(cfg, state, cancel); err != nil {
			emitErr(cfg.OnError, fmt.Sprintf("plan phase failed: %v", err))
			return state, err
		}
		if err := persistOrchestration(cfg.ProjectPath, state); err != nil {
			emitErr(cfg.OnError, fmt.Sprintf("persist plan: %v", err))
			return state, err
		}
		emit(cfg.OnPhase, "plan", fmt.Sprintf("%d step plan written to .engine/plan.md", len(state.Plan)))
		if cfg.OnPlanUpdate != nil {
			cfg.OnPlanUpdate(state)
		}
	} else {
		emit(cfg.OnPhase, "plan", fmt.Sprintf("resuming with %d-step plan (%d unchecked)", len(state.Plan), countUnchecked(state)))
	}

	for {
		paused, err := orchestratorPauseStep(cancel, handle)
		if err != nil {
			return state, err
		}
		if paused {
			continue
		}

		state.OuterIterations++
		state.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
		_ = persistOrchestration(cfg.ProjectPath, state)

		if state.OuterIterations > cfg.MaxOuterIterations {
			err := fmt.Errorf("orchestrator: hit safety cap of %d outer iterations", cfg.MaxOuterIterations)
			emitErr(cfg.OnError, err.Error())
			return state, err
		}

		if skipped := skipExhaustedSteps(state, OrchestratorMaxStepAttempts); len(skipped) > 0 {
			_ = persistOrchestration(cfg.ProjectPath, state)
			for _, idx := range skipped {
				emit(cfg.OnPhase, "skip", fmt.Sprintf("step %d exhausted %d attempts — skipping; validator will flag", idx, OrchestratorMaxStepAttempts))
			}
		}

		nextIdx := pickNextStep(state)
		if nextIdx < 0 {
			// Lift any URL the builder persisted during the final deploy step
			// into state so the validator hits the deployed instance.
			if liveURL := readLiveURL(cfg.ProjectPath); liveURL != "" {
				state.LiveURL = liveURL
				_ = persistOrchestration(cfg.ProjectPath, state)
			}
			validateTarget := "localhost"
			if state.LiveURL != "" {
				validateTarget = state.LiveURL
			}
			emit(cfg.OnPhase, "validate", "all plan steps complete — running behavioral validation against "+validateTarget)
			summary, validateErr := orchestratorValidatePhase(cfg, state, cancel)
			state.LastValidation = summary
			if validateErr == nil {
				state.CompletedAt = time.Now().UTC().Format(time.RFC3339)
				_ = persistOrchestration(cfg.ProjectPath, state)
				emit(cfg.OnPhase, "done", "behavioral validation passed")
				return state, nil
			}
			// Validation failed → unmark the most recent step so the loop tries
			// again with the validation feedback in scope.
			unchecked := ensureReopenStep(state)
			unchecked.LastFeedback = "Behavioral validation failed: " + validateErr.Error()
			_ = persistOrchestration(cfg.ProjectPath, state)
			emit(cfg.OnPhase, "validate", fmt.Sprintf("validation failed, reopening step %d", unchecked.Index))
			continue
		}

		step := &state.Plan[nextIdx]
		step.Attempts++
		step.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
		_ = persistOrchestration(cfg.ProjectPath, state)

		// Honor a pending redirect by prepending it to this step's prompt.
		redirect := handle.takeRedirect()

		emit(cfg.OnPhase, "execute", fmt.Sprintf("step %d/%d (attempt %d): %s", step.Index, len(state.Plan), step.Attempts, step.Title))
		buildErr := orchestratorBuildStep(cfg, state, step, redirect, cancel)
		if buildErr != nil {
			step.LastFeedback = "Builder error: " + buildErr.Error()
			_ = persistOrchestration(cfg.ProjectPath, state)
			emitErr(cfg.OnError, fmt.Sprintf("step %d builder error: %v", step.Index, buildErr))
			// Continue the outer loop — the next iteration retries with feedback.
			continue
		}

		emit(cfg.OnPhase, "review", fmt.Sprintf("reviewing step %d", step.Index))
		reviewVerdict, reviewMsg := orchestratorReviewStep(cfg, state, step, cancel)
		switch reviewVerdict {
		case ReviewApprove:
			step.Done = true
			step.LastFeedback = ""
			_ = persistOrchestration(cfg.ProjectPath, state)
			if cfg.OnPlanUpdate != nil {
				cfg.OnPlanUpdate(state)
			}
			// Refresh modules.md so the next step's planner/reviewer start
			// from the current module map. Non-fatal on failure.
			if modErr := orchestratorModuleIndexPhase(cfg, cancel); modErr != nil {
				emitErr(cfg.OnError, fmt.Sprintf("module index refresh: %v", modErr))
			}
			emit(cfg.OnPhase, "review", fmt.Sprintf("step %d approved", step.Index))
		case ReviewReject:
			step.LastFeedback = reviewMsg
			_ = persistOrchestration(cfg.ProjectPath, state)
			emit(cfg.OnPhase, "review", fmt.Sprintf("step %d rejected: %s", step.Index, summarise(reviewMsg, 200)))
			// Outer loop retries with reviewer feedback embedded in the next prompt.
		default:
			// Inconclusive review — treat as soft pass but flag.
			step.Done = true
			step.LastFeedback = "Review inconclusive, advancing: " + reviewMsg
			_ = persistOrchestration(cfg.ProjectPath, state)
			emit(cfg.OnPhase, "review", fmt.Sprintf("step %d inconclusive review", step.Index))
		}
	}
}

func ensureReopenStep(state *OrchestrationState) *PlanStep {
	if unchecked := unmarkLastDone(state); unchecked != nil {
		return unchecked
	}
	state.Plan = append(state.Plan, PlanStep{
		Index:      len(state.Plan) + 1,
		Title:      "Address validation feedback",
		Acceptance: "Validation passes",
		Attempts:   0,
		Done:       false,
	})
	return &state.Plan[len(state.Plan)-1]
}

// emit is a nil-safe progress emitter.
func emit(fn func(string, string), phase, detail string) {
	if fn != nil {
		fn(phase, detail)
	}
}

func emitErr(fn func(string), msg string) {
	if fn != nil {
		fn(msg)
	}
}

func cancelClosed(ch <-chan struct{}) bool {
	if ch == nil {
		return false
	}
	select {
	case <-ch:
		return true
	default:
		return false
	}
}

func orchestratorMergedCancel(a, b <-chan struct{}) <-chan struct{} {
	if a == nil && b == nil {
		return nil
	}
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	merged := make(chan struct{})
	go func() {
		select {
		case <-a:
		case <-b:
		}
		close(merged)
	}()
	return merged
}

func countUnchecked(state *OrchestrationState) int {
	n := 0
	for _, s := range state.Plan {
		if !s.Done {
			n++
		}
	}
	return n
}

func pickNextStep(state *OrchestrationState) int {
	for i, s := range state.Plan {
		if !s.Done {
			return i
		}
	}
	return -1
}

// skipExhaustedSteps marks any unfinished step that has burned through the
// per-step attempt cap as Done with a "skipped" feedback note. Returns the
// number of steps newly skipped so the caller can emit a notification. Without
// this, pickNextStep would keep handing the same broken step to the builder
// every outer iteration until the 200-iteration safety cap fires.
func skipExhaustedSteps(state *OrchestrationState, maxAttempts int) []int {
	if maxAttempts <= 0 {
		return nil
	}
	var skipped []int
	for i := range state.Plan {
		s := &state.Plan[i]
		if !s.Done && s.Attempts >= maxAttempts {
			s.Done = true
			if strings.TrimSpace(s.LastFeedback) == "" {
				s.LastFeedback = fmt.Sprintf("skipped after %d failed attempts — no acceptance match", s.Attempts)
			} else {
				s.LastFeedback = fmt.Sprintf("skipped after %d failed attempts; last feedback: %s", s.Attempts, s.LastFeedback)
			}
			s.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
			skipped = append(skipped, s.Index)
		}
	}
	return skipped
}

func unmarkLastDone(state *OrchestrationState) *PlanStep {
	for i := len(state.Plan) - 1; i >= 0; i-- {
		if state.Plan[i].Done {
			state.Plan[i].Done = false
			return &state.Plan[i]
		}
	}
	return nil
}

func summarise(s string, limit int) string {
	s = strings.TrimSpace(s)
	if len(s) <= limit {
		return s
	}
	return s[:limit] + "…"
}

// loadOrCreateOrchestrationState reads <project>/.engine/orchestration.json,
// returning a fresh state if missing.
func loadOrCreateOrchestrationState(projectPath, owner, repo, brief string) (*OrchestrationState, error) {
	dir := filepath.Join(projectPath, ".engine")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("mkdir .engine: %w", err)
	}
	path := filepath.Join(dir, orchestrationFile)
	data, err := os.ReadFile(path)
	if err == nil {
		var st OrchestrationState
		if jerr := json.Unmarshal(data, &st); jerr == nil {
			// Refresh brief on resume so a re-trigger with updated README sees
			// the new context.
			if strings.TrimSpace(brief) != "" {
				st.Brief = brief
			}
			st.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
			return &st, nil
		}
	}
	return &OrchestrationState{
		Repo:      repo,
		Owner:     owner,
		Brief:     brief,
		StartedAt: time.Now().UTC().Format(time.RFC3339),
		UpdatedAt: time.Now().UTC().Format(time.RFC3339),
	}, nil
}

func persistOrchestration(projectPath string, state *OrchestrationState) error {
	dir := filepath.Join(projectPath, ".engine")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, _ := json.MarshalIndent(state, "", "  ")
	if err := os.WriteFile(filepath.Join(dir, orchestrationFile), data, 0o644); err != nil {
		return err
	}
	return writePlanMarkdown(projectPath, state)
}

// writePlanMarkdown writes <project>/.engine/plan.md — a human-readable
// checkbox plan the user can see in their editor while engine works.
func writePlanMarkdown(projectPath string, state *OrchestrationState) error {
	var b strings.Builder
	b.WriteString("# Engine Project Plan\n\n")
	if state.Repo != "" {
		fmt.Fprintf(&b, "**Repository:** %s/%s\n\n", state.Owner, state.Repo)
	}
	fmt.Fprintf(&b, "Outer iterations: %d / %d\n\n", state.OuterIterations, OrchestratorMaxOuterIterations)
	if state.CompletedAt != "" {
		fmt.Fprintf(&b, "**Completed:** %s\n\n", state.CompletedAt)
	}
	b.WriteString("## Steps\n\n")
	for _, step := range state.Plan {
		mark := "[ ]"
		if step.Done {
			mark = "[x]"
		}
		fmt.Fprintf(&b, "- %s **%d. %s** (attempts: %d)\n", mark, step.Index, step.Title, step.Attempts)
		if strings.TrimSpace(step.Body) != "" {
			fmt.Fprintf(&b, "  - %s\n", indentLines(step.Body, "    "))
		}
		if strings.TrimSpace(step.Acceptance) != "" {
			fmt.Fprintf(&b, "  - **Acceptance:** %s\n", strings.TrimSpace(step.Acceptance))
		}
		if !step.Done && strings.TrimSpace(step.LastFeedback) != "" {
			fmt.Fprintf(&b, "  - **Last feedback:** %s\n", summarise(step.LastFeedback, 400))
		}
		b.WriteString("\n")
	}
	if strings.TrimSpace(state.LastValidation) != "" {
		fmt.Fprintf(&b, "## Last validation\n\n%s\n", state.LastValidation)
	}
	return os.WriteFile(filepath.Join(projectPath, ".engine", "plan.md"), []byte(b.String()), 0o644)
}

// readLiveURL returns the trimmed contents of <project>/.engine/live-url.txt
// when present, the empty string otherwise. The builder writes this file at
// the end of the deploy step so validation can target the live instance.
func readLiveURL(projectPath string) string {
	data, err := os.ReadFile(filepath.Join(projectPath, ".engine", "live-url.txt"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func indentLines(s, prefix string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	for i, line := range lines {
		if i == 0 {
			continue
		}
		lines[i] = prefix + line
	}
	return strings.Join(lines, "\n")
}
