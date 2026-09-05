package ai

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"
)

var teamDispatcherStopTimeout = 30 * time.Second

// TeamWorker represents a single execution context for a team.
type TeamWorker struct {
	teamID   string
	role     string
	steps    []int
	brain    *OrchestrationBrain
	bus      *EventBus
	comms    *AgentCommsHub
	cfg      OrchestratorConfig
	ctx      context.Context
	cancel   context.CancelFunc
	wg       sync.WaitGroup
	maxTurns int
}

// ErrTeamCapReached is returned by DispatchTeam when the concurrency ceiling is
// already full.
//
// It is deliberately a distinct sentinel rather than a generic error: the
// orchestrator must treat "no room right now" as a reason to stop handing out
// work this round, and treat every other dispatch error as a failed run. Fold
// them together and hitting the cap aborts the whole project.
var ErrTeamCapReached = errors.New("team dispatcher: concurrency cap reached")

// TeamDispatcher manages the concurrent execution of multiple teams.
type TeamDispatcher struct {
	mu      sync.RWMutex
	brain   *OrchestrationBrain
	bus     *EventBus
	comms   *AgentCommsHub
	cfg     OrchestratorConfig
	workers map[string]*TeamWorker
	ctx     context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup
	// capFn reports how many teams may run at once, asked afresh on every
	// dispatch. A function rather than an int because the answer changes while
	// the project runs: the governor narrows the ceiling as the rolling window
	// fills, and a cap captured at construction would spend the opening
	// allowance right up to the wall.
	capFn func() int
}

// NewTeamDispatcher builds a dispatcher with a fixed concurrency ceiling.
// maxTeams <= 0 means "one at a time" rather than "unlimited": an unbounded
// default here is how a plan with thirty steps becomes thirty simultaneous
// Claude sessions.
func NewTeamDispatcher(brain *OrchestrationBrain, bus *EventBus, cfg OrchestratorConfig, maxTeams int, comms *AgentCommsHub) *TeamDispatcher {
	if maxTeams <= 0 {
		maxTeams = 1
	}
	return NewTeamDispatcherWithCap(brain, bus, cfg, func() int { return maxTeams }, comms)
}

// NewTeamDispatcherWithCap builds a dispatcher whose ceiling is re-read on
// every dispatch. This is the production constructor: capFn asks the quota
// governor.
func NewTeamDispatcherWithCap(brain *OrchestrationBrain, bus *EventBus, cfg OrchestratorConfig, capFn func() int, comms *AgentCommsHub) *TeamDispatcher {
	ctx, cancel := context.WithCancel(context.Background())
	if capFn == nil {
		capFn = func() int { return 1 }
	}
	return &TeamDispatcher{
		brain:   brain,
		bus:     bus,
		comms:   comms,
		cfg:     cfg,
		workers: make(map[string]*TeamWorker),
		ctx:     ctx,
		cancel:  cancel,
		capFn:   capFn,
	}
}

// MaxTeams reports the ceiling currently in force.
func (d *TeamDispatcher) MaxTeams() int {
	n := d.capFn()
	if n < 0 {
		return 0
	}
	return n
}

// DispatchTeam starts a team's worker goroutine.
func (d *TeamDispatcher) DispatchTeam(teamID string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	_, ok := d.workers[teamID]
	if ok {
		return fmt.Errorf("team %s already dispatched", teamID)
	}

	// The ceiling was never actually applied before this: maxTeams was stored
	// and read by nothing, so "max 4 parallel teams" dispatched every ready
	// team at once. It never showed up because this path had no callers.
	if live := d.activeLocked(); live >= d.MaxTeams() {
		return ErrTeamCapReached
	}

	t, err := d.brain.GetTeam(teamID)
	if err != nil {
		return err
	}

	// Check if dependencies are satisfied
	blocked := d.brain.TeamBlockedOn(teamID)
	if len(blocked) > 0 {
		return fmt.Errorf("team %s blocked by: %v", teamID, blocked)
	}

	ctx, cancel := context.WithCancel(d.ctx)
	worker := &TeamWorker{
		teamID:   teamID,
		role:     t.Role,
		steps:    t.AssignedSteps,
		brain:    d.brain,
		bus:      d.bus,
		comms:    d.comms,
		cfg:      d.cfg,
		ctx:      ctx,
		cancel:   cancel,
		maxTurns: OrchestratorStepMaxTurns,
	}

	d.workers[teamID] = worker
	if d.comms != nil {
		d.comms.Register(teamID, t.Role, "queued")
	}

	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		// Cancelling on the way out is what makes the cap self-releasing.
		// activeLocked counts a worker as live until its context is done, and
		// nothing else ever cancelled it — so without this the ceiling fills up
		// once and never lets another team through for the rest of the run.
		defer worker.cancel()
		worker.run()
	}()

	// Emit started event
	d.bus.Emit(Event{
		Type:      EventTeamStarted,
		Timestamp: time.Now(),
		TeamID:    teamID,
		Payload: EventPayload(
			"team_id", teamID,
			"role", t.Role,
			"steps", t.AssignedSteps,
		),
	})

	return nil
}

