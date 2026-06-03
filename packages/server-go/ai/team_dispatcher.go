package ai

import (
	"context"
	"fmt"
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

// TeamDispatcher manages the concurrent execution of multiple teams.
type TeamDispatcher struct {
	mu       sync.RWMutex
	brain    *OrchestrationBrain
	bus      *EventBus
	comms    *AgentCommsHub
	cfg      OrchestratorConfig
	workers  map[string]*TeamWorker
	ctx      context.Context
	cancel   context.CancelFunc
	wg       sync.WaitGroup
	maxTeams int // limit concurrent teams to avoid overload
}

func NewTeamDispatcher(brain *OrchestrationBrain, bus *EventBus, cfg OrchestratorConfig, maxTeams int, comms *AgentCommsHub) *TeamDispatcher {
	ctx, cancel := context.WithCancel(context.Background())
	return &TeamDispatcher{
		brain:    brain,
		bus:      bus,
		comms:    comms,
		cfg:      cfg,
		workers:  make(map[string]*TeamWorker),
		ctx:      ctx,
		cancel:   cancel,
		maxTeams: maxTeams,
	}
}

// DispatchTeam starts a team's worker goroutine.
func (d *TeamDispatcher) DispatchTeam(teamID string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	_, ok := d.workers[teamID]
	if ok {
		return fmt.Errorf("team %s already dispatched", teamID)
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

// run executes the team's work on assigned steps.
func (w *TeamWorker) run() {
	// Mark team as running
	_ = w.brain.UpdateTeamStatus(w.teamID, "running")
	if w.comms != nil {
		w.comms.Register(w.teamID, w.role, "running")
	}

	for _, stepIdx := range w.steps {
		plan := w.brain.GetPlan()
		if stepIdx >= len(plan) {
			continue
		}

		step := plan[stepIdx]

		// Run the step (simplified: just invoke builder role)
		err := w.runStep(&step, stepIdx)
		if err != nil {
			// Mark as failed and stop this team
			_ = w.brain.UpdateTeamStatus(w.teamID, "failed")
			_ = w.brain.UpdateTeamFeedback(w.teamID, fmt.Sprintf("Error on step %d: %v", stepIdx, err))
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
			return
		}

		// Mark step as done
		if stepIdx < len(plan) {
			plan[stepIdx].Done = true
			plan[stepIdx].UpdatedAt = time.Now().Format(time.RFC3339)
			_ = w.brain.UpdatePlan(plan)
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
	}

	// Mark team as done
	_ = w.brain.UpdateTeamStatus(w.teamID, "done")
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

// runStep executes one plan step with the team's role (builder → reviewer pipeline).
func (w *TeamWorker) runStep(step *PlanStep, stepIdx int) error {
	// Create a session for this step
	sessionID := fmt.Sprintf("%s-team-%s-step-%d", w.cfg.SessionIDPrefix, w.teamID, stepIdx)

	// Build step
	cc := newChatContextForPhase(w.cfg.ProjectPath, sessionID)
	cc.Ctx.Role = RoleAutonomousBuilder
	cc.Ctx.ModelOverride = "" // Use default model for now
	cc.Ctx.AgentName = w.teamID
	cc.Ctx.AgentComms = w.comms

	prompt := fmt.Sprintf(`You are building step %d: %s

Team identity: %s (%s)

Team communication:
- Use agent_list to see the lead and peer teams.
- Use agent_send for narrow questions, handoffs, and review requests.
- Use agent_receive for convenient inbox checks while working, and use agent_await only when you must block for a specific reply.
- Keep messages concise and redact secrets or personal data.

Acceptance Criteria:
%s

Plan:
%s

Your task: implement this step completely. Use the tools to edit files, run commands, test, and commit.
When done, call signal_done with a summary of what you built.`,
		stepIdx, step.Title, w.teamID, w.role, step.Acceptance, step.Body)

	// Run with bounded turns
	for turn := 0; turn < w.maxTurns; turn++ {
		select {
		case <-w.ctx.Done():
			return w.ctx.Err()
		default:
		}

		// This is a simplified version; in reality you'd integrate with the full ChatContext
		// and tool execution loop from context.go
		cc.Ctx.MaxTurns = 1 // One turn at a time for now

		w.cfg.chatFnFor()(cc.Ctx, prompt)

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

	count := 0
	for _, w := range d.workers {
		select {
		case <-w.ctx.Done():
		default:
			count++
		}
	}
	return count
}
