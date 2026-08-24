package ai

// quality:allow-long-file quality:allow-long-function

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/engine/server/runtimecfg"
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
	Index      int    `json:"index"`
	Title      string `json:"title"`
	Body       string `json:"body"`
	Acceptance string `json:"acceptance,omitempty"`
	Done       bool   `json:"done"`
	Attempts   int    `json:"attempts"`
	// CriticRejects counts consecutive diff-critic rejections of this step, so
	// a critic that keeps objecting cannot stall the step forever.
	CriticRejects int    `json:"criticRejects,omitempty"`
	LastFeedback  string `json:"lastFeedback,omitempty"`
	UpdatedAt     string `json:"updatedAt,omitempty"`
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

	// slug namespaces this run's files inside .engine. Empty means the
	// project-wide default pair (orchestration.json + plan.md).
	//
	// The state carries its own filenames rather than every persist call site
	// being handed them, because persistOrchestration is called from twenty
	// places across four files and threading a path through all of them to
	// serve one caller would be a worse trade than one unexported field. It is
	// deliberately not serialised: where a file lives is a property of the run
	// that opened it, not of the content.
	slug string `json:"-"`
}

// stateFileName is the orchestration record this run reads and writes.
func (s *OrchestrationState) stateFileName() string {
	if s == nil || s.slug == "" {
		return orchestrationFile
	}
	return "task-" + s.slug + ".json"
}

// planFileName is the human-readable plan this run renders.
func (s *OrchestrationState) planFileName() string {
	if s == nil || s.slug == "" {
		return "plan.md"
	}
	return "task-" + s.slug + "-plan.md"
}

// taskSlug reduces a task id to something safe to put in a filename.
func taskSlug(id string) string {
	var b strings.Builder
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r + ('a' - 'A'))
		default:
			b.WriteRune('-')
		}
		if b.Len() >= 48 {
			break
		}
	}
	return strings.Trim(b.String(), "-")
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

	// TaskMode says this run is ONE already-specified unit of work — a dx
	// worklist item — rather than a whole project to be conceived.
	//
	// It exists because the two are not the same job and the difference is
	// most of the token bill. A project needs the design grill, the PRD
	// distillation and a fresh module index: three full agentic sessions that
	// decide WHAT to build. A worklist item has already been decided — someone
	// wrote it down — so re-running intake for it spends those three sessions
	// to rediscover a sentence the caller handed us, every single item, and
	// then overwrites the project's real design.md with a grill transcript
	// about a one-line fix.
	//
	// In task mode the orchestrator skips triage (a worklist item is a build
	// directive by construction, so asking a model whether it is one is a call
	// with a known answer), skips intake and PRD, plans directly from the item
	// text against the existing docs, and keeps every execution gate: builder,
	// critic, reviewer, repair, validation. What is dropped is rediscovery, not
	// rigour.
	//
	// TaskID namespaces the run's persisted state so two items being worked in
	// the same project at once do not overwrite each other's plan.
	TaskMode bool
	// TaskID namespaces task-mode orchestration state. Ignored unless TaskMode.
	TaskID string

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
	projectPath   string
	cancel        chan struct{}
	cancelOnce    sync.Once
	paused        bool
	redirectMu    sync.Mutex
	redirectQueue []string
	approveCh     chan bool
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
//
// Task-mode runs register under "<projectPath>#<taskSlug>" so several worklist
// items can be in flight in one project without clobbering each other's handle.
// A lookup by bare project path still finds one of them, because "stop what is
// running on this project" has to keep working from Discord — it just cannot
// promise which one when several are running. GetTaskHandle is the exact
// lookup, and it is what the task API uses.
func GetOrchestratorHandle(projectPath string) *OrchestratorHandle {
	orchSessionsMu.Lock()
	defer orchSessionsMu.Unlock()
	key := strings.TrimSpace(projectPath)
	if h, ok := orchSessions[key]; ok {
		return h
	}
	prefix := key + "#"
	for k, h := range orchSessions {
		if strings.HasPrefix(k, prefix) {
			return h
		}
	}
	return nil
}

