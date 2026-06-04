package ai

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestNewOrchestrationBrain_Fresh verifies fresh brain is created when no file exists.
func TestNewOrchestrationBrain_Fresh(t *testing.T) {
	dir := t.TempDir()
	brain, err := NewOrchestrationBrain(dir, "alice", "myrepo", "build a todo app", "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if brain.Owner != "alice" || brain.Repo != "myrepo" {
		t.Errorf("fields not set: owner=%q repo=%q", brain.Owner, brain.Repo)
	}
	if brain.Teams == nil {
		t.Error("expected initialized Teams map")
	}
}

// TestNewOrchestrationBrain_LoadFromDisk verifies existing brain.json is loaded.
func TestNewOrchestrationBrain_LoadFromDisk(t *testing.T) {
	dir := t.TempDir()
	engineDir := filepath.Join(dir, ".engine")
	if err := os.MkdirAll(engineDir, 0o755); err != nil {
		t.Fatal(err)
	}
	existing := &OrchestrationBrain{
		Owner: "bob",
		Repo:  "saved-repo",
		Brief: "saved brief",
		Teams: map[string]TeamState{
			"t1": {ID: "t1", Role: "general", Status: "done"},
		},
	}
	data, _ := json.Marshal(existing)
	if err := os.WriteFile(filepath.Join(engineDir, "brain.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	brain, err := NewOrchestrationBrain(dir, "bob", "saved-repo", "saved brief", "t")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should have loaded from disk — team t1 should be present
	if _, ok := brain.Teams["t1"]; !ok {
		t.Error("expected team t1 to be loaded from disk")
	}
}

// TestOrchestrationBrain_UpdateRequirements verifies fields are updated and persisted.
func TestOrchestrationBrain_UpdateRequirements(t *testing.T) {
	dir := t.TempDir()
	brain, _ := NewOrchestrationBrain(dir, "o", "r", "brief", "t")
	err := brain.UpdateRequirements("design doc", "- Foo: thing", "prd text", "modules idx")
	if err != nil {
		t.Fatalf("UpdateRequirements: %v", err)
	}
	if brain.Requirements.Design != "design doc" {
		t.Errorf("design not set: %q", brain.Requirements.Design)
	}
	if brain.Requirements.PRD != "prd text" {
		t.Errorf("prd not set: %q", brain.Requirements.PRD)
	}
	if got := brain.Requirements.Vocabulary["Foo"]; got != "thing" {
		t.Errorf("expected parsed vocabulary entry, got %q", got)
	}
	// Verify persisted to disk
	data, err := os.ReadFile(filepath.Join(dir, ".engine", "brain.json"))
	if err != nil {
		t.Fatalf("brain.json not found after persist: %v", err)
	}
	if len(data) == 0 {
		t.Error("brain.json is empty")
	}
}

// TestOrchestrationBrain_ReadyTeams_NoTeams verifies empty brain has no ready teams.
func TestOrchestrationBrain_ReadyTeams_NoTeams(t *testing.T) {
	dir := t.TempDir()
	brain, _ := NewOrchestrationBrain(dir, "o", "r", "brief", "t")
	ready := brain.ReadyTeams()
	if len(ready) != 0 {
		t.Errorf("expected no ready teams, got %d", len(ready))
	}
}

// TestOrchestrationBrain_ReadyTeams_AllQueued verifies queued teams with no deps are ready.
func TestOrchestrationBrain_ReadyTeams_AllQueued(t *testing.T) {
	dir := t.TempDir()
	brain, _ := NewOrchestrationBrain(dir, "o", "r", "brief", "t")
	_ = brain.AddTeam("t1", "general", []int{0}, nil)
	_ = brain.AddTeam("t2", "api", []int{1}, nil)
	ready := brain.ReadyTeams()
	if len(ready) != 2 {
		t.Errorf("expected 2 ready teams, got %d", len(ready))
	}
}

// TestOrchestrationBrain_ReadyTeams_Blocked verifies blocked teams are not ready.
func TestOrchestrationBrain_ReadyTeams_Blocked(t *testing.T) {
	dir := t.TempDir()
	brain, _ := NewOrchestrationBrain(dir, "o", "r", "brief", "t")
	_ = brain.AddTeam("t1", "general", []int{0}, nil)
	_ = brain.AddTeam("t2", "api", []int{1}, []string{"t1"}) // blocked on t1
	ready := brain.ReadyTeams()
	if len(ready) != 1 {
		t.Errorf("expected 1 ready team (not blocked), got %d", len(ready))
	}
	if ready[0].ID != "t1" {
		t.Errorf("expected t1 to be ready, got %q", ready[0].ID)
	}
}

// TestOrchestrationBrain_AllTeamsDone_Empty verifies empty teams → true.
func TestOrchestrationBrain_AllTeamsDone_Empty(t *testing.T) {
	dir := t.TempDir()
	brain, _ := NewOrchestrationBrain(dir, "o", "r", "brief", "t")
	if !brain.AllTeamsDone() {
		t.Error("empty teams should be considered done")
	}
}

// TestOrchestrationBrain_AllTeamsDone_HasQueued verifies queued team → false.
func TestOrchestrationBrain_AllTeamsDone_HasQueued(t *testing.T) {
	dir := t.TempDir()
	brain, _ := NewOrchestrationBrain(dir, "o", "r", "brief", "t")
	_ = brain.AddTeam("t1", "general", []int{0}, nil)
	if brain.AllTeamsDone() {
		t.Error("team with status=queued should not be done")
	}
}

// TestOrchestrationBrain_AllTeamsDone_AllDone verifies all done/failed → true.
func TestOrchestrationBrain_AllTeamsDone_AllDone(t *testing.T) {
	dir := t.TempDir()
	brain, _ := NewOrchestrationBrain(dir, "o", "r", "brief", "t")
	_ = brain.AddTeam("t1", "general", []int{0}, nil)
	_ = brain.AddTeam("t2", "api", []int{1}, nil)
	if err := brain.UpdateTeamStatus("t1", "done"); err != nil {
		t.Fatalf("update t1 status: %v", err)
	}
	if err := brain.UpdateTeamStatus("t2", "failed"); err != nil {
		t.Fatalf("update t2 status: %v", err)
	}
	if !brain.AllTeamsDone() {
		t.Error("all done/failed teams should be considered done")
	}
}

// TestOrchestrationBrain_AnyTeamFailed_None verifies no failed teams → false.
func TestOrchestrationBrain_AnyTeamFailed_None(t *testing.T) {
	dir := t.TempDir()
	brain, _ := NewOrchestrationBrain(dir, "o", "r", "brief", "t")
	_ = brain.AddTeam("t1", "general", []int{0}, nil)
	if err := brain.UpdateTeamStatus("t1", "done"); err != nil {
		t.Fatalf("update t1 status: %v", err)
	}
	if brain.AnyTeamFailed() {
		t.Error("no failed teams, AnyTeamFailed should be false")
	}
}

// TestOrchestrationBrain_AnyTeamFailed_HasFailed verifies failed team → true.
func TestOrchestrationBrain_AnyTeamFailed_HasFailed(t *testing.T) {
	dir := t.TempDir()
	brain, _ := NewOrchestrationBrain(dir, "o", "r", "brief", "t")
	_ = brain.AddTeam("t1", "general", []int{0}, nil)
	if err := brain.UpdateTeamStatus("t1", "failed"); err != nil {
		t.Fatalf("update t1 status: %v", err)
	}
	if !brain.AnyTeamFailed() {
		t.Error("failed team, AnyTeamFailed should be true")
	}
}

// TestOrchestrationBrain_MarkCompleted verifies CompletedAt is set and persisted.
func TestOrchestrationBrain_MarkCompleted(t *testing.T) {
	dir := t.TempDir()
	brain, _ := NewOrchestrationBrain(dir, "o", "r", "brief", "t")
	if err := brain.MarkCompleted(); err != nil {
		t.Fatalf("MarkCompleted: %v", err)
	}
	if brain.CompletedAt.IsZero() {
		t.Error("expected CompletedAt to be set")
	}
	// Verify persisted
	data, err := os.ReadFile(filepath.Join(dir, ".engine", "brain.json"))
	if err != nil {
		t.Fatalf("brain.json not found after MarkCompleted: %v", err)
	}
	if len(data) == 0 {
		t.Error("brain.json is empty")
	}
}

func TestOrchestrationBrain_UpdateTeamStatus_TeamNotFound(t *testing.T) {
	dir := t.TempDir()
	brain, _ := NewOrchestrationBrain(dir, "o", "r", "brief", "t")
	err := brain.UpdateTeamStatus("missing", "running")
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected not found error, got %v", err)
	}
}

func TestOrchestrationBrain_UpdateTeamFeedback_TeamNotFound(t *testing.T) {
	dir := t.TempDir()
	brain, _ := NewOrchestrationBrain(dir, "o", "r", "brief", "t")
	err := brain.UpdateTeamFeedback("missing", "feedback")
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected not found error, got %v", err)
	}
}