// markTeamRunning updates status and notifies comms that the team is running.
func (w *TeamWorker) markTeamRunning() {
	if err := w.brain.UpdateTeamStatus(w.teamID, "running"); err != nil {
		log.Printf("team dispatcher: failed to mark team %s running: %v", w.teamID, err)
	}
	if w.comms != nil {
		w.comms.Register(w.teamID, w.role, "running")
	}
}

// markTeamFailed consolidates the error-handling side-effect chain:
// updates status, persists feedback, notifies comms, and emits failure event.
func (w *TeamWorker) markTeamFailed(err error, stepIdx int) {
	if updateErr := w.brain.UpdateTeamStatus(w.teamID, "failed"); updateErr != nil {
		log.Printf("team dispatcher: failed to mark team %s failed: %v", w.teamID, updateErr)
	}
	if feedbackErr := w.brain.UpdateTeamFeedback(w.teamID, fmt.Sprintf("Error on step %d: %v", stepIdx, err)); feedbackErr != nil {
		log.Printf("team dispatcher: failed to persist team %s feedback: %v", w.teamID, feedbackErr)
	}
	if w.comms != nil {
		w.comms.Register(w.teamID, w.role, "failed")
	}
	w.bus.Emit(Event{
		Type:      EventTeamFailed,
		Timestamp: time.Now(),
		TeamID:    w.teamID,
		Payload: EventPayload(
			"error", err.Error(),
			"step_index", stepIdx,
		),
	})
}

// markTeamDone consolidates the completion side-effect chain:
// updates status, notifies comms, and emits done event.
func (w *TeamWorker) markTeamDone() {
	if err := w.brain.UpdateTeamStatus(w.teamID, "done"); err != nil {
		log.Printf("team dispatcher: failed to mark team %s done: %v", w.teamID, err)
	}
	if w.comms != nil {
		w.comms.Register(w.teamID, w.role, "done")
	}
	w.bus.Emit(Event{
		Type:      EventTeamDone,
		Timestamp: time.Now(),
		TeamID:    w.teamID,
		Payload:   EventPayload("team_id", w.teamID),
	})
}

// run executes the team's work on assigned steps.
func (w *TeamWorker) run() {
	// Mark team as running
	w.markTeamRunning()

	for i, stepIdx := range w.steps {
		plan := w.brain.GetPlan()
		if stepIdx >= len(plan) {
			continue
		}

		step := plan[stepIdx]

		// Run the step behind the same gates the classic orchestrator uses.
		err := w.runGatedStep(&step, stepIdx)
		if err != nil {
			// Mark as failed and stop this team
			w.markTeamFailed(err, stepIdx)
			return
		}

		// Mark step as done in place, rather than writing the whole snapshot
		// back. With teams running in parallel, a full-plan write-back reverts
		// whatever the other teams marked done since this worker took its
		// snapshot at the top of the loop.
		if !w.brain.MarkStepDone(stepIdx) {
			log.Printf("team dispatcher: team %s step %d out of plan range; not marked done", w.teamID, stepIdx)
		}

		w.bus.Emit(Event{
			Type:      EventTeamUpdated,
			Timestamp: time.Now(),
			TeamID:    w.teamID,
			Payload: EventPayload(
				"step_index", stepIdx,
				"step_title", step.Title,
				"status", "completed",
			),
		})

		// Tell the caller a step landed.
		//
		// The event bus above only reaches subscribers inside this package, and
		// nothing outside subscribes. OnPlanUpdate is the callback the outside
		// world actually watches — the task API turns it into stepsDone, and
		// SARA and the console read that. The serial orchestrator fires it after
		// every step; the parallel path fired it exactly once, when the plan was
		// created, so a parallel run reported "0 of 11" for its entire life no
		// matter how much of the project it had finished. An autonomous
		// supervisor watching a number that never moves cannot tell progress
		// from a hang.
		if w.cfg.OnPlanUpdate != nil {
			w.cfg.OnPlanUpdate(extractOrchestrationState(w.brain))
		}

		// Report step completion to lead via comms
		w.reportStepToLead(i+1, len(w.steps), step.Title)
	}

	// Mark team as done
	w.markTeamDone()
}

