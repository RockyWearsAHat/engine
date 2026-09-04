package ai

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/engine/server/db"
)

// teamWaitStallInterval is how often the team wait re-examines whether progress
// is still possible. Short enough that a deadlocked run reports rather than
// hangs, long enough that it costs nothing on a healthy run. Var, not const, so
// tests can shorten it.
var teamWaitStallInterval = 5 * time.Second

// effectiveTeamSize resolves how many teams/workers a single dispatch may run
// concurrently. requested is OrchestratorConfig.TeamSize — the dispatch
// payload's explicit ask, 0 meaning "no opinion". Precedence: requested wins
// outright when > 0; else MYEDITOR_TEAM_SIZE; else 1 (one item = one worker).
func effectiveTeamSize(requested int) int {
	if requested > 0 {
		return requested
	}
	if v := strings.TrimSpace(os.Getenv("MYEDITOR_TEAM_SIZE")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return 1
}

// planPhaseTimeout bounds the plan phase for a single dispatched item: it is
// scoping work already decided (TaskMode), not conceiving a project, so it
// runs on haiku and gets a hard wall-clock cap rather than the full run
// budget every execute-phase session gets. Prevents "plan" from becoming a
// second uncapped session per item.
//
// Default 180s, overridable via MYEDITOR_PLAN_BUDGET_SECS — 60s proved too
// tight on a loaded box (plans observed taking 130-160s that would otherwise
// reach execute) and the hard-stop below now has a non-fatal fallback, so a
// generous default costs nothing but a slower deadline.
var planPhaseTimeout = func() time.Duration {
	if v := strings.TrimSpace(os.Getenv("MYEDITOR_PLAN_BUDGET_SECS")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return time.Duration(n) * time.Second
		}
	}
	return 180 * time.Second
}()

// planPhaseHardStopGrace is added on top of planPhaseTimeout before phasePlan
// itself gives up waiting on the plan call and moves on with whatever output
// was captured. It covers the provider's own cancel-bridge + cmd.WaitDelay
// teardown (~5s) plus taskkill/kill(2) overhead, so this only fires when that
// teardown has actually failed to happen in time — not on every run.
var planPhaseHardStopGrace = 15 * time.Second

// EventOrchestrator runs the event-driven autonomy loop.
// It manages intake → requirements → planning → team dispatch → validation → completion.
type EventOrchestrator struct {
	cfg                OrchestratorConfig
	brain              *OrchestrationBrain
	bus                *EventBus
	dispatcher         *TeamDispatcher
	comms              *AgentCommsHub
	ctx                context.Context
	cancel             context.CancelFunc
	wg                 sync.WaitGroup
	redirectQueue      chan string
	pauseMu            sync.Mutex
	paused             bool
	pausedUntil        <-chan struct{}
	maxOuterIterations int

	// failure is why the run ended badly, or nil if it did not.
	//
	// It exists because the event loop runs in a goroutine and its only channel
	// back to the caller used to be the event bus, which nothing outside the
	// orchestrator subscribes to. RunEventOrchestratorAsState therefore returned
	// a nil error for every outcome — and the task API reads exactly that error
	// to decide between "done" and "failed". A parallel run whose planner
	// produced nothing at all was reported to SARA as `"status":"done"`, which
	// is the one answer that makes an autonomous supervisor stop looking.
	//
	// Written once by the loop goroutine before it exits and read after
	// wg.Wait(), but guarded anyway: RunEventOrchestrator hands the brain back
	// while the loop is still running, so a caller can legitimately ask.
	failureMu sync.Mutex
	failure   error
}

// RunEventOrchestratorAsState runs the event orchestrator to completion and
// returns its final state, matching RunAutonomousProject.
//
// It BLOCKS, and that is the substance of this wrapper rather than a detail.
// The two functions have had the same signature all along, but not the same
// meaning: RunAutonomousProject returns when the project is built, while this
// used to return the instant the event loop had been started in a goroutine.
// Swapping one for the other at the orchestrator seam on the strength of the
// signature alone would have made every caller believe a build had finished
// before it began — the Discord "✅ build complete" notice would fire on
// kickoff, and the caller's db.WithProject scope would close under the running
// loop. Waiting here is what makes the two genuinely interchangeable.
func RunEventOrchestratorAsState(cfg OrchestratorConfig) (*OrchestrationState, error) {
	eo, err := startEventOrchestrator(cfg)
	if err != nil {
		return nil, err
	}
	eo.wg.Wait()
	// The state comes back either way. A failed run still built something, and
	// the caller needs the plan and step counts to say how far it got — but the
	// error is what decides whether this is reported as a delivery.
	return extractOrchestrationState(eo.brain), eo.Failure()
}

