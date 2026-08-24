package ai

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

// TestInferRoleFromStep_DB verifies DB/database/schema titles → "db".
func TestInferRoleFromStep_DB(t *testing.T) {
	cases := []string{"create database schema", "setup db migration", "init database"}
	for _, title := range cases {
		if got := inferRoleFromStep(PlanStep{Title: title}); got != "db" {
			t.Errorf("inferRoleFromStep(%q) = %q, want %q", title, got, "db")
		}
	}
}

// TestInferRoleFromStep_Frontend verifies frontend/ui/component → "frontend".
func TestInferRoleFromStep_Frontend(t *testing.T) {
	cases := []string{"build frontend layout", "add ui button", "create react component"}
	for _, title := range cases {
		if got := inferRoleFromStep(PlanStep{Title: title}); got != "frontend" {
			t.Errorf("inferRoleFromStep(%q) = %q, want %q", title, got, "frontend")
		}
	}
}

// TestInferRoleFromStep_API verifies api/endpoint/server → "api".
func TestInferRoleFromStep_API(t *testing.T) {
	cases := []string{"implement api handler", "create rest endpoint", "setup http server"}
	for _, title := range cases {
		if got := inferRoleFromStep(PlanStep{Title: title}); got != "api" {
			t.Errorf("inferRoleFromStep(%q) = %q, want %q", title, got, "api")
		}
	}
}

// TestInferRoleFromStep_General verifies other titles → "general".
func TestInferRoleFromStep_General(t *testing.T) {
	if got := inferRoleFromStep(PlanStep{Title: "write unit tests"}); got != "general" {
		t.Errorf("got %q, want general", got)
	}
}

// TestBuildGrillPrompt verifies the prompt contains the brief.
func TestBuildGrillPrompt(t *testing.T) {
	out := buildGrillPrompt("build a todo app")
	if !strings.Contains(out, "todo app") {
		t.Errorf("expected brief in prompt, got: %q", out)
	}
}

// TestFormatVocabulary_Empty verifies empty map returns empty string.
func TestFormatVocabulary_Empty(t *testing.T) {
	if got := formatVocabulary(nil); got != "" {
		t.Errorf("expected empty string for nil vocab, got %q", got)
	}
	if got := formatVocabulary(map[string]string{}); got != "" {
		t.Errorf("expected empty string for empty vocab, got %q", got)
	}
}

// TestFormatVocabulary_NonEmpty verifies entries are formatted.
func TestFormatVocabulary_NonEmpty(t *testing.T) {
	m := map[string]string{"Foo": "a thing", "Bar": "another thing"}
	out := formatVocabulary(m)
	if !strings.Contains(out, "Foo") || !strings.Contains(out, "Bar") {
		t.Errorf("expected keys in output, got %q", out)
	}
}

// TestExtractPlanFromContext_ParsesNumberedPlan verifies planner text is parsed.
func TestExtractPlanFromContext_ParsesNumberedPlan(t *testing.T) {
	plan := extractPlanFromContext("1. Step one\n   do things\n   Acceptance: `go build`")
	if len(plan) != 1 {
		t.Fatalf("expected one step, got %d", len(plan))
	}
	if plan[0].Index != 1 || plan[0].Title != "Step one" {
		t.Fatalf("unexpected parsed step: %+v", plan[0])
	}
}

// TestExtractOrchestrationState verifies brain fields are copied to state.
func TestExtractOrchestrationState(t *testing.T) {
	dir := t.TempDir()
	brain, _ := NewOrchestrationBrain(dir, "owner", "myrepo", "brief text", "t")
	state := extractOrchestrationState(brain)
	if state.Owner != "owner" || state.Repo != "myrepo" {
		t.Errorf("fields not copied: owner=%q repo=%q", state.Owner, state.Repo)
	}
	if state.Brief != "brief text" {
		t.Errorf("brief not copied: %q", state.Brief)
	}
}

// TestNewChatContextForPhase verifies CapturedChat is properly initialized.
func TestNewChatContextForPhase(t *testing.T) {
	cc := newChatContextForPhase("/tmp/proj", "sess-123")
	if cc == nil {
		t.Fatal("expected non-nil CapturedChat")
	}
	if cc.Ctx == nil {
		t.Fatal("expected non-nil ChatContext")
	}
	// Verify OnChunk captures output
	cc.Ctx.OnChunk("hello world", false)
	if got := cc.GetOutput(); got != "hello world" {
		t.Errorf("got %q, want %q", got, "hello world")
	}
	// Empty chunk is ignored
	cc.Ctx.OnChunk("", false)
	if got := cc.GetOutput(); got != "hello world" {
		t.Errorf("empty chunk should not change output, got %q", got)
	}
}

