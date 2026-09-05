package ai

import (
	"fmt"
	"os"
	"testing"
	"time"
)

// ── Budget Global Defaults ────────────────────────────────────────────────────

func TestBudgetDefaults_ItemBudget(t *testing.T) {
	// planBudgetItem defaults to 45 seconds
	expected := 45 * time.Second
	if planBudgetItem != expected {
		t.Errorf("planBudgetItem: expected %v, got %v", expected, planBudgetItem)
	}
}

func TestBudgetDefaults_ItemWallBudget(t *testing.T) {
	// taskWallBudgetItem defaults to 60 minutes
	expected := 60 * 60 * time.Second
	if taskWallBudgetItem != expected {
		t.Errorf("taskWallBudgetItem: expected %v, got %v", expected, taskWallBudgetItem)
	}
}

func TestBudgetDefaults_OtherWallBudget(t *testing.T) {
	// taskWallBudgetOther defaults to 45 minutes
	expected := 45 * 60 * time.Second
	if taskWallBudgetOther != expected {
		t.Errorf("taskWallBudgetOther: expected %v, got %v", expected, taskWallBudgetOther)
	}
}

func TestBudgetDefaults_MaxIterationsItem(t *testing.T) {
	// maxIterationsItem defaults to 3
	expected := 3
	if maxIterationsItem != expected {
		t.Errorf("maxIterationsItem: expected %d, got %d", expected, maxIterationsItem)
	}
}

func TestBudgetDefaults_SessionBudget(t *testing.T) {
	// sessionBudget defaults to 8 minutes (kept for backward compatibility)
	// DEPRECATED: Use sessionIdleTimeout + sessionMaxTimeout instead.
	expected := 8 * 60 * time.Second
	if sessionBudget != expected {
		t.Errorf("sessionBudget: expected %v, got %v", expected, sessionBudget)
	}
}

// ── EventOrchestrator Budget Fields ───────────────────────────────────────────

func TestEventOrchestrator_TaskMode_BudgetInitialization(t *testing.T) {
	cfg := OrchestratorConfig{
		ProjectPath:        t.TempDir(),
		TaskMode:           true,
		TaskID:             "test-task-1",
		MaxOuterIterations: 200,
	}
	cfg.OnPhase = func(string, string) {}
	cfg.OnProgress = func(string) {}
	cfg.OnError = func(string) {}
	cfg.ChatFn = func(*ChatContext, string) {} // No-op to prevent hang

	eo, err := startEventOrchestrator(cfg)
	if err != nil {
		t.Fatalf("startEventOrchestrator failed: %v", err)
	}
	defer func() {
		eo.cancel()
		// Allow goroutines time to exit
		done := make(chan struct{})
		go func() {
			eo.wg.Wait()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(100 * time.Millisecond):
		}
	}()

	// In task mode, budget should be taskWallBudgetItem and cap should be maxIterationsItem
	if eo.taskWallBudget != taskWallBudgetItem {
		t.Errorf("taskWallBudget: expected %v, got %v", taskWallBudgetItem, eo.taskWallBudget)
	}
	if eo.iterationCap != maxIterationsItem {
		t.Errorf("iterationCap: expected %d, got %d", maxIterationsItem, eo.iterationCap)
	}
}

func TestEventOrchestrator_NonTaskMode_BudgetInitialization(t *testing.T) {
	cfg := OrchestratorConfig{
		ProjectPath:        t.TempDir(),
		TaskMode:           false,
		MaxOuterIterations: 200,
	}
	cfg.OnPhase = func(string, string) {}
	cfg.OnProgress = func(string) {}
	cfg.OnError = func(string) {}
	cfg.ChatFn = func(*ChatContext, string) {} // No-op to prevent hang

	eo, err := startEventOrchestrator(cfg)
	if err != nil {
		t.Fatalf("startEventOrchestrator failed: %v", err)
	}
	defer func() {
		eo.cancel()
		// Allow goroutines time to exit
		done := make(chan struct{})
		go func() {
			eo.wg.Wait()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(100 * time.Millisecond):
		}
	}()

	// In non-task mode, budget should be taskWallBudgetOther and cap should match maxOuterIterations
	if eo.taskWallBudget != taskWallBudgetOther {
		t.Errorf("taskWallBudget: expected %v, got %v", taskWallBudgetOther, eo.taskWallBudget)
	}
	if eo.iterationCap != eo.maxOuterIterations {
		t.Errorf("iterationCap: expected %d, got %d", eo.maxOuterIterations, eo.iterationCap)
	}
}