// GetTaskHandle returns the handle for one task-mode run, or nil.
func GetTaskHandle(projectPath, taskID string) *OrchestratorHandle {
	orchSessionsMu.Lock()
	defer orchSessionsMu.Unlock()
	return orchSessions[orchestratorSessionKey(strings.TrimSpace(projectPath), taskSlug(taskID))]
}

// orchestratorSessionKey is the registry key for a run.
func orchestratorSessionKey(projectPath, slug string) string {
	if slug == "" {
		return projectPath
	}
	return projectPath + "#" + slug
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
// step boundary. The instruction is prepended to the next builder prompt and
// persisted to the project direction so future sessions inherit the steering.
func (h *OrchestratorHandle) Redirect(message string) {
	if h == nil {
		return
	}
	trimmed := strings.TrimSpace(message)
	if trimmed == "" {
		return
	}
	h.redirectMu.Lock()
	if n := len(h.redirectQueue); n > 0 && h.redirectQueue[n-1] == trimmed {
		h.redirectMu.Unlock()
		return
	}
	h.redirectQueue = append(h.redirectQueue, trimmed)
	if len(h.redirectQueue) > maxQueuedRedirects {
		h.redirectQueue = h.redirectQueue[len(h.redirectQueue)-maxQueuedRedirects:]
	}
	h.redirectMu.Unlock()
	// Persist to project direction outside the lock so DB I/O never blocks
	// callers that hold redirectMu for the queue operations above.
	AppendHumanDirectiveToProjectDirection(h.projectPath, trimmed)
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
// RunProject is the single entry point that decides WHICH orchestrator runs.
//
// It exists so that choosing between the serial loop and the parallel team path
// is one decision in one place, made from the shape of the work and the state of
// the quota window — rather than a constant, which is what it was. Both paths
// have the same signature, so this is a pure routing function; everything it can
// return is something RunAutonomousProject or RunEventOrchestratorAsState would
// have returned on its own.
func RunProject(cfg OrchestratorConfig) (*OrchestrationState, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	parallel, why := ShouldRunEventOrchestrator(ctx, cfg)
	cancel()

	if parallel {
		emit(cfg.OnPhase, "orchestrator", "parallel teams — "+why)
		return RunEventOrchestratorAsState(cfg)
	}
	emit(cfg.OnPhase, "orchestrator", "serial loop — "+why)
	return RunAutonomousProject(cfg)
}

func RunAutonomousProject(cfg OrchestratorConfig) (*OrchestrationState, error) {
	if strings.TrimSpace(cfg.ProjectPath) == "" {
		return nil, fmt.Errorf("orchestrator: project path is required")
	}
	cfg.Brief = enrichOrchestratorBrief(cfg.ProjectPath, cfg.Brief)

	// Routing is the orchestrator's first job — and it happens before any
	// build machinery spins up. Reason about whether this brief is a
	// conversational turn or a build directive. A conversational route answers
	// in one interactive chat turn and never registers a handle, loads
	// orchestration state, or writes to .engine — so a plain "hi" leaves no
	// build artifacts behind. Empty briefs fall through to the build pipeline
	// (the historical default), preserving callers that drive state only.
	//
	// Task mode skips triage outright. A dx worklist item is a build directive
	// by construction — a human or a scout wrote it into a "now" list for
	// something to go and do — so spending a model call to ask whether it might
	// be small talk is paying for an answer we already have.
	if strings.TrimSpace(cfg.Brief) != "" && !cfg.TaskMode {
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
		activeTeam := ""
		if settings, err := runtimecfg.Load(cfg.ProjectPath); err == nil {
			activeTeam = strings.TrimSpace(settings.ActiveTeam)
		}
		resolvedTeam, teamProvider, teamModel, ok := ResolveAutonomousStartupTeam(cfg.ProjectPath, activeTeam)
		if ok {
			_, _ = runtimecfg.Apply(cfg.ProjectPath, runtimecfg.Patch{
				ActiveTeam:    &resolvedTeam,
				ModelProvider: &teamProvider,
				Model:         &teamModel,
			})
		}
	}
	if cfg.MaxOuterIterations <= 0 {
		cfg.MaxOuterIterations = OrchestratorMaxOuterIterations
	}

	// Production ownership stays with the classic orchestrator loop.
	// The event orchestrator remains in-tree for isolated development and
	// direct tests, but it is intentionally unreachable from the top-level
	// autonomous project entrypoint until it implements the real manager /
	// subordinate hierarchy expected by Engine.

	handle := &OrchestratorHandle{
		projectPath: cfg.ProjectPath,
		cancel:      make(chan struct{}),
		approveCh:   make(chan bool, 1),
	}
	sessionKey := cfg.ProjectPath
	if cfg.TaskMode {
		sessionKey = orchestratorSessionKey(cfg.ProjectPath, taskSlug(cfg.TaskID))
	}
	registerOrchestratorHandle(sessionKey, handle)
	defer deregisterOrchestratorHandle(sessionKey)

	// Combine the caller's cancel with the handle's internal cancel into one
	// channel the inner loop watches.
	cancel := orchestratorMergedCancel(cfg.Cancel, handle.cancel)

	slug := ""
	if cfg.TaskMode {
		slug = taskSlug(cfg.TaskID)
	}
	state, err := loadOrCreateOrchestrationStateSlug(cfg.ProjectPath, cfg.Owner, cfg.Repo, cfg.Brief, slug)
	if err != nil {
		return nil, fmt.Errorf("orchestrator: load state: %w", err)
	}

	if cfg.TaskMode {
		// Intake and PRD are how a project decides what it is. This run has been
		// told what it is. Skipping them saves two full agentic sessions per
		// item — and, just as importantly, stops each item rewriting the
		// project's design.md and prd.md with a grill transcript about a
		// one-line change. The existing docs are read as context by the planner
		// and builder below; they are simply not regenerated.
		emit(cfg.OnPhase, "task", "single worklist item — skipping intake and PRD, planning from the item")
	} else {
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

	autoExtensions := 0
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
		if err := persistOrchestration(cfg.ProjectPath, state); err != nil {
			emitErr(cfg.OnError, fmt.Sprintf("persist outer iteration: %v", err))
		}

		if state.OuterIterations > cfg.MaxOuterIterations {
			const maxAutoExtensions = 3
			const extensionIncrement = 100
			skippedCount := countSkippedInState(state)
			if autoExtensions < maxAutoExtensions && skippedCount > 0 {
				autoExtensions++
				cfg.MaxOuterIterations += extensionIncrement
				resetSkippedSteps(state)
				emit(cfg.OnPhase, "replan", fmt.Sprintf(
					"auto-extending: %d skipped steps reset for retry (extension %d/%d, cap now %d)",
					skippedCount, autoExtensions, maxAutoExtensions, cfg.MaxOuterIterations,
				))
				if err := persistOrchestration(cfg.ProjectPath, state); err != nil {
					emitErr(cfg.OnError, fmt.Sprintf("persist replan state: %v", err))
				}
				continue
			}
			err := fmt.Errorf("orchestrator: hit safety cap of %d outer iterations", cfg.MaxOuterIterations)
			emitErr(cfg.OnError, err.Error())
			return state, err
		}

		if skipped := skipExhaustedSteps(state, OrchestratorMaxStepAttempts); len(skipped) > 0 {
			if err := persistOrchestration(cfg.ProjectPath, state); err != nil {
				emitErr(cfg.OnError, fmt.Sprintf("persist skipped steps: %v", err))
			}
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
				if err := persistOrchestration(cfg.ProjectPath, state); err != nil {
					emitErr(cfg.OnError, fmt.Sprintf("persist live URL: %v", err))
				}
			}
			validateTarget := "localhost"
			if state.LiveURL != "" {
				validateTarget = state.LiveURL
			}
			emit(cfg.OnPhase, "validate", "all plan steps complete — running behavioral validation against "+validateTarget)
			summary, validateErr := orchestratorValidatePhase(cfg, state, cancel)
			state.LastValidation = summary
			if validateErr == nil {
				// Validation passing is necessary but not sufficient. If any plan
				// step was silently skipped (auto-extensions exhausted, acceptance
				// never satisfied), the project did NOT genuinely complete — Engine
				// reports done ONLY when 100% correct. Hard-block here.
				if skipped := skippedStepIndices(state); len(skipped) > 0 {
					if err := persistOrchestration(cfg.ProjectPath, state); err != nil {
						emitErr(cfg.OnError, fmt.Sprintf("persist skipped-block state: %v", err))
					}
					err := fmt.Errorf("orchestrator: validation passed but %d plan step(s) were skipped without satisfying acceptance: %v — not reporting done", len(skipped), skipped)
					emitErr(cfg.OnError, err.Error())
					return state, err
				}
				state.CompletedAt = time.Now().UTC().Format(time.RFC3339)
				if err := persistOrchestration(cfg.ProjectPath, state); err != nil {
					emitErr(cfg.OnError, fmt.Sprintf("persist completion state: %v", err))
				}
				emit(cfg.OnPhase, "done", "behavioral validation passed")
				return state, nil
			}
			// Validation failed. Try a bounded repair loop first: diagnose from
			// the failure evidence, apply a targeted fix, re-validate, give up
			// after maxRepairAttempts. Reopening the step and re-running the
			// whole builder is the fallback, not the first move — that was the
			// old behaviour and it is why a failed run needed a human.
			if orchestratorRepairStep(cfg, state, validateErr, cancel) {
				emit(cfg.OnPhase, "repair", "repair loop resolved the validation failure — re-validating")
				// Re-enter the loop rather than completing inline, so the
				// skipped-step block and every other completion guard still run.
				continue
			}

			// Validation failed → unmark the most recent step so the loop tries
			// again with the validation feedback in scope.
			unchecked := ensureReopenStep(state)
			unchecked.LastFeedback = "Behavioral validation failed: " + validateErr.Error()
			if err := persistOrchestration(cfg.ProjectPath, state); err != nil {
				emitErr(cfg.OnError, fmt.Sprintf("persist reopened step: %v", err))
			}
			emit(cfg.OnPhase, "validate", fmt.Sprintf("validation failed, reopening step %d", unchecked.Index))
			continue
		}

		step := &state.Plan[nextIdx]
		step.Attempts++
		step.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
		if err := persistOrchestration(cfg.ProjectPath, state); err != nil {
			emitErr(cfg.OnError, fmt.Sprintf("persist step attempt: %v", err))
		}

		// Honor a pending redirect by prepending it to this step's prompt.
		redirect := handle.takeRedirect()

		emit(cfg.OnPhase, "execute", fmt.Sprintf("step %d/%d (attempt %d): %s", step.Index, len(state.Plan), step.Attempts, step.Title))
		buildErr := orchestratorBuildStep(cfg, state, step, redirect, cancel)
		if buildErr != nil {
			step.LastFeedback = "Builder error: " + buildErr.Error()
			if err := persistOrchestration(cfg.ProjectPath, state); err != nil {
				emitErr(cfg.OnError, fmt.Sprintf("persist builder error feedback: %v", err))
			}
			emitErr(cfg.OnError, fmt.Sprintf("step %d builder error: %v", step.Index, buildErr))
			// Continue the outer loop — the next iteration retries with feedback.
			continue
		}

		// Cheap diff-only critic before the expensive agentic reviewer. It stops
		// blocking after maxCriticRejectLoops consecutive rejections of the same
		// step, at which point the full reviewer is the decider — otherwise a
		// critic that keeps objecting would stall the step indefinitely.
		if step.CriticRejects < maxCriticRejectLoops {
			if verdict, findings := orchestratorCriticStep(cfg, step, cancel); verdict == CriticReject {
				step.CriticRejects++
				step.LastFeedback = "Critic rejected the diff:\n" + findings
				if err := persistOrchestration(cfg.ProjectPath, state); err != nil {
					emitErr(cfg.OnError, fmt.Sprintf("persist critic rejection: %v", err))
				}
				emit(cfg.OnPhase, "critic", fmt.Sprintf("step %d rejected by critic: %s", step.Index, summarise(findings, 200)))
				// Outer loop retries with the critic's findings as feedback.
				continue
			}
		}
		// Survived the critic — reset the budget so a later regression on this
		// step gets a fresh set of attempts.
		step.CriticRejects = 0

		emit(cfg.OnPhase, "review", fmt.Sprintf("reviewing step %d", step.Index))
		reviewVerdict, reviewMsg := orchestratorReviewStep(cfg, state, step, cancel)
		switch reviewVerdict {
		case ReviewApprove:
			step.Done = true
			step.LastFeedback = ""
			if err := persistOrchestration(cfg.ProjectPath, state); err != nil {
				emitErr(cfg.OnError, fmt.Sprintf("persist approved step: %v", err))
			}
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
			if err := persistOrchestration(cfg.ProjectPath, state); err != nil {
				emitErr(cfg.OnError, fmt.Sprintf("persist rejected step feedback: %v", err))
			}
			emit(cfg.OnPhase, "review", fmt.Sprintf("step %d rejected: %s", step.Index, summarise(reviewMsg, 200)))
			// Outer loop retries with reviewer feedback embedded in the next prompt.
		default:
			// Inconclusive review — treat as soft pass but flag.
			step.Done = true
			step.LastFeedback = "Review inconclusive, advancing: " + reviewMsg
			if err := persistOrchestration(cfg.ProjectPath, state); err != nil {
				emitErr(cfg.OnError, fmt.Sprintf("persist inconclusive review state: %v", err))
			}
			emit(cfg.OnPhase, "review", fmt.Sprintf("step %d inconclusive review", step.Index))
		}
	}
}

// EventOrchestratorEnabled reports whether the parallel multi-team orchestrator
// should own a run.
//
// It was `return false` with no callers, which is what stranded ~1850 lines of
// tested parallel machinery. Two things had to be true before it could be
// anything else, and now are: the brain's state is actually behind its mutex
// (it was not — extractOrchestrationState raced the event loop), and a team
// step is held to the same critic-then-reviewer bar as a classic step (it was
// not — a team marked its step done on the builder's word alone).
//
// The default is DERIVED, not hardcoded: parallel teams only pay for
// themselves when there is genuinely concurrent work and quota headroom to run
// it in. One team is strictly worse than the classic loop — same work, an extra
// event bus, and a weaker outer validation story — so a single-team plan runs
// classic. See ShouldRunEventOrchestrator for the run-scoped decision;
// ENGINE_EVENT_ORCHESTRATOR=1/0 forces it either way.
func EventOrchestratorEnabled() bool {
	switch strings.TrimSpace(strings.ToLower(os.Getenv(envEventOrchestrator))) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	}
	return eventOrchestratorDefault
}