// RunEventOrchestrator starts the event-driven orchestrator and returns its
// brain immediately, without waiting for the run. Callers that want to wait
// should use RunEventOrchestratorAsState.
func RunEventOrchestrator(cfg OrchestratorConfig) (*OrchestrationBrain, error) {
	eo, err := startEventOrchestrator(cfg)
	if err != nil {
		return nil, err
	}
	return eo.brain, nil
}

// startEventOrchestrator builds the orchestrator and starts its loop.
func startEventOrchestrator(cfg OrchestratorConfig) (*EventOrchestrator, error) {
	if strings.TrimSpace(cfg.ProjectPath) == "" {
		return nil, fmt.Errorf("orchestrator: project path is required")
	}
	if cfg.OnPhase == nil {
		cfg.OnPhase = func(string, string) {}
	}
	if cfg.OnProgress == nil {
		cfg.OnProgress = func(string) {}
	}
	if cfg.OnError == nil {
		cfg.OnError = func(string) {}
	}
	if cfg.ChatFn == nil {
		cfg.ChatFn = func(*ChatContext, string) {}
	}

	ctx, cancel := context.WithCancel(context.Background())

	// Task mode = one worklist item. Own brain file so two items on one
	// repo do not stomp each other's plan.
	slug := ""
	if cfg.TaskMode {
		slug = taskSlug(cfg.TaskID)
	}
	brain, _ := NewOrchestrationBrainSlug(cfg.ProjectPath, cfg.Owner, cfg.Repo, cfg.Brief, cfg.SessionIDPrefix, slug)

	bus := NewEventBus()
	comms := AgentCommsForProject(cfg.ProjectPath)
	comms.Register("lead", "orchestrator", "running")

	// How many teams may run at once for THIS dispatch. One item = one worker
	// by default: sizing this off the quota governor's MaxConcurrency lever
	// (as it used to) meant a single dx worklist item could fan out into as
	// many concurrent `claude -p` sessions as the tier allowed project-wide —
	// 3 in-flight tasks measured as 14 concurrent CLI processes, exhausting
	// box memory before the governor ever got a chance to react. The quota
	// lever still bounds real per-project fan-out elsewhere (quotaBefore,
	// SubagentFanout); it has no business also multiplying team count for a
	// dispatch that never asked for a team.
	//
	// Precedence: cfg.TeamSize (the dispatch payload's explicit request) wins
	// outright; else MYEDITOR_TEAM_SIZE (operator override); else 1.
	capFn := func() int {
		return effectiveTeamSize(cfg.TeamSize)
	}
	dispatcher := NewTeamDispatcherWithCap(brain, bus, cfg, capFn, comms)

	eo := &EventOrchestrator{
		cfg:                cfg,
		brain:              brain,
		bus:                bus,
		dispatcher:         dispatcher,
		comms:              comms,
		ctx:                ctx,
		cancel:             cancel,
		redirectQueue:      make(chan string, 10),
		maxOuterIterations: cfg.MaxOuterIterations,
	}

	if eo.maxOuterIterations <= 0 {
		eo.maxOuterIterations = OrchestratorMaxOuterIterations
	}

	// Honour the caller's cancel channel. The event orchestrator had its own
	// context and ignored cfg.Cancel entirely, so a caller holding the only
	// stop signal the rest of the codebase uses had no way to stop it — which
	// is fine for a path nobody calls and not fine for one at the seam.
	if cfg.Cancel != nil {
		go func() {
			select {
			case <-cfg.Cancel:
				eo.cancel()
				eo.bus.Emit(Event{Type: EventCancel, Timestamp: time.Now()})
			case <-ctx.Done():
			}
		}()
	}

	// Run the main loop
	eo.wg.Add(1)
	go func() {
		defer eo.wg.Done()
		eo.eventLoop()
	}()

	return eo, nil
}