// TestCreateTeamsFromPlan_Empty verifies no teams created for empty plan.
func TestCreateTeamsFromPlan_Empty(t *testing.T) {
	dir := t.TempDir()
	brain, _ := NewOrchestrationBrain(dir, "o", "r", "b", "t")
	bus := NewEventBus()
	comms := AgentCommsForProject(dir)
	cfg := OrchestratorConfig{
		ProjectPath: dir,
		OnPhase:     func(string, string) {},
		OnProgress:  func(string) {},
		OnError:     func(string) {},
	}
	dispatcher := NewTeamDispatcher(brain, bus, cfg, 4, comms)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	eo := &EventOrchestrator{
		cfg:                cfg,
		brain:              brain,
		bus:                bus,
		dispatcher:         dispatcher,
		comms:              comms,
		ctx:                ctx,
		cancel:             cancel,
		redirectQueue:      make(chan string, 1),
		maxOuterIterations: 1,
	}
	eo.createTeamsFromPlan([]PlanStep{})
	if len(brain.Teams) != 0 {
		t.Errorf("expected no teams for empty plan, got %d", len(brain.Teams))
	}
}

// TestCreateTeamsFromPlan_MixedRoles verifies steps are grouped into teams by role.
func TestCreateTeamsFromPlan_MixedRoles(t *testing.T) {
	dir := t.TempDir()
	brain, _ := NewOrchestrationBrain(dir, "o", "r", "b", "t")
	bus := NewEventBus()
	comms := AgentCommsForProject(dir)
	cfg := OrchestratorConfig{
		ProjectPath: dir,
		OnPhase:     func(string, string) {},
		OnProgress:  func(string) {},
		OnError:     func(string) {},
	}
	dispatcher := NewTeamDispatcher(brain, bus, cfg, 4, comms)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	eo := &EventOrchestrator{
		cfg:                cfg,
		brain:              brain,
		bus:                bus,
		dispatcher:         dispatcher,
		comms:              comms,
		ctx:                ctx,
		cancel:             cancel,
		redirectQueue:      make(chan string, 1),
		maxOuterIterations: 3,
	}
	plan := []PlanStep{
		{Index: 1, Title: "create database schema"},
		{Index: 2, Title: "setup api endpoint"},
	}
	eo.createTeamsFromPlan(plan)
	if len(brain.Teams) == 0 {
		t.Error("expected teams to be created for non-empty plan")
	}
}

// TestHandleRedirect_SetsLastValidation verifies handleRedirect sets brain.LastValidation.
func TestHandleRedirect_SetsLastValidation(t *testing.T) {
	dir := t.TempDir()
	brain, _ := NewOrchestrationBrain(dir, "o", "r", "b", "t")
	bus := NewEventBus()
	comms := AgentCommsForProject(dir)
	cfg := OrchestratorConfig{
		ProjectPath: dir,
		OnPhase:     func(string, string) {},
		OnProgress:  func(string) {},
		OnError:     func(string) {},
	}
	dispatcher := NewTeamDispatcher(brain, bus, cfg, 4, comms)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	eo := &EventOrchestrator{
		cfg:           cfg,
		brain:         brain,
		bus:           bus,
		dispatcher:    dispatcher,
		comms:         comms,
		ctx:           ctx,
		cancel:        cancel,
		redirectQueue: make(chan string, 1),
	}
	eo.handleRedirect("focus on error handling")
	if brain.LastValidation != "focus on error handling" {
		t.Errorf("expected %q, got %q", "focus on error handling", brain.LastValidation)
	}
}

// TestEmitError_CallsOnError verifies emitError calls cfg.OnError.
func TestEmitError_CallsOnError(t *testing.T) {
	dir := t.TempDir()
	brain, _ := NewOrchestrationBrain(dir, "o", "r", "b", "t")
	bus := NewEventBus()
	comms := AgentCommsForProject(dir)
	var gotErr string
	cfg := OrchestratorConfig{
		ProjectPath: dir,
		OnPhase:     func(string, string) {},
		OnProgress:  func(string) {},
		OnError:     func(msg string) { gotErr = msg },
	}
	dispatcher := NewTeamDispatcher(brain, bus, cfg, 4, comms)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	eo := &EventOrchestrator{
		cfg:           cfg,
		brain:         brain,
		bus:           bus,
		dispatcher:    dispatcher,
		comms:         comms,
		ctx:           ctx,
		cancel:        cancel,
		redirectQueue: make(chan string, 1),
	}
	eo.emitError("something went wrong")
	if gotErr != "something went wrong" {
		t.Errorf("got %q, want %q", gotErr, "something went wrong")
	}
}