// envEventOrchestrator forces the parallel path on or off.
const envEventOrchestrator = "ENGINE_EVENT_ORCHESTRATOR"

// eventOrchestratorDefault is what an unset ENGINE_EVENT_ORCHESTRATOR means.
//
// On. The parallel path is now race-free, cap-enforced and quality-gated, and
// the concurrency ceiling comes from the quota governor rather than a literal —
// so on a tight window it narrows to one team and behaves like the serial loop,
// and on a roomy one it uses the room. There is no longer a configuration in
// which "off" is the safer answer, only slower ones.
const eventOrchestratorDefault = true

// eventOrchestratorEnabled is the internal spelling kept for existing callers.
func eventOrchestratorEnabled() bool { return EventOrchestratorEnabled() }

// ShouldRunEventOrchestrator decides, for one concrete run, whether the
// parallel path is the right one.
//
// An explicit ENGINE_EVENT_ORCHESTRATOR setting always wins — if someone has
// said which orchestrator they want, inferring something else from the shape of
// the work is second-guessing them. Absent that, parallelism has to be earned:
// it needs work that can genuinely proceed at once, and room in the window to
// run it. Otherwise the classic loop, whose per-run validation and repair loop
// are more developed, keeps the run.
func ShouldRunEventOrchestrator(ctx context.Context, cfg OrchestratorConfig) (bool, string) {
	switch strings.TrimSpace(strings.ToLower(os.Getenv(envEventOrchestrator))) {
	case "1", "true", "yes", "on":
		return true, "ENGINE_EVENT_ORCHESTRATOR=1"
	case "0", "false", "no", "off":
		return false, "ENGINE_EVENT_ORCHESTRATOR=0"
	}
	if !eventOrchestratorDefault {
		return false, "parallel orchestrator off by default"
	}
	// A single-unit-of-work run has nothing to parallelise. Task mode is the
	// SARA bridge's shape — one dx worklist item — and paying for an event bus,
	// a dispatcher and a team table to run one team is pure overhead.
	if cfg.TaskMode {
		return false, "task mode: one unit of work, nothing to run in parallel"
	}
	levers := CurrentQuotaLevers(ctx)
	if levers.MaxConcurrency < 2 {
		return false, fmt.Sprintf("quota tier %s allows %d concurrent session(s) — no room for parallel teams",
			levers.TierName, levers.MaxConcurrency)
	}
	return true, fmt.Sprintf("quota tier %s allows %d concurrent teams", levers.TierName, levers.MaxConcurrency)
}