// eventLoop is the main event-driven orchestrator loop.
func (eo *EventOrchestrator) eventLoop() {
	// Subscribe to relevant events
	teamDone := eo.bus.Subscribe(EventTeamDone, 10)
	teamFailed := eo.bus.Subscribe(EventTeamFailed, 10)
	userRedirect := eo.bus.Subscribe(EventUserRedirect, 10)
	cancelEv := eo.bus.Subscribe(EventCancel, 1)

	defer func() {
		eo.bus.Close()
		eo.dispatcher.Stop()
	}()

	// Phase 1: Intake (design, PRD, vocabulary). Task mode skips it: item
	// already decided, existing docs read by planner. Same rule as serial.
	if eo.cfg.TaskMode {
		eo.cfg.OnPhase("task", "single worklist item — skipping intake and PRD, planning from the item")
		eo.brain.UpdateRequirements(ReadDoc(eo.cfg.ProjectPath, DocDesign), ReadDoc(eo.cfg.ProjectPath, DocVocabulary),
			ReadDoc(eo.cfg.ProjectPath, DocPRD), ReadDoc(eo.cfg.ProjectPath, DocModules))
	} else {
		eo.phaseIntake()
	}

	// Phase 2: Planning
	if err := eo.phasePlan(); err != nil {
		eo.fail(err)
		return
	}

	// Phase 3+: Execute with team dispatch + validation loop
	for eo.brain.OuterIterationCount() < eo.maxOuterIterations {
		iteration := eo.brain.NextOuterIteration()
		eo.cfg.OnPhase("execute", fmt.Sprintf("iteration %d/%d", iteration, eo.maxOuterIterations))

		// Dispatch ready teams
		if err := eo.phaseDispatchTeams(); err != nil {
			eo.fail(fmt.Errorf("team dispatch failed: %w", err))
			return
		}

		// Wait for all teams to complete or fail
		if err := eo.phaseWaitTeams(teamDone, teamFailed, userRedirect, cancelEv); err != nil {
			if err == context.Canceled {
				eo.setFailure(context.Canceled)
				eo.bus.Emit(Event{Type: EventProjectCanceled, Timestamp: time.Now()})
				return
			}
			eo.fail(fmt.Errorf("team execution failed: %w", err))
			return
		}

		// Validate the build
		eo.cfg.OnPhase("validate", "")
		valid, feedback := eo.phaseValidate()
		if valid {
			eo.cfg.OnProgress("✓ Validation passed! Project ready.")
			eo.brain.MarkCompleted()
			eo.bus.Emit(Event{Type: EventProjectDone, Timestamp: time.Now()})
			return
		}

		// Validation failed; feedback → re-plan → retry
		eo.cfg.OnProgress(fmt.Sprintf("Validation feedback: %s", feedback))
		eo.brain.SetLastValidation(feedback)

		if err := eo.phasePlan(); err != nil {
			eo.fail(err)
			return
		}
	}

	eo.fail(fmt.Errorf("max iterations (%d) reached", eo.maxOuterIterations))
}

// phaseIntake runs design grill → PRD distillation.
func (eo *EventOrchestrator) phaseIntake() error {
	eo.cfg.OnPhase("intake", "grill interview")

	sessionID := fmt.Sprintf("%s-intake-grill", eo.cfg.SessionIDPrefix)
	cc := newPhaseChat(eo.cfg, sessionID)
	if strings.TrimSpace(eo.cfg.RequestedRole) == "" {
		cc.Ctx.Role = RoleGriller
	}

	// Grill phase (design)
	eo.cfg.chatFnFor()(cc.Ctx, buildGrillPrompt(eo.cfg.Brief))

	design := strings.TrimSpace(cc.GetOutput())
	if design != "" {
		if err := WriteDoc(eo.cfg.ProjectPath, DocDesign, design); err != nil {
			eo.cfg.OnError(fmt.Sprintf("persist design doc: %v", err))
		}
	}

	eo.bus.Emit(Event{Type: EventDesignReady, Timestamp: time.Now()})

	// PRD phase
	eo.cfg.OnPhase("intake", "PRD distillation")
	sessionID = fmt.Sprintf("%s-intake-prd", eo.cfg.SessionIDPrefix)
	cc = newPhaseChat(eo.cfg, sessionID)
	if strings.TrimSpace(eo.cfg.RequestedRole) == "" {
		cc.Ctx.Role = RolePRDWriter
	}

	prd := ReadDoc(eo.cfg.ProjectPath, DocPRD)
	vocab := ReadDoc(eo.cfg.ProjectPath, DocVocabulary)
	moduleIndex := ReadDoc(eo.cfg.ProjectPath, DocModules)

	eo.brain.UpdateRequirements(design, vocab, prd, moduleIndex)
	eo.bus.Emit(Event{Type: EventPRDReady, Timestamp: time.Now()})

	return nil
}