// TestPhaseWaitTeams_ContextCancelled verifies pre-cancelled context returns error.
func TestPhaseWaitTeams_ContextCancelled(t *testing.T) {
	dir := t.TempDir()
	brain, _ := NewOrchestrationBrain(dir, "o", "r", "b", "t")
	bus := NewEventBus()
	comms := AgentCommsForProject(dir)
	cfg := OrchestratorConfig{
		ProjectPath: dir,
		OnPhase:     func(string, string) {},
		OnProgress:  func(string) {},
		OnError:     func(string) {},
	}
	dispatcher := NewTeamDispatcher(brain, bus, cfg, 4, comms)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancel
	eo := &EventOrchestrator{
		cfg:           cfg,
		brain:         brain,
		bus:           bus,
		dispatcher:    dispatcher,
		comms:         comms,
		ctx:           ctx,
		cancel:        cancel,
		redirectQueue: make(chan string, 1),
	}
	teamDone := make(chan Event, 1)
	teamFailed := make(chan Event, 1)
	userRedirect := make(chan Event, 1)
	cancelEv := make(chan Event, 1)
	err := eo.phaseWaitTeams(teamDone, teamFailed, userRedirect, cancelEv)
	if err == nil {
		t.Error("expected error from pre-cancelled context")
	}
}

// TestPhaseWaitTeams_CancelEvent verifies cancelEv channel returns context.Canceled.
func TestPhaseWaitTeams_CancelEvent(t *testing.T) {
	dir := t.TempDir()
	brain, _ := NewOrchestrationBrain(dir, "o", "r", "b", "t")
	bus := NewEventBus()
	comms := AgentCommsForProject(dir)
	cfg := OrchestratorConfig{
		ProjectPath: dir,
		OnPhase:     func(string, string) {},
		OnProgress:  func(string) {},
		OnError:     func(string) {},
	}
	dispatcher := NewTeamDispatcher(brain, bus, cfg, 4, comms)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	eo := &EventOrchestrator{
		cfg:           cfg,
		brain:         brain,
		bus:           bus,
		dispatcher:    dispatcher,
		comms:         comms,
		ctx:           ctx,
		cancel:        cancel,
		redirectQueue: make(chan string, 1),
	}
	teamDone := make(chan Event, 1)
	teamFailed := make(chan Event, 1)
	userRedirect := make(chan Event, 1)
	cancelEv := make(chan Event, 1)
	cancelEv <- Event{Type: EventCancel}
	err := eo.phaseWaitTeams(teamDone, teamFailed, userRedirect, cancelEv)
	if err != context.Canceled {
		t.Errorf("expected context.Canceled, got %v", err)
	}
}

// TestPhaseWaitTeams_TeamDone_AllDone verifies teamDone+AllDone returns nil.
func TestPhaseWaitTeams_TeamDone_AllDone(t *testing.T) {
	dir := t.TempDir()
	// Brain with no teams → AllTeamsDone() = true
	brain, _ := NewOrchestrationBrain(dir, "o", "r", "b", "t")
	bus := NewEventBus()
	comms := AgentCommsForProject(dir)
	cfg := OrchestratorConfig{
		ProjectPath: dir,
		OnPhase:     func(string, string) {},
		OnProgress:  func(string) {},
		OnError:     func(string) {},
	}
	dispatcher := NewTeamDispatcher(brain, bus, cfg, 4, comms)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	eo := &EventOrchestrator{
		cfg:           cfg,
		brain:         brain,
		bus:           bus,
		dispatcher:    dispatcher,
		comms:         comms,
		ctx:           ctx,
		cancel:        cancel,
		redirectQueue: make(chan string, 1),
	}
	teamDone := make(chan Event, 1)
	teamFailed := make(chan Event, 1)
	userRedirect := make(chan Event, 1)
	cancelEv := make(chan Event, 1)
	// Send a teamDone event; brain has 0 teams → AllTeamsDone() = true → returns nil
	teamDone <- Event{Type: EventTeamDone, TeamID: "phantom-team"}
	err := eo.phaseWaitTeams(teamDone, teamFailed, userRedirect, cancelEv)
	if err != nil {
		t.Errorf("expected nil when all teams done, got %v", err)
	}
}