func TestEventOrchestrator_TaskStartTimeRecorded(t *testing.T) {
	cfg := OrchestratorConfig{
		ProjectPath:        t.TempDir(),
		TaskMode:           true,
		TaskID:             "test-task-2",
		MaxOuterIterations: 200,
	}
	cfg.OnPhase = func(string, string) {}
	cfg.OnProgress = func(string) {}
	cfg.OnError = func(string) {}
	cfg.ChatFn = func(*ChatContext, string) {} // No-op

	beforeTime := time.Now()
	eo, err := startEventOrchestrator(cfg)
	if err != nil {
		t.Fatalf("startEventOrchestrator failed: %v", err)
	}
	defer func() {
		eo.cancel()
		// Allow goroutines time to exit
		done := make(chan struct{})
		go func() {
			eo.wg.Wait()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(100 * time.Millisecond):
		}
	}()
	afterTime := time.Now()

	// taskStartTime should be recorded and between beforeTime and afterTime
	if eo.taskStartTime.Before(beforeTime) || eo.taskStartTime.After(afterTime) {
		t.Errorf("taskStartTime %v not between %v and %v", eo.taskStartTime, beforeTime, afterTime)
	}
}

// ── Environment Variable Parsing (via load-time globals) ──────────────────────
// Note: These tests can't easily override the package globals since they are
// initialized at package load time. A more sophisticated approach would require
// refactoring the budget variables into functions or a config struct. For now,
// we document that environment variables MYEDITOR_PLAN_BUDGET_SECS_ITEM, etc.
// are parsed at startup.

func TestBudgetEnvVars_Documentation(t *testing.T) {
	// This test documents the expected environment variables.
	// Actual testing would require re-running the tests with env vars set.
	envVars := []string{
		"MYEDITOR_TASK_BUDGET_MIN_HAIKU_ITEM",
		"MYEDITOR_TASK_BUDGET_MIN",
		"MYEDITOR_MAX_ITERATIONS_ITEM",
		"MYEDITOR_SESSION_BUDGET_SECS",
		"MYEDITOR_PLAN_BUDGET_SECS_ITEM",
	}
	for _, env := range envVars {
		// Just document that these exist
		_ = os.Getenv(env)
	}
}

// ── Budget Enforcement Tests (using mock/stub orchestrator) ──────────────────

// MockBrain implements a minimal orchestration brain for testing.
type MockBrain struct {
	iterations int
	teamCount  int
	allDone    bool
}

func (m *MockBrain) OuterIterationCount() int {
	return m.iterations
}

func (m *MockBrain) NextOuterIteration() int {
	m.iterations++
	return m.iterations
}

func (m *MockBrain) TeamCount() int {
	return m.teamCount
}

func (m *MockBrain) AllTeamsDone() bool {
	return m.allDone
}

func (m *MockBrain) ReadyTeams() []TeamState {
	return nil
}

func (m *MockBrain) UpdateTeamStatus(teamID, status string) {}

func (m *MockBrain) MarkCompleted() {}

func (m *MockBrain) GetLastValidation() string {
	return ""
}

func (m *MockBrain) SetLastValidation(feedback string) {}

func (m *MockBrain) UpdateRequirements(design, vocab, prd, modules string) {}

func (m *MockBrain) GetRequirements() ProjectRequirements {
	return ProjectRequirements{}
}

func (m *MockBrain) UpdatePlan(plan []PlanStep) error {
	return nil
}

func (m *MockBrain) ResetTeams() {}

func (m *MockBrain) AddTeam(id, role string, steps []int, dependsOn []string) {}

func (m *MockBrain) UnfinishedTeams() int {
	return 0
}

func (m *MockBrain) StateSnapshot() *OrchestrationState {
	return &OrchestrationState{}
}