// phasePlan runs the planner to generate/update the plan.
func (eo *EventOrchestrator) phasePlan() error {
	eo.cfg.OnPhase("plan", "generating plan")

	sessionID := fmt.Sprintf("%s-plan-%d", eo.cfg.SessionIDPrefix, eo.brain.OuterIterationCount())
	cc := newPhaseChat(eo.cfg, sessionID)
	if strings.TrimSpace(eo.cfg.RequestedRole) == "" {
		cc.Ctx.Role = RolePlanner
	}

	// Task mode's plan phase is one already-decided item, not a project to
	// conceive: bound it to a 60s wall clock, and to haiku unless the caller
	// pinned a specific model for this dispatch (RequestedModel wins — that
	// pin is a floor for the whole run, not something the plan phase should
	// silently override). Without this a "plan" phase was a second uncapped
	// `claude -p` session per dispatched item, on top of whatever the
	// execute phase spent.
	if eo.cfg.TaskMode {
		if strings.TrimSpace(eo.cfg.RequestedModel) == "" {
			cc.Ctx.ModelOverride = "haiku"
		}
		timer := time.NewTimer(planPhaseTimeout)
		defer timer.Stop()
		boundedCancel := make(chan struct{})
		go func() {
			select {
			case <-timer.C:
				close(boundedCancel)
			case <-eo.ctx.Done():
			}
		}()
		cc.Ctx.Cancel = orchestratorMergedCancel(eo.cfg.Cancel, boundedCancel)
	}

	// The planner prompt is the serial path's, deliberately, because the parser
	// is the serial path's too — extractPlanFromContext is a one-line wrapper
	// around parsePlanFromText.
	//
	// The prompt this replaces had two independent defects, both fatal and both
	// invisible while nothing called this code:
	//
	//  1. It never included the brief. It was assembled purely from the brain's
	//     requirements — PRD, vocabulary, design, module index — every one of
	//     which phaseIntake fills in by reading .engine docs that a fresh project
	//     does not have. So on a new project the planner was asked to "create a
	//     concrete numbered plan to implement the project" with four empty
	//     sections and no statement anywhere of what the project was.
	//
	//  2. It asked for JSON. parsePlanFromText reads numbered markdown
	//     (`^\s*(\d+)\.\s+`) and understands no JSON at all, so even a model that
	//     answered the prompt perfectly parsed to zero steps.
	//
	// Sharing the builder is what stops these drifting apart again: there is one
	// planner output format in this codebase and one thing that writes prompts
	// asking for it.
	req := eo.brain.GetRequirements()
	prompt := buildPlannerPromptWithContext(eo.cfg.Brief, eo.eventPlannerContext(req))

	// Run the call off-goroutine and enforce the wall clock here too, not
	// only via cc.Ctx.Cancel. Observed live: plan phases were taking 130-160s
	// against a 60s budget — cc.Ctx.Cancel closing depends on the provider's
	// own cancel-bridge goroutine noticing in time and the CLI's process
	// tree actually dying within cmd.WaitDelay, neither of which this call
	// site could verify. This select is the backstop: whatever produced
	// output by the deadline is what the plan is built from (task instructions:
	// "the plan is taken from whatever was produced by then"), and it force-
	// kills any session still registered for this task so a stuck `claude -p`
	// never survives into the execute phase — the "two live sessions per task"
	// bug this was chasing.
	planStart := time.Now()
	if eo.cfg.TaskMode {
		done := make(chan struct{})
		go func() {
			eo.cfg.chatFnFor()(cc.Ctx, prompt)
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(planPhaseTimeout + planPhaseHardStopGrace):
			for _, pid := range LiveSessionPIDs(eo.cfg.TaskID) {
				_ = KillPIDTree(pid)
			}
			log.Printf("task %s: plan phase exceeded %s wall clock (budget %s) — force-killed, using output produced so far",
				eo.cfg.TaskID, planPhaseTimeout+planPhaseHardStopGrace, planPhaseTimeout)
			// done fires once the killed RunLoop actually returns and the
			// goroutine above closes it; drain it so that goroutine cannot
			// leak, but do not block the phase on it any longer.
			go func() { <-done }()
		}
	} else {
		eo.cfg.chatFnFor()(cc.Ctx, prompt)
	}

	// Parse plan from context output
	output := cc.GetOutput()
	plan := extractPlanFromContext(output)

	// TaskMode's plan phase failing must never end the task: the item itself
	// is already a decided, executable unit of work (that is the whole point
	// of TaskMode), so a planner that produced nothing — because it was
	// killed at the wall-clock deadline, or simply answered with unparseable
	// text — falls back to a one-step plan that IS the item, verbatim, and
	// execution proceeds exactly as it would from a real plan.
	//
	// Before this, an empty plan here returned an error, eventLoop called
	// eo.fail and returned, and the task ended silently: no execute phase,
	// no error visible to the operator beyond a log line, and the engine
	// re-requested the same worklist item a minute later — burning quota on
	// repeated dead plan phases instead of ever doing the work.
	if len(plan) == 0 && eo.cfg.TaskMode {
		item := strings.TrimSpace(eo.cfg.Brief)
		log.Printf("plan: budget exhausted after %.0fs — executing the item as a one-step plan",
			time.Since(planStart).Seconds())
		plan = []PlanStep{{
			Index:     1,
			Title:     "Do the worklist item",
			Body:      fmt.Sprintf("Do this item: %s; commit when done; tick the item.", item),
			UpdatedAt: time.Now().UTC().Format(time.RFC3339),
		}}
	}

	// An empty plan is terminal, not something to carry on from.
	//
	// Every one of these returns used to be discarded. With no plan there are no
	// teams; with no teams phaseDispatchTeams dispatches nothing and
	// phaseWaitTeams returns at once, so the outer loop ran its full iteration
	// budget — 200 passes in six seconds, measured — and reported "max iterations
	// reached". That message describes a run that tried two hundred times, which
	// is the opposite of what happened, and it names the loop rather than the
	// planner call that actually failed.
	if len(plan) == 0 {
		return fmt.Errorf("planner produced no usable plan (%d chars of output); nothing can be dispatched", len(output))
	}
	if err := eo.brain.UpdatePlan(plan); err != nil {
		return fmt.Errorf("persist plan: %w", err)
	}

	// Create teams from plan: chunk steps into role-based teams
	eo.createTeamsFromPlan(plan)
	if eo.brain.TeamCount() == 0 {
		return fmt.Errorf("plan has %d step(s) but produced no teams; nothing can be dispatched", len(plan))
	}

	eo.bus.Emit(Event{Type: EventPlanReady, Timestamp: time.Now(), Payload: EventPayload("steps", len(plan))})

	if eo.cfg.OnPlanUpdate != nil {
		eo.cfg.OnPlanUpdate(extractOrchestrationState(eo.brain))
	}

	return nil
}

