package ai

import (
	"context"
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

// TestExtractPlanFromContext_AlwaysEmpty verifies it always returns empty slice.
func TestExtractPlanFromContext_AlwaysEmpty(t *testing.T) {
	plan := extractPlanFromContext("1. Step one\n   do things\n   Acceptance: `go build`")
	if len(plan) != 0 {
		t.Errorf("expected empty plan, got %d steps", len(plan))
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
		Type:   EventTeamFailed,
		TeamID: "t1",
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
	// Context times out after redirect is processed
	err := eo.phaseWaitTeams(teamDone, teamFailed, userRedirect, cancelEv)
	if err == nil {
		t.Error("expected error from context timeout after redirect")
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

// TestRunEventOrchestratorAsState_ReturnsState verifies the wrapper returns non-nil state.
func TestRunEventOrchestratorAsState_ReturnsState(t *testing.T) {
	dir := t.TempDir()
	cfg := OrchestratorConfig{
		ProjectPath:        dir,
		Owner:              "o",
		Repo:               "r",
		Brief:              "brief",
		SessionIDPrefix:    "test",
		MaxOuterIterations: 1,
		ChatFn:             func(c *ChatContext, _ string) { c.OnChunk("output", false) },
		OnProgress:         func(string) {},
		OnPhase:            func(string, string) {},
		OnError:            func(string) {},
	}
	state, err := RunEventOrchestratorAsState(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if state == nil {
		t.Error("expected non-nil state")
	}
	time.Sleep(30 * time.Millisecond)
}