func TestOrchestrationBrain_TeamBlockedOn_UnknownTeam(t *testing.T) {
	dir := t.TempDir()
	brain, _ := NewOrchestrationBrain(dir, "o", "r", "brief", "t")
	blocked := brain.TeamBlockedOn("missing")
	if blocked != nil {
		t.Fatalf("expected nil blocked list for missing team, got %v", blocked)
	}
}

func TestOrchestrationBrain_UpdateTeamStatus_RunningThenDoneSetsTimestamps(t *testing.T) {
	dir := t.TempDir()
	brain, _ := NewOrchestrationBrain(dir, "o", "r", "brief", "t")
	if err := brain.AddTeam("t1", "general", []int{0}, nil); err != nil {
		t.Fatalf("AddTeam: %v", err)
	}
	if err := brain.UpdateTeamStatus("t1", "running"); err != nil {
		t.Fatalf("running status: %v", err)
	}
	t1, err := brain.GetTeam("t1")
	if err != nil {
		t.Fatalf("GetTeam: %v", err)
	}
	if t1.StartedAt.IsZero() {
		t.Fatal("expected StartedAt to be set when running")
	}

	if err := brain.UpdateTeamStatus("t1", "done"); err != nil {
		t.Fatalf("done status: %v", err)
	}
	t1, err = brain.GetTeam("t1")
	if err != nil {
		t.Fatalf("GetTeam: %v", err)
	}
	if t1.CompletedAt.IsZero() {
		t.Fatal("expected CompletedAt to be set when done")
	}
}

func TestOrchestrationBrain_PersistWriteError(t *testing.T) {
	base := t.TempDir()
	fileProjectPath := filepath.Join(base, "not-a-dir")
	if err := os.WriteFile(fileProjectPath, []byte("x"), 0o644); err != nil {
		t.Fatalf("create file project path: %v", err)
	}

	brain := &OrchestrationBrain{
		ProjectPath: fileProjectPath,
		Owner:       "o",
		Repo:        "r",
		Brief:       "b",
		Teams:       map[string]TeamState{},
	}
	err := brain.persist()
	if err == nil {
		t.Fatal("expected persist write error when project path is a file")
	}
}