// phaseDispatchTeams dispatches every ready team the concurrency ceiling has
// room for.
//
// Hitting the ceiling is not a failure and must not read as one: the remaining
// teams stay queued and this function is called again on every team-done event,
// so they start as slots free up. Treating a full ceiling as a dispatch error
// would abort the project the moment the governor narrowed.
func (eo *EventOrchestrator) phaseDispatchTeams() error {
	ready := eo.brain.ReadyTeams()
	dispatched := 0
	for _, t := range ready {
		err := eo.dispatcher.DispatchTeam(t.ID)
		if errors.Is(err, ErrTeamCapReached) {
			eo.cfg.OnProgress(fmt.Sprintf(
				"At team ceiling (%d running); %d team(s) stay queued",
				eo.dispatcher.MaxTeams(), len(ready)-dispatched))
			return nil
		}
		if err != nil {
			return fmt.Errorf("dispatch team %s: %w", t.ID, err)
		}
		dispatched++
		eo.cfg.OnProgress(fmt.Sprintf("Dispatched team %s (%s) — %d/%d running",
			t.ID, t.Role, eo.dispatcher.ActiveTeams(), eo.dispatcher.MaxTeams()))
	}
	return nil
}

// phaseWaitTeams waits for all teams to complete and handles redirects.
//
// The check before the loop is load-bearing. The loop only ever reconsiders
// "are we done" on the arrival of a team event, so a run with no teams at all —
// an empty plan, or a plan the planner returned nothing for — waits forever for
// an event that nothing will ever send. That hang was invisible for as long as
// RunEventOrchestratorAsState returned before the loop got here; it is the
// first thing that happens once the wrapper actually waits.
func (eo *EventOrchestrator) phaseWaitTeams(teamDone, teamFailed, userRedirect, cancelEv <-chan Event) error {
	// The progress check runs on a timer rather than a ticker so the FIRST one
	// can be almost immediate.
	//
	// This matters more than it looks. The outer loop runs up to
	// maxOuterIterations times and calls this on every pass, so when there is
	// nothing to wait for — an empty plan, a planner that returned nothing, a
	// ceiling that left everything queued — a full stall interval is paid per
	// iteration and the run reads as a hang. Measured on the main package's
	// suite: ten minutes of waiting for events that were never coming.
	//
	// It cannot simply return before the select, though. A cancel or a redirect
	// may already be sitting in its channel, and those must still be honoured —
	// a cancelled run has to report cancellation, not success. Leaving the check
	// inside the select keeps that ordering: at entry only the buffered events
	// are ready, so select takes them; the timer only wins when nothing else is
	// there to take.
	// The fast first check is for exactly one state: no team was ever created.
	// Then no team event can ever arrive, by construction, and waiting five
	// seconds to work that out is five seconds of nothing per outer iteration.
	//
	// Once a team exists the normal interval applies, because "a team is running
	// and slow" and "a team is running and stuck" are indistinguishable from
	// here and only time separates them. That is also why this is not simply an
	// early return: a cancel or a redirect may already be queued, and those must
	// still be honoured before any conclusion about progress. Leaving the check
	// inside the select preserves that — at entry only the buffered events are
	// ready, so select takes them, and the timer only wins with nothing else to
	// take.
	firstCheck := teamWaitStallInterval
	if eo.brain != nil && eo.brain.TeamCount() == 0 {
		firstCheck = 25 * time.Millisecond
	}
	stall := time.NewTimer(firstCheck)
	defer stall.Stop()

	for {
		select {
		case <-eo.ctx.Done():
			return eo.ctx.Err()
		case <-stall.C:
			// Every check after the first is on the slow clock: from here on,
			// waiting is the normal state and polling it hard would cost real
			// work nothing but noise.
			stall.Reset(teamWaitStallInterval)

			// Two states look identical from inside this select and only one of
			// them is survivable: teams are working and simply slow, or no team
			// is running at all and no team event can therefore ever arrive. The
			// second is a permanent hang, and it is reachable normally — a plan
			// the ceiling left entirely queued, or every dispatched team already
			// finished.
			if eo.brain.AllTeamsDone() {
				return nil
			}
			if eo.dispatcher == nil || eo.dispatcher.ActiveTeams() > 0 {
				continue
			}
			// A slot may have freed since the last dispatch round.
			if err := eo.phaseDispatchTeams(); err != nil {
				return err
			}
			if eo.dispatcher.ActiveTeams() == 0 {
				return fmt.Errorf("no team is running and %d team(s) are unfinished — nothing can make progress", eo.unfinishedTeams())
			}
		case ev := <-cancelEv:
			_ = ev
			return context.Canceled
		case ev := <-userRedirect:
			msg := ev.Payload["message"].(string)
			eo.handleRedirect(msg)
		case ev := <-teamDone:
			teamID := ev.TeamID
			eo.cfg.OnProgress(fmt.Sprintf("✓ Team %s completed", teamID))

			// Check if all teams are done
			if eo.brain.AllTeamsDone() {
				return nil
			}

			// Check for newly ready teams (dependencies satisfied)
			if err := eo.phaseDispatchTeams(); err != nil {
				return err
			}
		case ev := <-teamFailed:
			teamID := ev.TeamID
			errMsg := ev.Payload["error"].(string)
			eo.cfg.OnProgress(fmt.Sprintf("✗ Team %s failed: %s", teamID, errMsg))

			// Mark all dependent teams as blocked
			eo.brain.UpdateTeamStatus(teamID, "failed")
		}
	}
}