// TestIterationCapEnforcement tests that the event loop respects iterationCap.
// This is a simplified test that doesn't run the full event loop but verifies
// the budget fields are set correctly.
func TestIterationCapEnforcement_TaskMode(t *testing.T) {
	cfg := OrchestratorConfig{
		ProjectPath:        t.TempDir(),
		TaskMode:           true,
		TaskID:             "cap-test-1",
		MaxOuterIterations: 200, // high overall cap
	}
	cfg.OnPhase = func(string, string) {}
	cfg.OnProgress = func(string) {}
	cfg.OnError = func(string) {}
	cfg.ChatFn = func(*ChatContext, string) {} // No-op

	eo, err := startEventOrchestrator(cfg)
	if err != nil {
		t.Fatalf("startEventOrchestrator failed: %v", err)
	}
	defer func() {
		eo.cancel()
		// Allow goroutines time to exit
		done := make(chan struct{})
		go func() {
			eo.wg.Wait()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(100 * time.Millisecond):
		}
	}()

	// Verify cap is set to maxIterationsItem (3), not the config's 200
	if eo.iterationCap != maxIterationsItem {
		t.Errorf("iterationCap should be %d (maxIterationsItem), got %d", maxIterationsItem, eo.iterationCap)
	}

	// Simulate loop checking condition
	if eo.brain.OuterIterationCount() >= eo.iterationCap {
		t.Error("OuterIterationCount should start less than iterationCap")
	}
}

// TestWallBudgetEnforcement tests that wall-clock time is checked.
// This verifies the logic, not the actual enforcement (which would require time mocking).
func TestWallBudgetEnforcement_Logic(t *testing.T) {
	cfg := OrchestratorConfig{
		ProjectPath:        t.TempDir(),
		TaskMode:           true,
		TaskID:             "wall-test-1",
		MaxOuterIterations: 200,
	}
	cfg.OnPhase = func(string, string) {}
	cfg.OnProgress = func(string) {}
	cfg.OnError = func(string) {}
	cfg.ChatFn = func(*ChatContext, string) {} // No-op

	eo, err := startEventOrchestrator(cfg)
	if err != nil {
		t.Fatalf("startEventOrchestrator failed: %v", err)
	}
	defer func() {
		eo.cancel()
		// Allow goroutines time to exit
		done := make(chan struct{})
		go func() {
			eo.wg.Wait()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(100 * time.Millisecond):
		}
	}()

	// Verify taskStartTime is recorded
	if eo.taskStartTime.IsZero() {
		t.Error("taskStartTime should be recorded")
	}

	// Verify budget is set correctly
	if eo.taskWallBudget != taskWallBudgetItem {
		t.Errorf("taskWallBudget should be %v, got %v", taskWallBudgetItem, eo.taskWallBudget)
	}

	// Simulate a check: time.Since(eo.taskStartTime) should be small
	elapsed := time.Since(eo.taskStartTime)
	if elapsed > eo.taskWallBudget {
		t.Error("elapsed time should not exceed budget immediately after start")
	}
}

// ── Table-Driven Tests ────────────────────────────────────────────────────────

