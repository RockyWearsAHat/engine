package ai

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParsePlanFromText_ParsesNumberedSteps(t *testing.T) {
	input := strings.Join([]string{
		"1. Scaffold project",
		"   Create go.mod and a minimal main.go.",
		"   Acceptance: `go build ./...` exits 0",
		"",
		"2. Add HTTP server",
		"   Wire net/http on :8080 with a /health route.",
		"   Acceptance: curl localhost:8080/health returns 200",
		"",
		"3. Add tests",
		"   Cover /health with httptest.",
		"   Acceptance: go test ./... passes",
	}, "\n")

	steps := parsePlanFromText(input)
	if len(steps) != 3 {
		t.Fatalf("expected 3 steps, got %d", len(steps))
	}
	if steps[0].Title != "Scaffold project" {
		t.Errorf("step 1 title mismatch: %q", steps[0].Title)
	}
	if !strings.Contains(steps[0].Body, "go.mod") {
		t.Errorf("step 1 body missing go.mod: %q", steps[0].Body)
	}
	if !strings.Contains(steps[0].Acceptance, "go build") {
		t.Errorf("step 1 acceptance not captured: %q", steps[0].Acceptance)
	}
	for i, s := range steps {
		if s.Index != i+1 {
			t.Errorf("step %d has Index=%d", i, s.Index)
		}
	}
}

func TestParsePlanFromText_RenumbersSkippedNumbers(t *testing.T) {
	input := "1. A\n   body\n5. B\n   body\n"
	steps := parsePlanFromText(input)
	if len(steps) != 2 {
		t.Fatalf("expected 2 steps, got %d", len(steps))
	}
	if steps[0].Index != 1 || steps[1].Index != 2 {
		t.Errorf("renumber failed: %+v", []int{steps[0].Index, steps[1].Index})
	}
}

func TestParseReviewerVerdict_Approve(t *testing.T) {
	v, msg := parseReviewerVerdict("Some findings here\nNo issues found\nAPPROVE")
	if v != ReviewApprove {
		t.Fatalf("expected approve, got %d (msg=%q)", v, msg)
	}
}

func TestParseReviewerVerdict_Reject(t *testing.T) {
	out := "- main.go:12 - missing nil check\n- handler.go:5 - data race\nREJECT: data race on session map"
	v, msg := parseReviewerVerdict(out)
	if v != ReviewReject {
		t.Fatalf("expected reject, got %d", v)
	}
	if !strings.Contains(msg, "data race") {
		t.Errorf("reject message missing reason: %q", msg)
	}
}

func TestParseReviewerVerdict_Empty(t *testing.T) {
	v, _ := parseReviewerVerdict("")
	if v != ReviewInconclusive {
		t.Fatalf("expected inconclusive, got %d", v)
	}
}