// phaseValidate runs tests + behavior check.
func (eo *EventOrchestrator) phaseValidate() (bool, string) {
	sessionID := fmt.Sprintf("%s-validate-%d", eo.cfg.SessionIDPrefix, eo.brain.OuterIterationCount())
	cc := newPhaseChat(eo.cfg, sessionID)
	if strings.TrimSpace(eo.cfg.RequestedRole) == "" {
		cc.Ctx.Role = RoleReviewer
	}

	prompt := `Run the full test suite and verify the project builds. Report any failures.
If all tests pass and the project is ready for production, respond with: VALIDATION_PASSED
Otherwise, describe the failures and what needs to be fixed.`

	eo.cfg.chatFnFor()(cc.Ctx, prompt)

	output := cc.GetOutput()

	// Check if validation passed
	if strings.Contains(output, "VALIDATION_PASSED") {
		return true, ""
	}

	// Extract feedback
	feedback := output
	if len(feedback) > 500 {
		feedback = feedback[:500]
	}

	return false, feedback
}

// handleRedirect integrates user feedback into the running system.
func (eo *EventOrchestrator) handleRedirect(msg string) {
	eo.cfg.OnProgress(fmt.Sprintf("User redirect: %s", msg))
	// In future: could guide in-flight teams or queue for next phase
	// For now: record it as validation feedback
	eo.brain.SetLastValidation(msg)
}