// runGatedStep builds one step and then holds it to the same bar the classic
// orchestrator holds its steps to: a cheap diff-only critic first, then the
// agentic reviewer, retrying with the feedback until the per-step attempt cap
// is spent.
//
// This is what makes the parallel path safe to switch on. Before it, a team
// worker built a step and marked it done the moment the builder said
// signal_done — no critic, no reviewer, nothing that could say "that is not
// actually what the step asked for". The classic loop has always gated every
// step, and turning on a faster path that quietly drops the quality gates would
// be trading correctness for wall-clock, which is the opposite of the point.
//
// The gates are per-step and stateless with respect to other teams, so they
// parallelise without coordination.
func (w *TeamWorker) runGatedStep(step *PlanStep, stepIdx int) error {
	cancel := w.ctx.Done()
	var lastReason string

	for attempt := 1; attempt <= OrchestratorMaxStepAttempts; attempt++ {
		select {
		case <-cancel:
			return w.ctx.Err()
		default:
		}

		step.Attempts = attempt
		if err := w.runStep(step, stepIdx); err != nil {
			return err
		}

		// Cheap diff-only critic before the expensive agentic reviewer. It stops
		// blocking after maxCriticRejectLoops consecutive rejections so a critic
		// that keeps objecting cannot stall the step forever — the reviewer is
		// then the decider, exactly as in the classic loop.
		if step.CriticRejects < maxCriticRejectLoops {
			if verdict, findings := orchestratorCriticStep(w.cfg, step, cancel); verdict == CriticReject {
				step.CriticRejects++
				step.LastFeedback = "Critic rejected the diff:\n" + findings
				lastReason = step.LastFeedback
				w.cfg.OnProgress(fmt.Sprintf("team %s step %d rejected by critic (attempt %d)", w.teamID, stepIdx, attempt))
				continue
			}
		}
		step.CriticRejects = 0

		state := &OrchestrationState{Owner: w.cfg.Owner, Repo: w.cfg.Repo, Plan: []PlanStep{*step}}
		verdict, msg := orchestratorReviewStep(w.cfg, state, step, cancel)
		switch verdict {
		case ReviewApprove:
			step.LastFeedback = ""
			return nil
		case ReviewReject:
			step.ReviewRejects++
			step.LastFeedback = msg
			lastReason = msg
			w.cfg.OnProgress(fmt.Sprintf("team %s step %d rejected by reviewer (attempt %d/%d)", w.teamID, stepIdx, attempt, OrchestratorMaxStepAttempts))

			// Coaching same as serial path: 1st REJECT → coach + retry; etc.
			if step.ReviewRejects == 1 {
				w.cfg.OnProgress(fmt.Sprintf("team %s step %d: spawning coach", w.teamID, stepIdx))
				newBrief := orchestratorCoachStep(w.cfg, step, cancel)
				if newBrief != "" {
					step.CoachingBrief = newBrief
					if w.cfg.OnCoach != nil {
						w.cfg.OnCoach(1, false)
					}
				}
			} else if step.ReviewRejects == 2 {
				w.cfg.OnProgress(fmt.Sprintf("team %s step %d: second REJECT, coaching again", w.teamID, stepIdx))
				newBrief := orchestratorCoachStep(w.cfg, step, cancel)
				if newBrief != "" {
					step.CoachingBrief = newBrief
					if w.cfg.OnCoach != nil {
						w.cfg.OnCoach(2, false)
					}
				}
			} else if step.ReviewRejects >= 3 {
				w.cfg.OnProgress(fmt.Sprintf("team %s step %d: escalating after %d REJECTs", w.teamID, stepIdx, step.ReviewRejects))
				if w.cfg.OnCoach != nil {
					w.cfg.OnCoach(step.ReviewRejects, true)
				}
			}
		default:
			// Inconclusive review is a soft pass in the classic loop, and it has
			// to be one here too: failing the team on "the reviewer could not
			// tell" would make an unreadable review fatal.
			step.LastFeedback = "Review inconclusive, advancing: " + msg
			return nil
		}
	}

	return fmt.Errorf("step %d never satisfied its acceptance in %d attempts: %s",
		stepIdx, OrchestratorMaxStepAttempts, summarise(lastReason, 300))
}