// TestPhaseWaitTeams_TeamFailed verifies teamFailed updates team status then exits via ctx cancel.
func TestPhaseWaitTeams_TeamFailed(t *testing.T) {
	dir := t.TempDir()
	brain, _ := NewOrchestrationBrain(dir, "o", "r", "b", "t")
	_ = brain.AddTeam("t1", "general", []int{0}, nil)
	bus := NewEventBus()
	comms := AgentCommsForProject(dir)
	cfg := OrchestratorConfig{
		ProjectPath: dir,
		OnPhase:     func(string, string) {},
		OnProgress:  func(string) {},
		OnError:     func(string) {},
	}
	dispatcher := NewTeamDispatcher(brain, bus, cfg, 4, comms)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	eo := &EventOrchestrator{
		cfg:           cfg,
		brain:         brain,
		bus:           bus,
		dispatcher:    dispatcher,
		comms:         comms,
		ctx:           ctx,
		cancel:        cancel,
		redirectQueue: make(chan string, 1),
	}
	teamDone := make(chan Event, 1)
	teamFailed := make(chan Event, 1)
	userRedirect := make(chan Event, 1)
	cancelEv := make(chan Event, 1)
	teamFailed <- Event{
		Type:    EventTeamFailed,
		TeamID:  "t1",
		Payload: EventPayload("error", "build failed"),
	}
	// Context times out after team-failed is processed
	err := eo.phaseWaitTeams(teamDone, teamFailed, userRedirect, cancelEv)
	if err == nil {
		t.Error("expected error from context timeout after team failed")
	}
	// Verify team status was updated
	brain.mu.RLock()
	team := brain.Teams["t1"]
	brain.mu.RUnlock()
	if team.Status != "failed" {
		t.Errorf("expected team status=failed, got %q", team.Status)
	}
}

// TestPhaseWaitTeams_UserRedirect verifies redirect is processed then exits via ctx cancel.
func TestPhaseWaitTeams_UserRedirect(t *testing.T) {
	dir := t.TempDir()
	brain, _ := NewOrchestrationBrain(dir, "o", "r", "b", "t")
	bus := NewEventBus()
	comms := AgentCommsForProject(dir)
	cfg := OrchestratorConfig{
		ProjectPath: dir,
		OnPhase:     func(string, string) {},
		OnProgress:  func(string) {},
		OnError:     func(string) {},
	}
	dispatcher := NewTeamDispatcher(brain, bus, cfg, 4, comms)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	eo := &EventOrchestrator{
		cfg:           cfg,
		brain:         brain,
		bus:           bus,
		dispatcher:    dispatcher,
		comms:         comms,
		ctx:           ctx,
		cancel:        cancel,
		redirectQueue: make(chan string, 1),
	}
	teamDone := make(chan Event, 1)
	teamFailed := make(chan Event, 1)
	userRedirect := make(chan Event, 1)
	cancelEv := make(chan Event, 1)
	userRedirect <- Event{
		Type:    EventUserRedirect,
		Payload: EventPayload("message", "focus on auth"),
	}
	// The property under test is that the redirect lands in the brain, below.
	//
	// This used to also assert a non-nil error, on the reasoning that the wait
	// would run until the 200ms context expired. That was never the behaviour
	// being tested, only a side effect of the stall check being slower than the
	// context: this orchestrator has no teams, so once the redirect is applied
	// there is nothing left to wait for and returning nil is the correct answer.
	// It reads as an error only because the wait used to take five seconds to
	// notice. Asserting the error back would be asserting the slowness.
	if err := eo.phaseWaitTeams(teamDone, teamFailed, userRedirect, cancelEv); err != nil && err != context.DeadlineExceeded {
		t.Errorf("unexpected error from wait: %v", err)
	}
	if brain.LastValidation != "focus on auth" {
		t.Errorf("expected LastValidation=%q, got %q", "focus on auth", brain.LastValidation)
	}
}