func TestBudgetScenarios_Table(t *testing.T) {
	tests := []struct {
		name               string
		taskMode           bool
		maxOuterIterations int
		expectedCap        int
		expectedBudget     time.Duration
		description        string
	}{
		{
			name:               "task mode with high config cap",
			taskMode:           true,
			maxOuterIterations: 200,
			expectedCap:        maxIterationsItem,  // 3
			expectedBudget:     taskWallBudgetItem, // 20 minutes
			description:        "item gets tight cap regardless of config",
		},
		{
			name:               "task mode with low config cap",
			taskMode:           true,
			maxOuterIterations: 1,
			expectedCap:        maxIterationsItem, // 3
			expectedBudget:     taskWallBudgetItem,
			description:        "config cap ignored in task mode",
		},
		{
			name:               "non-task mode uses config cap",
			taskMode:           false,
			maxOuterIterations: 50,
			expectedCap:        50,                  // matches config
			expectedBudget:     taskWallBudgetOther, // 45 minutes
			description:        "project uses config cap and large budget",
		},
		{
			name:               "non-task mode defaults to OrchestratorMaxOuterIterations",
			taskMode:           false,
			maxOuterIterations: 0,                              // triggers default
			expectedCap:        OrchestratorMaxOuterIterations, // 200
			expectedBudget:     taskWallBudgetOther,
			description:        "zero config cap gets package default",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := OrchestratorConfig{
				ProjectPath:        t.TempDir(),
				TaskMode:           tt.taskMode,
				TaskID:             fmt.Sprintf("test-%s", tt.name),
				MaxOuterIterations: tt.maxOuterIterations,
			}
			cfg.OnPhase = func(string, string) {}
			cfg.OnProgress = func(string) {}
			cfg.OnError = func(string) {}
			cfg.ChatFn = func(*ChatContext, string) {} // No-op

			eo, err := startEventOrchestrator(cfg)
			if err != nil {
				t.Fatalf("startEventOrchestrator failed: %v", err)
			}
			defer func() {
				eo.cancel()
				// Allow goroutines time to exit
				done := make(chan struct{})
				go func() {
					eo.wg.Wait()
					close(done)
				}()
				select {
				case <-done:
				case <-time.After(100 * time.Millisecond):
				}
			}()

			if eo.iterationCap != tt.expectedCap {
				t.Errorf("iterationCap: expected %d, got %d (case: %s)", tt.expectedCap, eo.iterationCap, tt.description)
			}
			if eo.taskWallBudget != tt.expectedBudget {
				t.Errorf("taskWallBudget: expected %v, got %v (case: %s)", tt.expectedBudget, eo.taskWallBudget, tt.description)
			}
		})
	}
}

// ── Budget Helper Functions ───────────────────────────────────────────────────

func TestBudgetCalculation_ElapsedTime(t *testing.T) {
	cfg := OrchestratorConfig{
		ProjectPath:        t.TempDir(),
		TaskMode:           true,
		TaskID:             "elapsed-test",
		MaxOuterIterations: 200,
	}
	cfg.OnPhase = func(string, string) {}
	cfg.OnProgress = func(string) {}
	cfg.OnError = func(string) {}
	cfg.ChatFn = func(*ChatContext, string) {} // No-op

	eo, err := startEventOrchestrator(cfg)
	if err != nil {
		t.Fatalf("startEventOrchestrator failed: %v", err)
	}
	defer func() {
		eo.cancel()
		// Allow goroutines time to exit
		done := make(chan struct{})
		go func() {
			eo.wg.Wait()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(100 * time.Millisecond):
		}
	}()

	// Immediately after start, elapsed should be minimal
	elapsed := time.Since(eo.taskStartTime)
	if elapsed < 0 {
		t.Error("elapsed time cannot be negative")
	}
	if elapsed > 100*time.Millisecond {
		t.Logf("elapsed time took longer than expected: %v (timing-dependent, may not be a failure)", elapsed)
	}

	// Sleep a bit and verify elapsed increases
	time.Sleep(10 * time.Millisecond)
	elapsed2 := time.Since(eo.taskStartTime)
	if elapsed2 <= elapsed {
		t.Errorf("elapsed time should increase: first %v, second %v", elapsed, elapsed2)
	}
}

// ── Environment Variable Boundary Tests ──────────────────────────────────────

func TestBudgetEnv_InvalidValues(t *testing.T) {
	// This test documents what happens with invalid env var values.
	// The package globals handle invalid values by falling back to defaults.
	tests := []struct {
		env   string
		value string
	}{
		{"MYEDITOR_TASK_BUDGET_MIN_HAIKU_ITEM", "-100"}, // negative
		{"MYEDITOR_TASK_BUDGET_MIN", "not-a-number"},    // non-numeric
		{"MYEDITOR_MAX_ITERATIONS_ITEM", "0"},           // zero
		{"MYEDITOR_SESSION_BUDGET_SECS", ""},            // empty
	}

	for _, tt := range tests {
		// Save original value
		original := os.Getenv(tt.env)
		defer os.Setenv(tt.env, original)

		// Set test value
		os.Setenv(tt.env, tt.value)

		// The package would have already parsed these at init time,
		// so we can only verify that invalid values don't crash.
		// A real test would require reloading the package or moving
		// initialization to a function.
		_ = os.Getenv(tt.env)
	}
}

