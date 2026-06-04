package ai

import (
	stdctx "context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/engine/server/db"
	"github.com/engine/server/mesh"
)

type fakeMeshExecClient struct{}

func (fakeMeshExecClient) Exec(_ stdctx.Context, peer *mesh.Peer, req mesh.ExecRequest) (*mesh.ExecResponse, error) {
	return &mesh.ExecResponse{ExitCode: 0, DurationMs: 1, Stdout: peer.Name + ":" + req.Command}, nil
}

func TestEventBusEmit_DropsWhenSubscriberBufferFull(t *testing.T) {
	b := NewEventBus()
	b.mu.Lock()
	b.listeners[EventCancel] = append(b.listeners[EventCancel], make(chan Event))
	b.mu.Unlock()
	b.Emit(Event{Type: EventCancel})
}

func TestExecuteToolShell_AutonomousOutsideWorkspaceError(t *testing.T) {
	projectDir := t.TempDir()
	policy := ResolveAutonomousPolicy(projectDir)
	ctx := &ChatContext{ProjectPath: projectDir, AutonomousPolicy: &policy}
	result, isErr := ExecuteToolForTest("shell", map[string]any{"command": "pwd", "cwd": "../outside"}, ctx)
	if !isErr {
		t.Fatalf("expected error, got success: %s", result)
	}
}

func TestExecuteToolShell_TimeoutBranch(t *testing.T) {
	oldInteractive := interactiveShellTimeout
	defer func() { interactiveShellTimeout = oldInteractive }()
	interactiveShellTimeout = 1 * time.Millisecond

	ctx := &ChatContext{ProjectPath: t.TempDir()}
	result, isErr := ExecuteToolForTest("shell", map[string]any{"command": "sleep 1"}, ctx)
	if !isErr {
		t.Fatalf("expected timeout error, got isErr=%v result=%q", isErr, result)
	}
}

func TestExecuteTool_MeshExecSwitchBranch(t *testing.T) {
	oldLoad := meshLoadConfigFn
	oldFactory := meshClientFactory
	defer func() {
		meshLoadConfigFn = oldLoad
		meshClientFactory = oldFactory
	}()

	meshLoadConfigFn = func(string) (*mesh.Config, error) {
		return &mesh.Config{SelfName: "self", Peers: []mesh.Peer{{Name: "peer-1"}}}, nil
	}
	meshClientFactory = func(string) meshClient { return fakeMeshExecClient{} }

	result, isErr := ExecuteToolForTest("mesh_exec", map[string]any{"command": "echo"}, &ChatContext{})
	if isErr {
		t.Fatalf("unexpected mesh_exec error: %s", result)
	}
	if !strings.Contains(result, "peer-1") {
		t.Fatalf("expected peer in result, got %s", result)
	}
}

func TestCoverage_DefaultEnvAndMeshFactoryBodies(t *testing.T) {
	_ = getEnv("PATH")
	if meshClientFactory("self") == nil {
		t.Fatal("expected default mesh client")
	}
}