func enrichOrchestratorBrief(projectPath, brief string) string {
	trimmedBrief := strings.TrimSpace(brief)
	direction := strings.TrimSpace(EnsureProjectDirection(projectPath))
	if direction == "" {
		return trimmedBrief
	}
	if strings.Contains(trimmedBrief, "PERSISTENT PROJECT DIRECTION") {
		return trimmedBrief
	}
	if trimmedBrief == "" {
		return strings.TrimSpace("PERSISTENT PROJECT DIRECTION:\n" + direction)
	}
	return strings.TrimSpace(
		"PERSISTENT PROJECT DIRECTION:\n" + direction +
			"\n\nEXECUTION RULE:\nAlways preserve and advance the project direction while completing the current request.\n\nCURRENT REQUEST:\n" + trimmedBrief,
	)
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

// countSkippedInState returns the number of plan steps that were skipped after
// exhausting their per-step attempt cap (identified by their LastFeedback).
func countSkippedInState(state *OrchestrationState) int {
	n := 0
	for _, s := range state.Plan {
		if s.Done && strings.Contains(s.LastFeedback, "skipped after") {
			n++
		}
	}
	return n
}

// skippedStepIndices returns the indices (1-based step.Index values) of Done
// steps whose LastFeedback marks them as skipped after exhausting their attempt
// cap. A non-empty result means the project advanced past steps it never
// genuinely satisfied — those must block "done".
func skippedStepIndices(state *OrchestrationState) []int {
	var idxs []int
	for _, s := range state.Plan {
		if s.Done && strings.Contains(s.LastFeedback, "skipped after") {
			idxs = append(idxs, s.Index)
		}
	}
	return idxs
}

// resetSkippedSteps reopens any step that was skipped after exhausting its
// attempt cap, resetting attempts to zero so the auto-extension cycle gives
// each step a fresh attempt budget.
func resetSkippedSteps(state *OrchestrationState) {
	now := time.Now().UTC().Format(time.RFC3339)
	for i := range state.Plan {
		s := &state.Plan[i]
		if s.Done && strings.Contains(s.LastFeedback, "skipped after") {
			s.Done = false
			s.Attempts = 0
			s.LastFeedback = "reset for retry after auto-extension"
			s.UpdatedAt = now
		}
	}
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
	return loadOrCreateOrchestrationStateSlug(projectPath, owner, repo, brief, "")
}

// loadOrCreateOrchestrationStateSlug is the same, for a namespaced run. A
// non-empty slug gives the run its own state and plan files so two units of
// work in one project do not overwrite each other's plan.
func loadOrCreateOrchestrationStateSlug(projectPath, owner, repo, brief, slug string) (*OrchestrationState, error) {
	dir := filepath.Join(projectPath, ".engine")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("mkdir .engine: %w", err)
	}
	probe := &OrchestrationState{slug: slug}
	path := filepath.Join(dir, probe.stateFileName())
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
			st.slug = slug
			return &st, nil
		}
	}
	return &OrchestrationState{
		Repo:      repo,
		Owner:     owner,
		Brief:     brief,
		StartedAt: time.Now().UTC().Format(time.RFC3339),
		UpdatedAt: time.Now().UTC().Format(time.RFC3339),
		slug:      slug,
	}, nil
}

func persistOrchestration(projectPath string, state *OrchestrationState) error {
	dir := filepath.Join(projectPath, ".engine")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, _ := json.MarshalIndent(state, "", "  ")
	if err := os.WriteFile(filepath.Join(dir, state.stateFileName()), data, 0o644); err != nil {
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
	return os.WriteFile(filepath.Join(projectPath, ".engine", state.planFileName()), []byte(b.String()), 0o644)
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