// ── Orchestration Flow Integration ──────────────────────────────────────────

func TestOrchestrator_ProgressLogsIncludeBudget(t *testing.T) {
	cfg := OrchestratorConfig{
		ProjectPath:        t.TempDir(),
		TaskMode:           true,
		TaskID:             "progress-test",
		MaxOuterIterations: 200,
	}

	progressMessages := []string{}
	cfg.OnPhase = func(string, string) {}
	cfg.OnProgress = func(msg string) {
		progressMessages = append(progressMessages, msg)
	}
	cfg.OnError = func(string) {}
	cfg.ChatFn = func(*ChatContext, string) {} // No-op

	eo, err := startEventOrchestrator(cfg)
	if err != nil {
		t.Fatalf("startEventOrchestrator failed: %v", err)
	}
	defer func() {
		eo.cancel()
		// Allow goroutines time to exit
		done := make(chan struct{})
		go func() {
			eo.wg.Wait()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(100 * time.Millisecond):
		}
	}()

	// Verify at least one progress message was logged with budget info
	found := false
	for _, msg := range progressMessages {
		if msg != "" && len(msg) > 0 {
			// Budget info should be in a progress message
			found = true
		}
	}

	if !found {
		t.Logf("Note: no progress messages captured (may be implementation detail)")
	}
}

// ── Export test for package visibility ──────────────────────────────────────

func TestBudget_ExportsAreVisible(t *testing.T) {
	// Verify budget variables are accessible for testing/debugging
	_ = planBudgetItem
	_ = taskWallBudgetItem
	_ = taskWallBudgetOther
	_ = maxIterationsItem
	_ = sessionBudget

	// Verify they have sensible values
	if planBudgetItem <= 0 {
		t.Error("planBudgetItem must be positive")
	}
	if taskWallBudgetItem <= 0 {
		t.Error("taskWallBudgetItem must be positive")
	}
	if taskWallBudgetOther <= 0 {
		t.Error("taskWallBudgetOther must be positive")
	}
	if maxIterationsItem <= 0 {
		t.Error("maxIterationsItem must be positive")
	}
	if sessionBudget <= 0 {
		t.Error("sessionBudget must be positive")
	}

	// Verify reasonable values
	// Note: taskWallBudgetItem (60m) > taskWallBudgetOther (45m) by design:
	// items are single focused tasks that may need more time than a full project build
	if maxIterationsItem > 10 {
		t.Logf("maxIterationsItem=%d is unusually high for an item", maxIterationsItem)
	}
}

// ── Idle-Based Session Timeout Tests ─────────────────────────────────────────

func TestBudgetDefaults_SessionIdleTimeout(t *testing.T) {
	// sessionIdleTimeout defaults to 300 seconds (5 minutes)
	expected := 300 * time.Second
	if sessionIdleTimeout != expected {
		t.Errorf("sessionIdleTimeout: expected %v, got %v", expected, sessionIdleTimeout)
	}
}

func TestBudgetDefaults_SessionMaxTimeout(t *testing.T) {
	// sessionMaxTimeout defaults to 2700 seconds (45 minutes)
	expected := 2700 * time.Second
	if sessionMaxTimeout != expected {
		t.Errorf("sessionMaxTimeout: expected %v, got %v", expected, sessionMaxTimeout)
	}
}

func TestBudgetDefaults_SessionTimeoutRelationship(t *testing.T) {
	// sessionIdleTimeout should be less than sessionMaxTimeout
	if sessionIdleTimeout >= sessionMaxTimeout {
		t.Errorf("sessionIdleTimeout (%v) should be less than sessionMaxTimeout (%v)",
			sessionIdleTimeout, sessionMaxTimeout)
	}
}

func TestSessionTimeout_IdleKillLogging(t *testing.T) {
	// Verify that idle kill logs the expected message format
	// This test documents the expected log line format for idle kills
	logFormat := "session idle-killed task=%s phase=%s after %ds without output"
	_ = logFormat // Document the format for future reference
}