func TestChat_LocalFirstRoutingBranch(t *testing.T) {
	projectDir := setupHistoryTestProject(t)
	if err := db.CreateSession("session-local-first", projectDir, "main"); err != nil {
		t.Fatalf("create session: %v", err)
	}

	var requestedModel string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/ps":
			w.Header().Set("Content-Type", "application/json")
			if _, err := w.Write([]byte(`{"models":[{"name":"local-first-model"}]}`)); err != nil {
				t.Fatalf("write /api/ps response: %v", err)
			}
		case "/v1/chat/completions":
			var req struct {
				Model string `json:"model"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("decode request: %v", err)
			}
			requestedModel = req.Model
			sendSSEDone(w)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	t.Setenv("OLLAMA_BASE_URL", server.URL)
	t.Setenv("ENGINE_LOCAL_FIRST", "1")
	t.Setenv("ENGINE_MODEL_PROVIDER", "")
	t.Setenv("ENGINE_MODEL", "")
	t.Setenv("ENGINE_OLLAMA_MODEL", "local-first-model")

	ctx := &ChatContext{
		ProjectPath:  projectDir,
		SessionID:    "session-local-first",
		Role:         RolePlanner,
		OnChunk:      func(string, bool) {},
		OnError:      func(string) {},
		OnToolCall:   func(string, any) {},
		OnToolResult: func(string, any, bool) {},
	}
	Chat(ctx, "plan this")
	if requestedModel != "local-first-model" {
		t.Fatalf("expected local-first-model, got %q", requestedModel)
	}
}

func TestChat_ForcedEngineOllamaModelBranch(t *testing.T) {
	projectDir := setupHistoryTestProject(t)
	if err := db.CreateSession("session-forced-ollama", projectDir, "main"); err != nil {
		t.Fatalf("create session: %v", err)
	}

	var requestedModel string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/chat/completions":
			var req struct {
				Model string `json:"model"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("decode request: %v", err)
			}
			requestedModel = req.Model
			sendSSEDone(w)
		default:
			w.Header().Set("Content-Type", "application/json")
			if _, err := w.Write([]byte(`{"models":[]}`)); err != nil {
				t.Fatalf("write models response: %v", err)
			}
		}
	}))
	defer server.Close()

	t.Setenv("OLLAMA_BASE_URL", server.URL)
	t.Setenv("ENGINE_MODEL_PROVIDER", "ollama")
	t.Setenv("ENGINE_MODEL", "forced-ollama-model")
	t.Setenv("ENGINE_OLLAMA_MODEL", "forced-ollama-model")

	ctx := &ChatContext{
		ProjectPath:  projectDir,
		SessionID:    "session-forced-ollama",
		Role:         RoleInteractive,
		OnChunk:      func(string, bool) {},
		OnError:      func(string) {},
		OnToolCall:   func(string, any) {},
		OnToolResult: func(string, any, bool) {},
	}
	Chat(ctx, "hello")
	if requestedModel != "forced-ollama-model" {
		t.Fatalf("expected forced model, got %q", requestedModel)
	}
}

