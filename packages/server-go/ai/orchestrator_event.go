package ai

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
)

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
}

// RunEventOrchestratorAsState wraps the event orchestrator to match the RunAutonomousProject signature.
// This is a compatibility wrapper that converts the new event-driven orchestrator to return OrchestrationState.
func RunEventOrchestratorAsState(cfg OrchestratorConfig) (*OrchestrationState, error) {
	brain, err := RunEventOrchestrator(cfg)
	if err != nil {
		return nil, err
	}

	return extractOrchestrationState(brain), nil
}

// RunEventOrchestrator starts the event-driven orchestrator.
func RunEventOrchestrator(cfg OrchestratorConfig) (*OrchestrationBrain, error) {
	ctx, cancel := context.WithCancel(context.Background())

	brain, err := NewOrchestrationBrain(cfg.ProjectPath, cfg.Owner, cfg.Repo, cfg.Brief, cfg.SessionIDPrefix)
	if err != nil {
		cancel()
		return nil, err
	}

	bus := NewEventBus()
	comms := AgentCommsForProject(cfg.ProjectPath)
	comms.Register("lead", "orchestrator", "running")
	dispatcher := NewTeamDispatcher(brain, bus, cfg, 4, comms) // max 4 parallel teams

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

	// Run the main loop
	eo.wg.Add(1)
	go func() {
		defer eo.wg.Done()
		eo.eventLoop()
	}()

	return brain, nil
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

	// Phase 1: Intake (design, PRD, vocabulary)
	if err := eo.phaseIntake(); err != nil {
		eo.emitError(fmt.Sprintf("intake failed: %v", err))
		return
	}

	// Phase 2: Planning
	if err := eo.phasePlan(); err != nil {
		eo.emitError(fmt.Sprintf("planning failed: %v", err))
		return
	}

	// Phase 3+: Execute with team dispatch + validation loop
	for eo.brain.OuterIterations < eo.maxOuterIterations {
		eo.brain.OuterIterations++
		eo.cfg.OnPhase("execute", fmt.Sprintf("iteration %d/%d", eo.brain.OuterIterations, eo.maxOuterIterations))

		// Dispatch ready teams
		if err := eo.phaseDispatchTeams(); err != nil {
			eo.emitError(fmt.Sprintf("team dispatch failed: %v", err))
			return
		}

		// Wait for all teams to complete or fail
		if err := eo.phaseWaitTeams(teamDone, teamFailed, userRedirect, cancelEv); err != nil {
			if err == context.Canceled {
				eo.bus.Emit(Event{Type: EventProjectCanceled, Timestamp: time.Now()})
				return
			}
			eo.emitError(fmt.Sprintf("team execution failed: %v", err))
			return
		}

		// Check for failures
		if eo.brain.AnyTeamFailed() {
			eo.cfg.OnProgress("Some teams failed; re-planning...")
			if err := eo.phasePlan(); err != nil {
				eo.emitError(fmt.Sprintf("replanning failed: %v", err))
				return
			}
			continue
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
		eo.brain.LastValidation = feedback

		if err := eo.phasePlan(); err != nil {
			eo.emitError(fmt.Sprintf("replanning after validation failed: %v", err))
			return
		}
	}

	eo.emitError(fmt.Sprintf("max iterations (%d) reached", eo.maxOuterIterations))
	eo.bus.Emit(Event{Type: EventProjectFailed, Timestamp: time.Now()})
}

// phaseIntake runs design grill → PRD distillation.
func (eo *EventOrchestrator) phaseIntake() error {
	eo.cfg.OnPhase("intake", "grill interview")

	sessionID := fmt.Sprintf("%s-intake-grill", eo.cfg.SessionIDPrefix)
	cc := newChatContextForPhase(eo.cfg.ProjectPath, sessionID)
	cc.Ctx.Role = RoleGriller

	// Grill phase (design)
	eo.cfg.chatFnFor()(cc.Ctx, buildGrillPrompt(eo.cfg.Brief))

	design := strings.TrimSpace(cc.GetOutput())
	if design != "" {
		_ = WriteDoc(eo.cfg.ProjectPath, DocDesign, design)
	}

	eo.bus.Emit(Event{Type: EventDesignReady, Timestamp: time.Now()})

	// PRD phase
	eo.cfg.OnPhase("intake", "PRD distillation")
	sessionID = fmt.Sprintf("%s-intake-prd", eo.cfg.SessionIDPrefix)
	cc = newChatContextForPhase(eo.cfg.ProjectPath, sessionID)
	cc.Ctx.Role = RolePRDWriter

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

	sessionID := fmt.Sprintf("%s-plan-%d", eo.cfg.SessionIDPrefix, eo.brain.OuterIterations)
	cc := newChatContextForPhase(eo.cfg.ProjectPath, sessionID)
	cc.Ctx.Role = RolePlanner

	// Build planner prompt with current requirements
	req := eo.brain.Requirements
	prompt := fmt.Sprintf(`You are the project planner. Create a concrete numbered plan to implement the project.

PRD:
%s

Vocabulary:
%s

Design:
%s

Module Index:
%s

Previous feedback (if any): %s

Return a JSON plan array with objects: {index, title, body, acceptance_criteria}`,
		req.PRD, formatVocabulary(req.Vocabulary), req.Design, req.ModuleIndex, eo.brain.LastValidation)

	eo.cfg.chatFnFor()(cc.Ctx, prompt)

	// Parse plan from context output
	output := cc.GetOutput()
	plan := extractPlanFromContext(output)
	eo.brain.UpdatePlan(plan)

	// Create teams from plan: chunk steps into role-based teams
	eo.createTeamsFromPlan(plan)

	eo.bus.Emit(Event{Type: EventPlanReady, Timestamp: time.Now(), Payload: EventPayload("steps", len(plan))})

	if eo.cfg.OnPlanUpdate != nil {
		eo.cfg.OnPlanUpdate(extractOrchestrationState(eo.brain))
	}

	return nil
}

// phaseDispatchTeams dispatches all ready teams.
func (eo *EventOrchestrator) phaseDispatchTeams() error {
	ready := eo.brain.ReadyTeams()
	for _, t := range ready {
		if err := eo.dispatcher.DispatchTeam(t.ID); err != nil {
			return fmt.Errorf("dispatch team %s: %w", t.ID, err)
		}
		eo.cfg.OnProgress(fmt.Sprintf("Dispatched team %s (%s)", t.ID, t.Role))
	}
	return nil
}

// phaseWaitTeams waits for all teams to complete and handles redirects.
func (eo *EventOrchestrator) phaseWaitTeams(teamDone, teamFailed, userRedirect, cancelEv <-chan Event) error {
	for {
		select {
		case <-eo.ctx.Done():
			return eo.ctx.Err()
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
	sessionID := fmt.Sprintf("%s-validate-%d", eo.cfg.SessionIDPrefix, eo.brain.OuterIterations)
	cc := newChatContextForPhase(eo.cfg.ProjectPath, sessionID)
	cc.Ctx.Role = RoleReviewer

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
	eo.brain.LastValidation = msg
}

// createTeamsFromPlan chunks the plan into role-based teams.
// Heuristic: group sequential steps into teams when they share the same domain.
func (eo *EventOrchestrator) createTeamsFromPlan(plan []PlanStep) {
	// Clear old teams
	eo.brain.Teams = make(map[string]TeamState)

	if len(plan) == 0 {
		return
	}

	// Grouping heuristic: consecutive steps with similar titles stay in same team
	// For now: simple approach — assign steps to teams based on their index
	// DB steps (0, 1, 2) → DB team
	// Frontend steps (3, 4, 5) → Frontend team
	// etc.

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
			teamID := fmt.Sprintf("team-%s-%d", currentRole, len(eo.brain.Teams))
			eo.brain.AddTeam(teamID, currentRole, currentTeamSteps, nil)

			currentRole = role
			currentTeamSteps = []int{i}
		}
	}

	// Flush last team
	if len(currentTeamSteps) > 0 {
		teamID := fmt.Sprintf("team-%s-%d", currentRole, len(eo.brain.Teams))
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
	cc := &CapturedChat{
		Ctx: &ChatContext{
			ProjectPath:  projectPath,
			SessionID:    sessionID,
			Role:         RolePlanner,
			OnChunk:      func(content string, _ bool) {},
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
	// Parse plan from context output (simplified for now)
	// In production: extract JSON or structured format from the AI output
	// For now, return empty plan - this will be extended to parse the actual plan
	return []PlanStep{}
}

func extractOrchestrationState(brain *OrchestrationBrain) *OrchestrationState {
	return &OrchestrationState{
		Repo:            brain.Repo,
		Owner:           brain.Owner,
		Brief:           brain.Brief,
		Plan:            brain.Plan,
		OuterIterations: brain.OuterIterations,
		StartedAt:       brain.StartedAt.Format(time.RFC3339),
		UpdatedAt:       brain.UpdatedAt.Format(time.RFC3339),
		CompletedAt:     brain.CompletedAt.Format(time.RFC3339),
		LastValidation:  brain.LastValidation,
	}
}

func (eo *EventOrchestrator) emitError(msg string) {
	eo.cfg.OnError(msg)
}