// createTeamsFromPlan chunks the plan into role-based teams.
// Heuristic: group sequential steps into teams when they share the same domain.
func (eo *EventOrchestrator) createTeamsFromPlan(plan []PlanStep) {
	// Clear old teams
	eo.brain.ResetTeams()

	if len(plan) == 0 {
		return
	}

	// Grouping heuristic: a run of consecutive steps with the same inferred role
	// becomes one team. Teams run concurrently; steps inside a team run in order.
	//
	// KNOWN LIMIT, measured rather than guessed. Parallelism here is bounded by
	// role DIVERSITY, not by the quota plan. A live run of an eleven-step plan
	// to build three independent Go packages produced exactly one team —
	// inferRoleFromStep only distinguishes db / frontend / api / general, and
	// every step in that plan was "general", so the whole project built
	// serially inside team-general-0 while the governor's ceiling of three sat
	// unused. The dispatcher itself is genuinely concurrent and genuinely
	// capped by the governor (see TestTeamDispatcher_RunsStepsConcurrently and
	// TestTeamDispatcher_QuotaCeilingSerialisesWork); it is the team formation
	// above it that hands it only one thing to run.
	//
	// The obvious fix — chop a long same-role run into several teams — is NOT
	// safe as things stand, and that is why it has not been done here. Steps in
	// different teams run at the same time, so splitting assumes the steps are
	// independent, and nothing in the plan says whether they are. PlanStep
	// carries no dependency information and the planner is never asked for any,
	// so "1. create the module" and "2. add a feature to the module" would be
	// dispatched concurrently into the same files. Doing this properly means
	// teaching the planner to declare dependencies and populating AddTeam's
	// dependsOn (which every caller currently passes as nil) — a plan-format
	// change, not a grouping tweak.

	var currentTeamSteps []int
	var currentRole string

	for i, step := range plan {
		role := inferRoleFromStep(step)

		if currentRole == "" {
			currentRole = role
			currentTeamSteps = []int{i}
		} else if role == currentRole {
			currentTeamSteps = append(currentTeamSteps, i)
		} else {
			// Start a new team
			teamID := fmt.Sprintf("team-%s-%d", currentRole, eo.brain.TeamCount())
			eo.brain.AddTeam(teamID, currentRole, currentTeamSteps, nil)

			currentRole = role
			currentTeamSteps = []int{i}
		}
	}

	// Flush last team
	if len(currentTeamSteps) > 0 {
		teamID := fmt.Sprintf("team-%s-%d", currentRole, eo.brain.TeamCount())
		eo.brain.AddTeam(teamID, currentRole, currentTeamSteps, nil)
	}
}

// CapturedChat wraps a ChatContext and captures its output.
type CapturedChat struct {
	Ctx    *ChatContext
	Output strings.Builder
	outMu  sync.Mutex
}

func (cc *CapturedChat) GetOutput() string {
	cc.outMu.Lock()
	defer cc.outMu.Unlock()
	return cc.Output.String()
}

// newChatContextForPhase creates a ChatContext that captures output.
func newChatContextForPhase(projectPath string, sessionID string) *CapturedChat {
	// Record the session before anything talks on it.
	//
	// The serial orchestrator does this at the top of every phase; the event
	// orchestrator never did, because nothing called it. The consequence is not
	// cosmetic: a session row is what makes a run visible in the UI and what the
	// messages saved during it hang off, so an event-driven build was one that
	// happened with no record that it had. That was survivable while this path
	// was dead code and is not once the seam routes real work here.
	//
	// A failure is logged and not fatal, matching every other CreateSession call
	// site: losing the transcript of a build is bad, refusing to build because
	// the transcript could not be opened is worse.
	// db.Ready guards the case this helper is reachable in and the serial
	// orchestrator's phases are not: being called with no database open at all.
	// CreateSession does not return an error for that, it panics on a nil
	// handle, so asking first is the only way not to.
	if db.Ready() {
		if err := createSessionFn(sessionID, projectPath, ""); err != nil {
			log.Printf("event orchestrator: create session %s: %v", sessionID, err)
		}
	}

	cc := &CapturedChat{
		Ctx: &ChatContext{
			ProjectPath:  projectPath,
			SessionID:    sessionID,
			Role:         RolePlanner,
			OnChunk:      nil,
			OnError:      func(string) {},
			OnToolCall:   func(string, any) {},
			OnToolResult: func(string, any, bool) {},
		},
	}

	// Capture output in OnChunk
	cc.Ctx.OnChunk = func(content string, _ bool) {
		if content == "" {
			return
		}
		cc.outMu.Lock()
		cc.Output.WriteString(content)
		cc.outMu.Unlock()
	}

	return cc
}