func TestRunPlannerPrePass_ForcedEngineOllamaModelBranch(t *testing.T) {
	projectDir := setupHistoryTestProject(t)

	var requestedModel string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/chat/completions":
			var req struct {
				Model string `json:"model"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("decode request: %v", err)
			}
			requestedModel = req.Model
			sendSSEDone(w)
		default:
			w.Header().Set("Content-Type", "application/json")
			if _, err := w.Write([]byte(`{"models":[]}`)); err != nil {
				t.Fatalf("write models response: %v", err)
			}
		}
	}))
	defer server.Close()

	t.Setenv("OLLAMA_BASE_URL", server.URL)
	t.Setenv("ENGINE_PLANNER_PROVIDER", "ollama")
	t.Setenv("ENGINE_PLANNER_MODEL", "forced-prepass-model")
	t.Setenv("ENGINE_OLLAMA_MODEL", "forced-prepass-model")

	ctx := &ChatContext{
		ProjectPath:  projectDir,
		SessionID:    "session-prepass-forced",
		Role:         RoleInteractive,
		Usage:        &SessionUsage{},
		Quarantine:   NewToolQuarantine(),
		Cancel:       make(chan struct{}),
		OnError:      func(string) {},
		OnChunk:      func(string, bool) {},
		OnToolCall:   func(string, any) {},
		OnToolResult: func(string, any, bool) {},
	}
	_ = runPlannerPrePass(ctx, "ollama", "", "ship this", "main")
	if requestedModel != "forced-prepass-model" {
		t.Fatalf("expected forced prepass model, got %q", requestedModel)
	}
}

func TestRunAnthropicLoop_CancelDuringRetrySelect(t *testing.T) {
	cancel := make(chan struct{})
	var attempts int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&attempts, 1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	oldClient := http.DefaultClient
	defer func() { http.DefaultClient = oldClient }()
	http.DefaultClient = &http.Client{Transport: redirectTransport{target: server.URL}}

	go func() {
		for atomic.LoadInt32(&attempts) == 0 {
			time.Sleep(1 * time.Millisecond)
		}
		close(cancel)
	}()

	ctx := &ChatContext{
		Cancel:       cancel,
		OnChunk:      func(string, bool) {},
		OnToolCall:   func(string, any) {},
		OnToolResult: func(string, any, bool) {},
		OnError:      func(string) {},
		ActiveTools:  bootstrapTools(),
		ProjectPath:  setupHistoryTestProject(t),
	}
	var calls []ToolCall
	var text strings.Builder
	runAnthropicLoop(ctx, "claude-3-5-sonnet-20241022", "key", "system", []anthropicMessage{{Role: "user", Content: "hi"}}, &calls, &text)
}

func TestReadyTeams_SkipsNonQueuedStatus(t *testing.T) {
	brain, err := NewOrchestrationBrain(t.TempDir(), "o", "r", "brief", "s")
	if err != nil {
		t.Fatalf("brain: %v", err)
	}
	if err := brain.AddTeam("queued", "general", []int{0}, nil); err != nil {
		t.Fatalf("add queued team: %v", err)
	}
	if err := brain.AddTeam("running", "general", []int{1}, nil); err != nil {
		t.Fatalf("add running team: %v", err)
	}
	if err := brain.UpdateTeamStatus("running", "running"); err != nil {
		t.Fatalf("set running status: %v", err)
	}
	ready := brain.ReadyTeams()
	if len(ready) != 1 || ready[0].ID != "queued" {
		t.Fatalf("expected only queued team ready, got %+v", ready)
	}
}

func TestTeamDispatcherStop_TimeoutBranch(t *testing.T) {
	oldTimeout := teamDispatcherStopTimeout
	defer func() { teamDispatcherStopTimeout = oldTimeout }()
	teamDispatcherStopTimeout = 1 * time.Millisecond

	d := NewTeamDispatcher(nil, NewEventBus(), OrchestratorConfig{}, 1, nil)
	d.wg.Add(1)
	d.Stop()
	d.wg.Done()
}

func TestOrchestratorPhasePlan_CallsOnPlanUpdateAndRoleSplit(t *testing.T) {
	dir := setupPhasesDB(t)
	brain, err := NewOrchestrationBrain(dir, "o", "r", "brief", "s")
	if err != nil {
		t.Fatalf("brain: %v", err)
	}
	updates := 0
	cfg := OrchestratorConfig{
		ProjectPath: dir,
		OnPhase:     func(string, string) {},
		OnProgress:  func(string) {},
		OnError:     func(string) {},
		OnPlanUpdate: func(*OrchestrationState) {
			updates++
		},
		SessionIDPrefix: "t",
		ChatFn: func(c *ChatContext, _ string) {
			c.OnChunk("1. database migration\n   add table\n   Acceptance: `go test ./...`\n2. ui component\n   render card\n   Acceptance: `go test ./...`", false)
		},
	}
	eo := &EventOrchestrator{cfg: cfg, brain: brain, bus: NewEventBus(), dispatcher: NewTeamDispatcher(brain, NewEventBus(), cfg, 4, NewAgentCommsHub())}
	if err := eo.phasePlan(); err != nil {
		t.Fatalf("phasePlan: %v", err)
	}
	if updates == 0 {
		t.Fatal("expected OnPlanUpdate callback")
	}
	if len(brain.Teams) < 2 {
		t.Fatalf("expected role-based team split, got %d teams", len(brain.Teams))
	}
}

func TestPhaseWaitTeams_DispatchErrorPath(t *testing.T) {
	brain, err := NewOrchestrationBrain(t.TempDir(), "o", "r", "brief", "s")
	if err != nil {
		t.Fatalf("brain: %v", err)
	}
	if err := brain.AddTeam("team-a", "general", []int{0}, nil); err != nil {
		t.Fatalf("add team: %v", err)
	}
	bus := NewEventBus()
	dispatcher := NewTeamDispatcher(brain, bus, OrchestratorConfig{ProjectPath: t.TempDir()}, 4, NewAgentCommsHub())
	dispatcher.workers["team-a"] = &TeamWorker{}
	eo := &EventOrchestrator{
		cfg:        OrchestratorConfig{ProjectPath: t.TempDir(), OnProgress: func(string) {}, OnError: func(string) {}, OnPhase: func(string, string) {}},
		brain:      brain,
		bus:        bus,
		dispatcher: dispatcher,
		ctx:        stdctx.Background(),
	}
	teamDone := make(chan Event, 1)
	teamFailed := make(chan Event, 1)
	userRedirect := make(chan Event, 1)
	cancelEv := make(chan Event, 1)
	teamDone <- Event{Type: EventTeamDone, TeamID: "team-a"}
	err = eo.phaseWaitTeams(teamDone, teamFailed, userRedirect, cancelEv)
	if err == nil {
		t.Fatal("expected dispatch error")
	}
}

func TestOrchestratorIntake_CallbackNoOpBranches(t *testing.T) {
	dir := setupIntakeDB(t)
	cfg := OrchestratorConfig{
		ProjectPath:     dir,
		SessionIDPrefix: "t",
		ChatFn: func(c *ChatContext, _ string) {
			c.OnChunk("", false)
			c.OnError("x")
			c.OnToolCall("noop", nil)
			c.OnToolResult("noop", nil, false)
			c.OnChunk("design output", false)
		},
	}
	state := &OrchestrationState{Brief: "brief"}
	got, err := orchestratorIntakePhase(cfg, state, make(chan struct{}))
	if err != nil {
		t.Fatalf("intake: %v", err)
	}
	if !strings.Contains(got, "design output") {
		t.Fatalf("unexpected design output: %q", got)
	}
}

func TestOrchestratorPRDPhase_CreateSessionAndPersistErrorBranches(t *testing.T) {
	// create session error
	dirNoDB := t.TempDir()
	if err := WriteDoc(dirNoDB, DocDesign, "design"); err != nil {
		t.Fatalf("write design doc: %v", err)
	}
	err := orchestratorPRDPhase(OrchestratorConfig{ProjectPath: dirNoDB, SessionIDPrefix: "t"}, make(chan struct{}))
	if err == nil || !strings.Contains(err.Error(), "create PRD session") {
		t.Fatalf("expected create PRD session error, got %v", err)
	}

	// persist vocabulary error
	dir := setupIntakeDB(t)
	if err := WriteDoc(dir, DocDesign, "design"); err != nil {
		t.Fatalf("write design: %v", err)
	}
	engineDir := filepath.Join(dir, ".engine")
	if err := os.Chmod(engineDir, 0o500); err != nil {
		t.Fatalf("chmod engine dir: %v", err)
	}
	cfg := OrchestratorConfig{ProjectPath: dir, SessionIDPrefix: "t", ChatFn: func(c *ChatContext, _ string) { c.OnChunk("vocab---SPLIT---prd", false) }}
	err = orchestratorPRDPhase(cfg, make(chan struct{}))
	if err == nil || !strings.Contains(err.Error(), "persist vocabulary.md") {
		t.Fatalf("expected vocab persist error, got %v", err)
	}
	_ = os.Chmod(engineDir, 0o700)

	// persist prd error
	dir2 := setupIntakeDB(t)
	if err := WriteDoc(dir2, DocDesign, "design"); err != nil {
		t.Fatalf("write design: %v", err)
	}
	prdPath := filepath.Join(dir2, ".engine", "prd.md")
	if err := os.WriteFile(prdPath, []byte("old"), 0o400); err != nil {
		t.Fatalf("seed prd: %v", err)
	}
	cfg2 := OrchestratorConfig{ProjectPath: dir2, SessionIDPrefix: "t", ChatFn: func(c *ChatContext, _ string) { c.OnChunk("vocab---SPLIT---prd", false) }}
	err = orchestratorPRDPhase(cfg2, make(chan struct{}))
	if err == nil || !strings.Contains(err.Error(), "persist prd.md") {
		t.Fatalf("expected prd persist error, got %v", err)
	}
	_ = os.Chmod(prdPath, 0o600)
}

func TestReadContextDoc_LegacyFileReturnPath(t *testing.T) {
	dir := t.TempDir()
	engineDir := filepath.Join(dir, ".engine")
	if err := os.MkdirAll(engineDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(engineDir, contextFile), []byte("legacy"), 0o644); err != nil {
		t.Fatalf("write legacy context: %v", err)
	}
	if got := readContextDoc(dir); got != "legacy" {
		t.Fatalf("expected legacy content, got %q", got)
	}
}

func TestReadContextDoc_WhitespaceFallsThroughLegacyRead(t *testing.T) {
	dir := t.TempDir()
	if err := writeContextDoc(dir, "   \n\t"); err != nil {
		t.Fatalf("write context doc: %v", err)
	}
	if got := readContextDoc(dir); got == "" {
		t.Fatal("expected legacy fallback content")
	}
}

func TestOrchestratorArchitectReview_CreateSessionAndCallbackBranches(t *testing.T) {
	dirNoDB := t.TempDir()
	state := &OrchestrationState{Owner: "o", Repo: "r"}
	step := &PlanStep{Index: 1, Title: "title"}
	verdict, msg := orchestratorArchitectReview(OrchestratorConfig{ProjectPath: dirNoDB, SessionIDPrefix: "t"}, state, step, make(chan struct{}))
	if verdict != ReviewInconclusive || !strings.Contains(msg, "create architect session") {
		t.Fatalf("expected create architect session error, got verdict=%v msg=%q", verdict, msg)
	}

	dir := setupIntakeDB(t)
	cfg := OrchestratorConfig{
		ProjectPath:     dir,
		SessionIDPrefix: "t",
		ChatFn: func(c *ChatContext, _ string) {
			c.OnChunk("", false)
			c.OnError("x")
			c.OnToolCall("noop", nil)
			c.OnToolResult("noop", nil, false)
			c.OnChunk("APPROVE", false)
		},
	}
	_, _ = orchestratorArchitectReview(cfg, state, step, make(chan struct{}))
}

func TestOrchestratorPhaseCallbackNoOpBranches(t *testing.T) {
	dir := setupPhasesDB(t)
	state := &OrchestrationState{Owner: "o", Repo: "r", Plan: []PlanStep{{Index: 1, Title: "t", Acceptance: "`echo ok`"}}}
	step := &state.Plan[0]

	cfg := OrchestratorConfig{
		ProjectPath:     dir,
		SessionIDPrefix: "t",
		ChatFn: func(c *ChatContext, _ string) {
			c.OnChunk("", false)
			c.OnError("x")
			c.OnToolCall("noop", nil)
			c.OnToolResult("noop", nil, false)
			c.OnChunk("result\nAPPROVE", false)
		},
	}

	if err := orchestratorBuildStep(cfg, state, step, "", make(chan struct{})); err != nil {
		t.Fatalf("build step: %v", err)
	}
	_, _ = orchestratorReviewStep(cfg, state, step, make(chan struct{}))
	_, _ = orchestratorValidatePhase(cfg, state, make(chan struct{}))
}

func TestParsePlanAndReviewerExtraBranches(t *testing.T) {
	steps := parsePlanFromText("1. Step\n   body line\n   extra detail\n\n   Acceptance: `go test ./...`")
	if len(steps) != 1 {
		t.Fatalf("expected one parsed step, got %d", len(steps))
	}

	verdict, _ := parseReviewerVerdict("finding\n\nAPPROVE")
	if verdict != ReviewApprove {
		t.Fatalf("expected approve verdict, got %v", verdict)
	}

	verdict, _ = parseReviewerVerdict("not a verdict")
	if verdict != ReviewInconclusive {
		t.Fatalf("expected inconclusive verdict, got %v", verdict)
	}

	verdict, _ = parseReviewerVerdict("   \n\t")
	if verdict != ReviewInconclusive {
		t.Fatalf("expected inconclusive verdict for whitespace-only output, got %v", verdict)
	}
}

func TestRunAutonomousProject_PersistPlanErrorBranch(t *testing.T) {
	dir := setupPhasesDB(t)
	cfg := OrchestratorConfig{
		ProjectPath:        dir,
		Owner:              "o",
		Repo:               "r",
		Brief:              "brief",
		SessionIDPrefix:    "t",
		MaxOuterIterations: 2,
		OnPhase:            func(string, string) {},
		OnProgress:         func(string) {},
		OnError:            func(string) {},
		ChatFn: func(c *ChatContext, _ string) {
			switch c.Role {
			case RoleGriller:
				c.OnChunk("design", false)
			case RolePRDWriter:
				c.OnChunk("vocab---SPLIT---prd", false)
			case RolePlanner:
				enginePath := filepath.Join(dir, ".engine")
				if err := os.RemoveAll(enginePath); err != nil {
					t.Fatalf("remove .engine path: %v", err)
				}
				if err := os.WriteFile(enginePath, []byte("blocked"), 0o644); err != nil {
					t.Fatalf("write blocking file: %v", err)
				}
				c.OnChunk("1. Step\n   build\n   Acceptance: `echo ok`", false)
			}
		},
	}
	_, err := RunAutonomousProject(cfg)
	if err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("expected persist-plan style error, got %v", err)
	}
}

func TestRunAutonomousProject_PausePollAndModuleIndexErrorBranches(t *testing.T) {
	dir := setupPhasesDB(t)
	oldPause := orchestratorPausePollInterval
	defer func() { orchestratorPausePollInterval = oldPause }()
	orchestratorPausePollInterval = 1 * time.Millisecond

	handle := GetOrchestratorHandle(dir)
	handle.Pause()
	defer handle.Resume()

	var sawModuleRefreshErr bool
	go func() {
		time.Sleep(5 * time.Millisecond)
		handle.Resume()
	}()

	cfg := OrchestratorConfig{
		ProjectPath:        dir,
		Owner:              "o",
		Repo:               "r",
		Brief:              "brief",
		SessionIDPrefix:    "t",
		MaxOuterIterations: 3,
		OnPhase:            func(string, string) {},
		OnProgress:         func(string) {},
		OnError: func(msg string) {
			if strings.Contains(msg, "module index refresh") {
				sawModuleRefreshErr = true
			}
		},
		ChatFn: func(c *ChatContext, msg string) {
			switch c.Role {
			case RoleGriller:
				c.OnChunk("design", false)
			case RolePRDWriter:
				c.OnChunk("vocab---SPLIT---prd", false)
			case RolePlanner:
				c.OnChunk("1. Step\n   implement\n   Acceptance: `echo ok`", false)
			case RoleAutonomousBuilder:
				c.OnChunk("builder output", false)
			case RoleReviewer:
				if strings.Contains(msg, "Final behavioral validation") {
					c.OnChunk("all good\nAPPROVE", false)
				} else {
					c.OnChunk("looks good\nAPPROVE", false)
				}
			case RoleModuleIndexer:
				// Empty output triggers module-index refresh error branch.
			}
		},
	}

	st, err := RunAutonomousProject(cfg)
	if err != nil {
		t.Fatalf("expected successful run, got %v", err)
	}
	if st == nil || st.CompletedAt == "" {
		t.Fatalf("expected completed state, got %+v", st)
	}
	if !sawModuleRefreshErr {
		t.Fatal("expected module index refresh error to be emitted")
	}
}

func TestEventLoop_DispatchFailureBranch(t *testing.T) {
	dir := t.TempDir()
	brain, err := NewOrchestrationBrain(dir, "o", "r", "brief", "t")
	if err != nil {
		t.Fatalf("brain: %v", err)
	}
	bus := NewEventBus()
	comms := NewAgentCommsHub()
	var gotErr string
	cfg := OrchestratorConfig{
		ProjectPath:     dir,
		Owner:           "o",
		Repo:            "r",
		Brief:           "brief",
		SessionIDPrefix: "t",
		OnPhase:         func(string, string) {},
		OnProgress:      func(string) {},
		OnError:         func(msg string) { gotErr = msg },
		ChatFn: func(c *ChatContext, _ string) {
			switch c.Role {
			case RoleGriller:
				c.OnChunk("design", false)
			case RolePRDWriter:
				c.OnChunk("prd", false)
			case RolePlanner:
				c.OnChunk("1. implement\n   do work\n   Acceptance: `echo ok`", false)
			}
		},
	}
	dispatcher := NewTeamDispatcher(brain, bus, cfg, 4, comms)
	dispatcher.workers["team-general-0"] = &TeamWorker{}
	ctx, cancel := stdctx.WithCancel(stdctx.Background())
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
	eo.eventLoop()
	if !strings.Contains(gotErr, "team dispatch failed") {
		t.Fatalf("expected dispatch failure error, got %q", gotErr)
	}
}

func TestCreateTeamsFromPlan_RoleTransitionBranch(t *testing.T) {
	dir := t.TempDir()
	brain, err := NewOrchestrationBrain(dir, "o", "r", "brief", "t")
	if err != nil {
		t.Fatalf("brain: %v", err)
	}
	eo := &EventOrchestrator{brain: brain}
	eo.createTeamsFromPlan([]PlanStep{
		{Index: 1, Title: "database migration"},
		{Index: 2, Title: "ui form"},
	})
	if len(brain.Teams) < 2 {
		t.Fatalf("expected at least two teams after role transition, got %d", len(brain.Teams))
	}
}

func TestCreateTeamsFromPlan_SameRoleAppendBranch(t *testing.T) {
	dir := t.TempDir()
	brain, err := NewOrchestrationBrain(dir, "o", "r", "brief", "t")
	if err != nil {
		t.Fatalf("brain: %v", err)
	}
	eo := &EventOrchestrator{brain: brain}
	eo.createTeamsFromPlan([]PlanStep{
		{Index: 1, Title: "database migration one"},
		{Index: 2, Title: "database migration two"},
	})
	if len(brain.Teams) != 1 {
		t.Fatalf("expected one team for same-role contiguous steps, got %d", len(brain.Teams))
	}
}

func TestRunAutonomousProject_PausePollBranch(t *testing.T) {
	dir := setupPhasesDB(t)
	oldPause := orchestratorPausePollInterval
	defer func() { orchestratorPausePollInterval = oldPause }()
	orchestratorPausePollInterval = 1 * time.Millisecond

	handle := GetOrchestratorHandle(dir)
	handle.Pause()
	defer handle.Resume()
	go func() {
		time.Sleep(700 * time.Millisecond)
		handle.Resume()
	}()

	cfg := OrchestratorConfig{
		ProjectPath:        dir,
		Owner:              "o",
		Repo:               "r",
		Brief:              "brief",
		SessionIDPrefix:    "t",
		MaxOuterIterations: 3,
		OnPhase:            func(string, string) {},
		OnProgress:         func(string) {},
		OnError:            func(string) {},
		ChatFn: func(c *ChatContext, msg string) {
			switch c.Role {
			case RoleGriller:
				c.OnChunk("design", false)
			case RolePRDWriter:
				c.OnChunk("vocab---SPLIT---prd", false)
			case RolePlanner:
				c.OnChunk("1. Step\n   implement\n   Acceptance: `echo ok`", false)
			case RoleAutonomousBuilder:
				c.OnChunk("builder output", false)
			case RoleReviewer:
				if strings.Contains(msg, "Final behavioral validation") {
					c.OnChunk("all good\nAPPROVE", false)
				} else {
					c.OnChunk("looks good\nAPPROVE", false)
				}
			case RoleModuleIndexer:
				c.OnChunk("modules", false)
			}
		},
	}
	st, err := RunAutonomousProject(cfg)
	if err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if st == nil || st.CompletedAt == "" {
		t.Fatalf("expected completed state, got %+v", st)
	}
}

func TestRunAutonomousProject_ValidationFailureReopenBranch(t *testing.T) {
	dir := setupPhasesDB(t)
	cfg := OrchestratorConfig{
		ProjectPath:        dir,
		Owner:              "o",
		Repo:               "r",
		Brief:              "brief",
		SessionIDPrefix:    "t",
		MaxOuterIterations: 2,
		OnPhase:            func(string, string) {},
		OnProgress:         func(string) {},
		OnError:            func(string) {},
		ChatFn: func(c *ChatContext, msg string) {
			switch c.Role {
			case RoleGriller:
				c.OnChunk("design", false)
			case RolePRDWriter:
				c.OnChunk("vocab---SPLIT---prd", false)
			case RolePlanner:
				c.OnChunk("1. Step\n   implement\n   Acceptance: `echo ok`", false)
			case RoleAutonomousBuilder:
				c.OnChunk("builder output", false)
			case RoleReviewer:
				if strings.Contains(msg, "Final behavioral validation") {
					c.OnChunk("validation failed\nREJECT: not yet", false)
				} else {
					c.OnChunk("looks good\nAPPROVE", false)
				}
			case RoleModuleIndexer:
				c.OnChunk("modules", false)
			}
		},
	}
	st, err := RunAutonomousProject(cfg)
	if err == nil {
		t.Fatalf("expected failure after repeated validation rejects, got state %+v", st)
	}
}

func TestEnsureReopenStep_CoversBothBranches(t *testing.T) {
	stateWithDone := &OrchestrationState{Plan: []PlanStep{{Index: 1, Done: true}}}
	reopened := ensureReopenStep(stateWithDone)
	if reopened == nil || reopened.Index != 1 || reopened.Done {
		t.Fatalf("expected reopen existing done step, got %+v", reopened)
	}

	empty := &OrchestrationState{Plan: nil}
	created := ensureReopenStep(empty)
	if created == nil || created.Index != 1 || created.Done {
		t.Fatalf("expected synthetic reopen step, got %+v", created)
	}
}

func TestEventLoop_NonCanceledWaitErrorBranch(t *testing.T) {
	dir := t.TempDir()
	brain, err := NewOrchestrationBrain(dir, "o", "r", "brief", "t")
	if err != nil {
		t.Fatalf("brain: %v", err)
	}
	bus := NewEventBus()
	comms := NewAgentCommsHub()
	var gotErr string
	cfg := OrchestratorConfig{
		ProjectPath:     dir,
		Owner:           "o",
		Repo:            "r",
		Brief:           "brief",
		SessionIDPrefix: "t",
		OnPhase:         func(string, string) {},
		OnProgress:      func(string) {},
		OnError:         func(msg string) { gotErr = msg },
		ChatFn:          func(*ChatContext, string) {},
	}
	dispatcher := NewTeamDispatcher(brain, bus, cfg, 4, comms)
	ctx, cancel := stdctx.WithTimeout(stdctx.Background(), 1*time.Millisecond)
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
	eo.eventLoop()
	if !strings.Contains(gotErr, "team execution failed") {
		t.Fatalf("expected non-canceled wait error path, got %q", gotErr)
	}
}