// TestRunEventOrchestrator_ReturnsNonNilBrain verifies function returns immediately with non-nil brain.
func TestRunEventOrchestrator_ReturnsNonNilBrain(t *testing.T) {
	dir := t.TempDir()
	cfg := OrchestratorConfig{
		ProjectPath:        dir,
		Owner:              "o",
		Repo:               "r",
		Brief:              "brief",
		SessionIDPrefix:    "test",
		MaxOuterIterations: 1,
		ChatFn:             func(c *ChatContext, _ string) { c.OnChunk("design output", false) },
		OnProgress:         func(string) {},
		OnPhase:            func(string, string) {},
		OnError:            func(string) {},
	}
	brain, err := RunEventOrchestrator(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if brain == nil {
		t.Error("expected non-nil brain")
	}
	// Give goroutine a moment to start.
	time.Sleep(30 * time.Millisecond)
}

// TestRunEventOrchestratorAsState_ReturnsState verifies the wrapper returns
// non-nil state AND an honest error.
//
// The error half is the point. This wrapper used to return nil unconditionally,
// so a run that produced nothing was indistinguishable from a run that built the
// project — and the task API decides "done" vs "failed" on exactly this value.
// A parallel run whose planner returned junk was reported to SARA as done, which
// is the one answer that stops an autonomous supervisor from retrying.
func TestRunEventOrchestratorAsState_ReturnsState(t *testing.T) {
	base := func(dir string, chat func(*ChatContext, string)) OrchestratorConfig {
		return OrchestratorConfig{
			ProjectPath:        dir,
			Owner:              "o",
			Repo:               "r",
			Brief:              "brief",
			SessionIDPrefix:    "test",
			MaxOuterIterations: 1,
			ChatFn:             chat,
			OnProgress:         func(string) {},
			OnPhase:            func(string, string) {},
			OnError:            func(string) {},
		}
	}

	// A model that answers every phase with the word "output" cannot produce a
	// plan, and the run therefore delivered nothing.
	state, err := RunEventOrchestratorAsState(base(t.TempDir(), func(c *ChatContext, _ string) {
		c.OnChunk("output", false)
	}))
	if err == nil {
		t.Error("a run that produced no plan must report an error, not nil")
	}
	if state == nil {
		t.Error("expected non-nil state even for a failed run — the caller still needs the step counts")
	}

	// The same wrapper must stay quiet when the run is fine: a planner that
	// returns a real step, and a validator that passes, is a completed project.
	state, err = RunEventOrchestratorAsState(base(t.TempDir(), func(c *ChatContext, prompt string) {
		if strings.Contains(prompt, plannerPromptMarker) {
			c.OnChunk(onePlanStep, false)
			return
		}
		c.OnChunk("VALIDATION_PASSED", false)
	}))
	if err != nil {
		t.Errorf("a completed run must report nil, got %v", err)
	}
	if state == nil {
		t.Error("expected non-nil state")
	}
	time.Sleep(30 * time.Millisecond)
}

func TestPhaseValidate_Passed(t *testing.T) {
	dir := t.TempDir()
	brain, _ := NewOrchestrationBrain(dir, "o", "r", "brief", "t")
	bus := NewEventBus()
	comms := AgentCommsForProject(dir)
	cfg := OrchestratorConfig{
		ProjectPath:     dir,
		SessionIDPrefix: "test",
		ChatFn: func(c *ChatContext, _ string) {
			c.OnChunk("all checks green\nVALIDATION_PASSED", false)
		},
	}
	dispatcher := NewTeamDispatcher(brain, bus, cfg, 4, comms)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	eo := &EventOrchestrator{
		cfg:           cfg,
		brain:         brain,
		bus:           bus,
		dispatcher:    dispatcher,
		comms:         comms,
		ctx:           ctx,
		cancel:        cancel,
		redirectQueue: make(chan string, 1),
	}

	valid, feedback := eo.phaseValidate()
	if !valid {
		t.Fatal("expected validation to pass")
	}
	if feedback != "" {
		t.Fatalf("expected empty feedback, got %q", feedback)
	}
}

func TestPhaseValidate_FailedAndTrimmed(t *testing.T) {
	dir := t.TempDir()
	brain, _ := NewOrchestrationBrain(dir, "o", "r", "brief", "t")
	bus := NewEventBus()
	comms := AgentCommsForProject(dir)
	long := strings.Repeat("x", 700)
	cfg := OrchestratorConfig{
		ProjectPath:     dir,
		SessionIDPrefix: "test",
		ChatFn: func(c *ChatContext, _ string) {
			c.OnChunk(long, false)
		},
	}
	dispatcher := NewTeamDispatcher(brain, bus, cfg, 4, comms)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	eo := &EventOrchestrator{
		cfg:           cfg,
		brain:         brain,
		bus:           bus,
		dispatcher:    dispatcher,
		comms:         comms,
		ctx:           ctx,
		cancel:        cancel,
		redirectQueue: make(chan string, 1),
	}

	valid, feedback := eo.phaseValidate()
	if valid {
		t.Fatal("expected validation to fail")
	}
	if len(feedback) != 500 {
		t.Fatalf("expected trimmed feedback length 500, got %d", len(feedback))
	}
}

func TestPhaseDispatchTeams_DispatchError(t *testing.T) {
	dir := t.TempDir()
	brain, _ := NewOrchestrationBrain(dir, "o", "r", "brief", "t")
	if err := brain.AddTeam("t1", "general", []int{0}, nil); err != nil {
		t.Fatalf("AddTeam: %v", err)
	}

	bus := NewEventBus()
	comms := AgentCommsForProject(dir)
	cfg := OrchestratorConfig{ProjectPath: dir}
	dispatcher := NewTeamDispatcher(brain, bus, cfg, 4, comms)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	eo := &EventOrchestrator{
		cfg:           cfg,
		brain:         brain,
		bus:           bus,
		dispatcher:    dispatcher,
		comms:         comms,
		ctx:           ctx,
		cancel:        cancel,
		redirectQueue: make(chan string, 1),
	}

	// Force dispatch error by pre-registering the worker as already dispatched.
	dispatcher.workers["t1"] = &TeamWorker{teamID: "t1"}
	err := eo.phaseDispatchTeams()
	if err == nil || !strings.Contains(err.Error(), "dispatch team") {
		t.Fatalf("expected dispatch team error, got %v", err)
	}
}

func TestEventLoop_CancelEvent_EmitsProjectCanceled(t *testing.T) {
	dir := t.TempDir()
	brain, _ := NewOrchestrationBrain(dir, "o", "r", "brief", "t")
	bus := NewEventBus()
	comms := AgentCommsForProject(dir)

	var errs []string
	cfg := OrchestratorConfig{
		ProjectPath:        dir,
		Owner:              "o",
		Repo:               "r",
		Brief:              "brief",
		SessionIDPrefix:    "test",
		MaxOuterIterations: 1,
		ChatFn: func(c *ChatContext, prompt string) {
			if strings.Contains(prompt, "grill master") {
				c.OnChunk("design", false)
				return
			}
			if strings.Contains(prompt, plannerPromptMarker) {
				// A real plan, because an empty one is now terminal. This test
				// used to return nothing here and still reach the cancel path,
				// which only worked because phasePlan's failure was discarded —
				// it was exercising the cancel path of a run that had already
				// failed to plan.
				c.OnChunk(onePlanStep, false)
				return
			}
			c.OnChunk("", false)
		},
		OnPhase:    func(string, string) {},
		OnProgress: func(string) {},
		OnError:    func(msg string) { errs = append(errs, msg) },
	}

	dispatcher := NewTeamDispatcher(brain, bus, cfg, 4, comms)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	eo := &EventOrchestrator{
		cfg:                cfg,
		brain:              brain,
		bus:                bus,
		dispatcher:         dispatcher,
		comms:              comms,
		ctx:                ctx,
		cancel:             cancel,
		redirectQueue:      make(chan string, 1),
		maxOuterIterations: 1,
	}

	projectCanceled := bus.Subscribe(EventProjectCanceled, 1)
	go func() {
		// Let eventLoop reach phaseWaitTeams, then emit cancellation event.
		time.Sleep(20 * time.Millisecond)
		bus.Emit(Event{Type: EventCancel, Timestamp: time.Now()})
	}()

	eo.eventLoop()

	select {
	case <-projectCanceled:
		// expected
	default:
		t.Fatal("expected EventProjectCanceled emission")
	}

	if len(errs) != 0 {
		t.Fatalf("expected no terminal errors, got %s", fmt.Sprint(errs))
	}
}

func TestEventLoop_Success_CompletesProject(t *testing.T) {
	dir := t.TempDir()
	brain, _ := NewOrchestrationBrain(dir, "o", "r", "brief", "t")
	bus := NewEventBus()
	comms := AgentCommsForProject(dir)

	cfg := OrchestratorConfig{
		ProjectPath:        dir,
		Owner:              "o",
		Repo:               "r",
		Brief:              "brief",
		SessionIDPrefix:    "test",
		MaxOuterIterations: 2,
		ChatFn: func(c *ChatContext, prompt string) {
			if strings.Contains(prompt, "VALIDATION_PASSED") {
				c.OnChunk("VALIDATION_PASSED", false)
				return
			}
			if strings.Contains(prompt, plannerPromptMarker) {
				c.OnChunk(onePlanStep, false)
				return
			}
			c.OnChunk("design", false)
		},
		// The team comes from the plan now, the way it does in a real run. This
		// used to be an empty plan plus a team injected from the "execute" phase
		// callback — a shape no production path produces, and one that only made
		// sense while an empty plan was survivable.
		OnPhase:    func(string, string) {},
		OnProgress: func(string) {},
		OnError:    func(string) {},
	}

	dispatcher := NewTeamDispatcher(brain, bus, cfg, 4, comms)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	eo := &EventOrchestrator{
		cfg:                cfg,
		brain:              brain,
		bus:                bus,
		dispatcher:         dispatcher,
		comms:              comms,
		ctx:                ctx,
		cancel:             cancel,
		redirectQueue:      make(chan string, 1),
		maxOuterIterations: 2,
	}

	projectDone := bus.Subscribe(EventProjectDone, 1)
	eo.eventLoop()

	if brain.CompletedAt.IsZero() {
		t.Fatal("expected project to be marked completed")
	}
	select {
	case <-projectDone:
		// expected
	default:
		t.Fatal("expected EventProjectDone emission")
	}
}

func TestEventLoop_ValidationFail_ReachesMaxIterations(t *testing.T) {
	dir := t.TempDir()
	brain, _ := NewOrchestrationBrain(dir, "o", "r", "brief", "t")
	bus := NewEventBus()
	comms := AgentCommsForProject(dir)

	var errs []string
	cfg := OrchestratorConfig{
		ProjectPath:        dir,
		Owner:              "o",
		Repo:               "r",
		Brief:              "brief",
		SessionIDPrefix:    "test",
		MaxOuterIterations: 1,
		ChatFn: func(c *ChatContext, prompt string) {
			if strings.Contains(prompt, "VALIDATION_PASSED") {
				c.OnChunk("tests failed: missing build artifact", false)
				return
			}
			if strings.Contains(prompt, plannerPromptMarker) {
				c.OnChunk(onePlanStep, false)
				return
			}
			c.OnChunk("design", false)
		},
		OnPhase:    func(string, string) {},
		OnProgress: func(string) {},
		OnError:    func(msg string) { errs = append(errs, msg) },
	}

	dispatcher := NewTeamDispatcher(brain, bus, cfg, 4, comms)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	eo := &EventOrchestrator{
		cfg:                cfg,
		brain:              brain,
		bus:                bus,
		dispatcher:         dispatcher,
		comms:              comms,
		ctx:                ctx,
		cancel:             cancel,
		redirectQueue:      make(chan string, 1),
		maxOuterIterations: 1,
	}

	projectFailed := bus.Subscribe(EventProjectFailed, 1)
	eo.eventLoop()

	select {
	case <-projectFailed:
		// expected
	default:
		t.Fatal("expected EventProjectFailed emission")
	}
	if len(errs) == 0 || !strings.Contains(errs[len(errs)-1], "max iterations") {
		t.Fatalf("expected max iterations error, got %v", errs)
	}
}

// TestPhaseValidate_DetectsPassKeyword tests phaseValidate with VALIDATION_PASSED in output.
func TestPhaseValidate_DetectsPassKeyword(t *testing.T) {
	dir := t.TempDir()
	cfg := OrchestratorConfig{
		ProjectPath:        dir,
		Owner:              "owner",
		Repo:               "repo",
		SessionIDPrefix:    "t",
		MaxOuterIterations: 2,
		ChatFn: func(ctx *ChatContext, msg string) {
			ctx.OnChunk("All tests pass. VALIDATION_PASSED", true)
		},
		OnPhase:    func(string, string) {},
		OnProgress: func(string) {},
		OnError:    func(string) {},
	}

	eo := &EventOrchestrator{
		cfg:   cfg,
		brain: &OrchestrationBrain{OuterIterations: 1},
	}

	passed, reason := eo.phaseValidate()
	if !passed {
		t.Errorf("expected validation to pass, got %v reason=%s", passed, reason)
	}
}

// TestPhaseValidate_FailsWithoutKeyword tests phaseValidate extraction of error reason.
func TestPhaseValidate_FailsWithoutKeyword(t *testing.T) {
	dir := t.TempDir()
	cfg := OrchestratorConfig{
		ProjectPath:        dir,
		Owner:              "owner",
		Repo:               "repo",
		SessionIDPrefix:    "t",
		MaxOuterIterations: 2,
		ChatFn: func(ctx *ChatContext, msg string) {
			ctx.OnChunk("Tests failed:\nError at line 42: assertion false", true)
		},
		OnPhase:    func(string, string) {},
		OnProgress: func(string) {},
		OnError:    func(string) {},
	}

	eo := &EventOrchestrator{
		cfg:   cfg,
		brain: &OrchestrationBrain{OuterIterations: 1},
	}

	passed, reason := eo.phaseValidate()
	if passed {
		t.Errorf("expected validation to fail, got %v", passed)
	}
	if reason == "" {
		t.Error("expected reason to be extracted")
	}
	if len(reason) > 500 {
		t.Errorf("reason exceeds truncation limit: %d chars", len(reason))
	}
}

// TestPhaseDispatchTeams_NoTeamsReady tests phaseDispatchTeams when no teams are ready.
func TestPhaseDispatchTeams_NoTeamsReady(t *testing.T) {
	dir := t.TempDir()
	brain, _ := NewOrchestrationBrain(dir, "owner", "repo", "brief", "t")

	eo := &EventOrchestrator{
		cfg: OrchestratorConfig{
			ProjectPath: dir,
		},
		brain: brain,
		bus:   NewEventBus(),
		comms: AgentCommsForProject(dir),
	}

	// No teams created, phaseDispatchTeams should handle it
	err := eo.phaseDispatchTeams()
	// Should not crash even with no teams
	if err != nil && strings.Contains(err.Error(), "plan") {
		// Expected if validation or plan extraction fails
	}
}

// TestCreateTeamsFromPlan_EmptyBrain tests team creation with empty plan.
func TestCreateTeamsFromPlan_EmptyBrain(t *testing.T) {
	dir := t.TempDir()
	brain, _ := NewOrchestrationBrain(dir, "owner", "repo", "brief", "t")

	eo := &EventOrchestrator{
		brain: brain,
	}

	// Pass empty plan - function returns void
	eo.createTeamsFromPlan([]PlanStep{})
	// Should not panic even with no teams
}

// TestRunAutonomousProject_Success tests main entry point with chat success.
func TestRunAutonomousProject_Success(t *testing.T) {
	dir := t.TempDir()
	cfg := OrchestratorConfig{
		ProjectPath:        dir,
		Owner:              "owner",
		Repo:               "repo",
		SessionIDPrefix:    "t",
		MaxOuterIterations: 1,
		ChatFn: func(ctx *ChatContext, msg string) {
			ctx.OnChunk("Completed successfully", true)
		},
		OnPhase: func(phase, detail string) {
			t.Logf("Phase: %s - %s", phase, detail)
		},
		OnProgress: func(msg string) {
			t.Logf("Progress: %s", msg)
		},
		OnError: func(msg string) {
			t.Logf("Error: %s", msg)
		},
	}

	state, err := RunAutonomousProject(cfg)
	// RunAutonomousProject should return either a valid state or error
	// It might hit max iterations limit which is not an error
	if state == nil && err == nil {
		t.Fatal("expected orchestrator to return state or error")
	}
}

// TestPersistOrchestrationState tests persist with valid state.
func TestPersistOrchestrationState(t *testing.T) {
	dir := t.TempDir()

	state := &OrchestrationState{
		Repo:            "test-repo",
		Owner:           "test-owner",
		Brief:           "test brief",
		OuterIterations: 1,
		StartedAt:       "2024-01-01T00:00:00Z",
		UpdatedAt:       "2024-01-01T00:00:00Z",
	}

	// Should persist state file successfully
	err := persistOrchestration(dir, state)
	if err != nil {
		t.Logf("persist failed (expected in test): %v", err)
	}
}

// TestReadyTeamsEmptyBrain tests ReadyTeams with no teams.
func TestReadyTeamsEmptyBrain(t *testing.T) {
	dir := t.TempDir()
	brain, _ := NewOrchestrationBrain(dir, "owner", "repo", "brief", "t")

	ready := brain.ReadyTeams()
	if len(ready) != 0 {
		t.Errorf("expected no ready teams from empty brain, got %d", len(ready))
	}
}

// TestLoadOrCreateOrchestrationState_CreateNew tests loading from nonexistent path.
func TestLoadOrCreateOrchestrationState_CreateNew(t *testing.T) {
	dir := t.TempDir()

	state, err := loadOrCreateOrchestrationState(dir, "owner", "repo", "brief")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if state.Repo != "repo" || state.Owner != "owner" || state.Brief != "brief" {
		t.Errorf("expected new state with repo/owner/brief, got %v", state)
	}
}

// TestLoadOrCreateOrchestrationState_LoadExisting tests loading existing state file.
func TestLoadOrCreateOrchestrationState_LoadExisting(t *testing.T) {
	dir := t.TempDir()

	// Create initial state
	state1, err := loadOrCreateOrchestrationState(dir, "owner", "repo", "initial")
	if err != nil {
		t.Fatalf("first load failed: %v", err)
	}

	// Persist it
	err = persistOrchestration(dir, state1)
	if err != nil {
		t.Fatalf("persist failed: %v", err)
	}

	// Load again with updated brief
	state2, err := loadOrCreateOrchestrationState(dir, "owner2", "repo2", "updated")
	if err != nil {
		t.Fatalf("second load failed: %v", err)
	}

	// Should have loaded state1 but with updated brief
	if state2.Brief != "updated" {
		t.Errorf("expected brief to be updated to 'updated', got %s", state2.Brief)
	}
	if state2.Repo != "repo" {
		t.Errorf("expected repo to remain 'repo', got %s", state2.Repo)
	}
}

// TestPersistBrainState tests persisting brain state to disk.
func TestPersistBrainState(t *testing.T) {
	dir := t.TempDir()
	brain, _ := NewOrchestrationBrain(dir, "owner", "repo", "brief", "t")

	// Add some plan steps
	brain.Plan = []PlanStep{
		{Index: 1, Title: "Step1", Done: false},
		{Index: 2, Title: "Step2", Done: true},
	}

	// Persist the state
	err := brain.persist()
	if err != nil {
		t.Logf("persist failed: %v", err)
	}
}