// runStep executes one plan step with the team's role (builder → reviewer pipeline).
func (w *TeamWorker) runStep(step *PlanStep, stepIdx int) error {
	// Create a session for this step
	sessionID := fmt.Sprintf("%s-team-%s-step-%d", w.cfg.SessionIDPrefix, w.teamID, stepIdx)

	// Build the step using the same shared builder contract as the main
	// orchestrator path, so team workers inherit the full autonomous loop
	// instead of a separate hardcoded prompt variant.
	cc := newPhaseChat(w.cfg, sessionID)
	if strings.TrimSpace(w.cfg.RequestedRole) == "" {
		cc.Ctx.Role = RoleAutonomousBuilder
	}
	// Execution is what resumption is for: this is the session that holds the
	// half-finished work a restart interrupted.
	cc.Ctx.ResumeSessionID = w.cfg.ResumeSessionID
	cc.Ctx.MaxTurns = 0
	cc.Ctx.AgentName = w.teamID
	cc.Ctx.AgentComms = w.comms

	state := &OrchestrationState{
		Owner: w.cfg.Owner,
		Repo:  w.cfg.Repo,
		Plan:  []PlanStep{*step},
	}
	// Narrowed to this step, exactly as the serial builder's is. It matters
	// more here: teams run concurrently, so the same whole-document prefix
	// would be paid by every team in flight at the same moment.
	contextDoc := ComposeDocContextFocused(w.cfg.ProjectPath, stepQuery(step), DocVocabulary, DocPRD, DocModules)
	if contextDoc == "" {
		contextDoc = readContextDoc(w.cfg.ProjectPath)
	}
	prompt := buildStepPromptWithContext(state, step, "", contextDoc)
	prompt += fmt.Sprintf("\n\nTEAM IDENTITY:\n- Team: %s (%s)\n- Use agent_list to see the lead and peer teams.\n- Use agent_send for narrow questions, handoffs, and review requests.\n- Use agent_receive for convenient inbox checks while working, and use agent_await only when you must block for a specific reply.\n- Keep messages concise and redact secrets or personal data.\n", w.teamID, w.role)

	// Run with bounded turns
	for turn := 0; turn < w.maxTurns; turn++ {
		select {
		case <-w.ctx.Done():
			return w.ctx.Err()
		default:
		}

		// Each builder turn is one `claude -p` session and is held to
		// sessionBudget. Overrunning it fails the step (and so the team): the
		// outer loop then re-plans and spends an iteration on it, which is the
		// bounded cost — the unbounded one was a stuck session eating the whole
		// task wall clock with nothing to show for it.
		if runBoundedSession(w.cfg, "execute", cc, prompt) {
			return fmt.Errorf("session budget hit: team %s step %d exceeded %s", w.teamID, stepIdx, sessionBudget)
		}

		// Check if builder called signal_done (would be in the output)
		output := cc.GetOutput()
		if strings.Contains(output, "signal_done") {
			return nil
		}

		// Prepare for next turn if needed
		prompt = "Continue working on this step."
	}

	return fmt.Errorf("step timed out after %d turns", w.maxTurns)
}

// reportStepToLead sends a progress message from team to lead via agent comms.
func (w *TeamWorker) reportStepToLead(stepNum, totalSteps int, stepTitle string) {
	if w.comms == nil {
		return
	}
	msg := fmt.Sprintf("Step %d/%d done: %s", stepNum, totalSteps, stepTitle)
	if _, err := w.comms.Send(w.teamID, "lead", "progress", msg, ""); err != nil {
		log.Printf("team dispatcher: failed to send progress to lead: %v", err)
	}
}

// Stop gracefully stops all workers.
func (d *TeamDispatcher) Stop() {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.cancel()

	// Wait for all to finish or timeout
	done := make(chan struct{})
	go func() {
		d.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(teamDispatcherStopTimeout):
	}
}

// Wait blocks until all dispatched teams are done.
func (d *TeamDispatcher) Wait() {
	d.wg.Wait()
}

// ActiveTeams returns count of running teams.
func (d *TeamDispatcher) ActiveTeams() int {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.activeLocked()
}

// activeLocked counts live workers. The caller must already hold d.mu — the cap
// check in DispatchTeam runs inside the same critical section as the insert, so
// two callers cannot both see room and both take the last slot.
func (d *TeamDispatcher) activeLocked() int {
	count := 0
	for _, w := range d.workers {
		// A worker with no context is registered but not yet running. It still
		// holds its slot — the team is spoken for, and releasing it would let a
		// second worker start on the same team.
		if w == nil || w.ctx == nil {
			count++
			continue
		}
		select {
		case <-w.ctx.Done():
		default:
			count++
		}
	}
	return count
}