// newPhaseChat is newChatContextForPhase plus the run's own callbacks.
//
// Errors were the missing half. The bare constructor installs `OnError` as a
// no-op, so a provider call that failed — bad credentials, a model refusal, an
// unparseable response — produced a silent empty phase and nothing anywhere
// said so. Observed live: the planner failed on its first call, the plan came
// back empty, no teams were created, and the outer loop span all 200 iterations
// in six seconds logging "generating plan" each time. The only symptom was "max
// iterations reached", which points at the wrong thing entirely.
//
// Wiring cfg.OnError makes the first failure the one that gets reported. Wiring
// cfg.Cancel means a stopped run stops inside the phase rather than at the next
// phase boundary, which for a long builder turn is the difference between
// stopping and appearing not to.
func newPhaseChat(cfg OrchestratorConfig, sessionID string) *CapturedChat {
	cc := newChatContextForPhase(cfg.ProjectPath, sessionID)
	cc.Ctx.Cancel = cfg.Cancel
	// Model pin. Serial path sets this in stageChatContextCreation; event path
	// forgot. Result: SARA picked haiku, every phase + every TeamWorker step ran
	// at env default. One seam for both call sites (planner phases here,
	// TeamWorker.runStep) so it cannot drift again.
	cc.Ctx.ModelOverride = cfg.RequestedModel
	cc.Ctx.TaskID = cfg.TaskID
	if strings.TrimSpace(cfg.RequestedRole) != "" {
		cc.Ctx.Role = agentRoleFromLabel(cfg.RequestedRole)
	}
	if cfg.OnError != nil {
		cc.Ctx.OnError = cfg.OnError
	}
	return cc
}

func inferRoleFromStep(step PlanStep) string {
	title := strings.ToLower(step.Title)
	if strings.Contains(title, "db") || strings.Contains(title, "database") || strings.Contains(title, "schema") {
		return "db"
	}
	if strings.Contains(title, "frontend") || strings.Contains(title, "ui") || strings.Contains(title, "component") {
		return "frontend"
	}
	if strings.Contains(title, "api") || strings.Contains(title, "endpoint") || strings.Contains(title, "server") {
		return "api"
	}
	return "general"
}

func buildGrillPrompt(brief string) string {
	return fmt.Sprintf(`You are Matt Pocock's grill master. Interview the user about their project concept.
Start with the brief and ask clarifying questions to resolve design decisions.

User brief: %s

Run the grill interview and output a structured design.md file with all decisions resolved.`, brief)
}

func formatVocabulary(vocab map[string]string) string {
	var s strings.Builder
	for k, v := range vocab {
		s.WriteString(fmt.Sprintf("- %s: %s\n", k, v))
	}
	return s.String()
}

func extractPlanFromContext(output string) []PlanStep {
	return parsePlanFromText(output)
}

// eventPlannerContext assembles the documentation layers the planner reads.
//
// Empty layers are omitted rather than sent as a labelled blank. A prompt that
// says "PRD:" followed by nothing is worse than one that does not mention a PRD
// at all — it asserts a document exists and is empty, which invites the model to
// plan around an emptiness that is really just a project that has not written
// one yet. It also costs tokens to say nothing, which on the path that is
// supposed to be the efficient one is the wrong direction.
func (eo *EventOrchestrator) eventPlannerContext(req ProjectRequirements) string {
	var b strings.Builder
	add := func(label, body string) {
		body = strings.TrimSpace(body)
		if body == "" {
			return
		}
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(label)
		b.WriteString(":\n")
		b.WriteString(body)
	}
	add("PRD", req.PRD)
	add("Vocabulary", formatVocabulary(req.Vocabulary))
	add("Design", req.Design)
	add("Module Index", req.ModuleIndex)
	add("Previous validation feedback", eo.brain.GetLastValidation())
	return b.String()
}

func extractOrchestrationState(brain *OrchestrationBrain) *OrchestrationState {
	if brain == nil {
		return &OrchestrationState{}
	}
	return brain.StateSnapshot()
}

func (eo *EventOrchestrator) emitError(msg string) {
	eo.cfg.OnError(msg)
}

// fail ends the run: it records why, tells the caller's error sink, and emits
// the terminal event. Every unsuccessful exit from the event loop goes through
// here so that none of the three can drift apart again — two of the exits used
// to emit no terminal event at all, leaving anything waiting on the bus waiting
// for something that was never coming.
func (eo *EventOrchestrator) fail(err error) {
	if err == nil {
		return
	}
	eo.setFailure(err)
	eo.emitError(err.Error())
	eo.bus.Emit(Event{Type: EventProjectFailed, Timestamp: time.Now()})
}

// setFailure records the first reason the run ended badly and keeps it. First
// rather than last: the plan failure that started the collapse explains more
// than whatever fell over behind it.
func (eo *EventOrchestrator) setFailure(err error) {
	eo.failureMu.Lock()
	defer eo.failureMu.Unlock()
	if eo.failure == nil {
		eo.failure = err
	}
}

// Failure reports why the run ended badly, or nil if it did not.
func (eo *EventOrchestrator) Failure() error {
	eo.failureMu.Lock()
	defer eo.failureMu.Unlock()
	return eo.failure
}

// unfinishedTeams counts teams still owing work.
func (eo *EventOrchestrator) unfinishedTeams() int {
	return eo.brain.UnfinishedTeams()
}