func TestPersistOrchestration_WritesPlanMarkdownAndJSON(t *testing.T) {
	dir := t.TempDir()
	state := &OrchestrationState{
		Owner: "rocky",
		Repo:  "demo",
		Plan: []PlanStep{
			{Index: 1, Title: "Scaffold", Body: "Create files", Acceptance: "build passes", Done: true},
			{Index: 2, Title: "Add server", Body: "HTTP", Acceptance: "200 OK", LastFeedback: "missing handler"},
		},
		StartedAt: time.Now().UTC().Format(time.RFC3339),
		UpdatedAt: time.Now().UTC().Format(time.RFC3339),
	}
	if err := persistOrchestration(dir, state); err != nil {
		t.Fatalf("persist: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".engine", "orchestration.json")); err != nil {
		t.Errorf("orchestration.json missing: %v", err)
	}
	planData, err := os.ReadFile(filepath.Join(dir, ".engine", "plan.md"))
	if err != nil {
		t.Fatalf("plan.md missing: %v", err)
	}
	plan := string(planData)
	if !strings.Contains(plan, "[x]") || !strings.Contains(plan, "[ ]") {
		t.Errorf("plan.md missing checkboxes: %s", plan)
	}
	if !strings.Contains(plan, "missing handler") {
		t.Errorf("plan.md missing feedback: %s", plan)
	}
}

func TestLoadOrCreateOrchestrationState_Roundtrip(t *testing.T) {
	dir := t.TempDir()
	first, err := loadOrCreateOrchestrationState(dir, "rocky", "demo", "brief 1")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	first.Plan = []PlanStep{{Index: 1, Title: "x", Done: true}}
	if err := persistOrchestration(dir, first); err != nil {
		t.Fatalf("persist: %v", err)
	}
	second, err := loadOrCreateOrchestrationState(dir, "rocky", "demo", "brief 2")
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if len(second.Plan) != 1 || second.Plan[0].Title != "x" {
		t.Errorf("plan not persisted across reload: %+v", second.Plan)
	}
	if second.Brief != "brief 2" {
		t.Errorf("brief not refreshed on reload: %q", second.Brief)
	}
}

func TestOrchestratorHandle_StopRedirectPause(t *testing.T) {
	h := &OrchestratorHandle{
		cancel:    make(chan struct{}),
		approveCh: make(chan bool, 1),
	}
	h.Redirect("focus on tests")
	if got := h.takeRedirect(); got != "focus on tests" {
		t.Errorf("redirect not consumed: %q", got)
	}
	if got := h.takeRedirect(); got != "" {
		t.Errorf("redirect not single-use: %q", got)
	}
	h.Pause()
	if !h.isPaused() {
		t.Error("expected paused")
	}
	h.Resume()
	if h.isPaused() {
		t.Error("expected not paused")
	}
	h.Stop()
	select {
	case <-h.cancel:
	case <-time.After(time.Second):
		t.Fatal("cancel channel not closed by Stop")
	}
	h.Stop() // idempotent
}

func TestGetOrchestratorHandle_RegisterDeregister(t *testing.T) {
	if h := GetOrchestratorHandle("/nope"); h != nil {
		t.Fatal("unexpected handle for unknown project")
	}
	h := &OrchestratorHandle{cancel: make(chan struct{})}
	registerOrchestratorHandle("/tmp/proj", h)
	defer deregisterOrchestratorHandle("/tmp/proj")
	if got := GetOrchestratorHandle("/tmp/proj"); got != h {
		t.Fatalf("got %p want %p", got, h)
	}
	projects := ListOrchestratorProjects()
	found := false
	for _, p := range projects {
		if p == "/tmp/proj" {
			found = true
		}
	}
	if !found {
		t.Errorf("project not listed: %v", projects)
	}
}

func TestNewHandle_InitializesFields(t *testing.T) {
	h := NewHandle("/tmp/new-project")
	if h == nil {
		t.Fatal("expected non-nil handle")
	}
	if h.projectPath != "/tmp/new-project" {
		t.Fatalf("unexpected project path: %q", h.projectPath)
	}
	if h.cancel == nil {
		t.Fatal("cancel channel should be initialized")
	}
	if h.approveCh == nil {
		t.Fatal("approve channel should be initialized")
	}
	if cap(h.approveCh) != 1 {
		t.Fatalf("approve channel should be buffered by 1, got %d", cap(h.approveCh))
	}
}

func TestSkipExhaustedSteps_MarksOnlyStepsPastCap(t *testing.T) {
	state := &OrchestrationState{
		Plan: []PlanStep{
			{Index: 1, Title: "stuck", Attempts: 5, Done: false, LastFeedback: "reviewer rejected: missing test"},
			{Index: 2, Title: "fresh", Attempts: 2, Done: false},
			{Index: 3, Title: "done", Attempts: 1, Done: true},
		},
	}
	skipped := skipExhaustedSteps(state, 5)
	if len(skipped) != 1 || skipped[0] != 1 {
		t.Fatalf("expected to skip step 1, got %v", skipped)
	}
	if !state.Plan[0].Done {
		t.Errorf("step 1 should be Done after skip")
	}
	if !strings.Contains(state.Plan[0].LastFeedback, "skipped after 5 failed attempts") {
		t.Errorf("step 1 feedback missing skip note: %q", state.Plan[0].LastFeedback)
	}
	if state.Plan[1].Done {
		t.Errorf("step 2 still under cap, should not be Done")
	}
	if !state.Plan[2].Done {
		t.Errorf("step 3 was already Done, must stay Done")
	}
}

func TestSkipExhaustedSteps_ZeroCapIsNoop(t *testing.T) {
	state := &OrchestrationState{
		Plan: []PlanStep{{Index: 1, Attempts: 99, Done: false}},
	}
	if skipped := skipExhaustedSteps(state, 0); skipped != nil {
		t.Fatalf("zero cap should not skip anything, got %v", skipped)
	}
	if state.Plan[0].Done {
		t.Errorf("step should remain un-done when cap is 0")
	}
}

func TestSummarise_WithinLimit(t *testing.T) {
	input := "Short message"
	if got := summarise(input, 100); got != input {
		t.Errorf("expected %q, got %q", input, got)
	}
}

func TestSummarise_ExceedsLimit(t *testing.T) {
	input := "This is a very long message that definitely exceeds the limit"
	result := summarise(input, 20)
	if !strings.HasSuffix(result, "…") {
		t.Errorf("expected ellipsis suffix, got %q", result)
	}
	// Result should be shorter than input
	if len(result) >= len(input) {
		t.Errorf("expected truncation, got same or longer: %q", result)
	}
}

func TestWritePlanMarkdown_GeneratesMarkdown(t *testing.T) {
	dir := t.TempDir()
	// Create .engine dir (writePlanMarkdown doesn't create it)
	if err := os.MkdirAll(filepath.Join(dir, ".engine"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	state := &OrchestrationState{
		Repo:  "myrepo",
		Owner: "myorg",
		Plan: []PlanStep{
			{Index: 1, Title: "Step 1", Body: "Do X", Acceptance: "X works", Done: true},
			{Index: 2, Title: "Step 2", Body: "Do Y", Acceptance: "Y works", Done: false, LastFeedback: "retry"},
		},
		StartedAt: time.Now().UTC().Format(time.RFC3339),
		UpdatedAt: time.Now().UTC().Format(time.RFC3339),
	}
	if err := writePlanMarkdown(dir, state); err != nil {
		t.Fatalf("writePlanMarkdown: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, ".engine", "plan.md"))
	if err != nil {
		t.Fatalf("read plan.md: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "# Engine Project Plan") {
		t.Error("missing title")
	}
	if !strings.Contains(content, "[x]") || !strings.Contains(content, "[ ]") {
		t.Error("missing checkboxes")
	}
}

func TestIndentLines_MultilineText(t *testing.T) {
	input := "line 1\nline 2\nline 3"
	result := indentLines(input, "  ")
	expected := "line 1\n  line 2\n  line 3"
	if result != expected {
		t.Errorf("expected %q, got %q", expected, result)
	}
}

func TestIndentLines_SingleLine(t *testing.T) {
	input := "single line"
	result := indentLines(input, "  ")
	if result != input {
		t.Errorf("single line should not be indented, got %q", result)
	}
}
