package ai

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// ProjectRequirements holds the immutable requirements + vocabulary.
type ProjectRequirements struct {
	// From vocabulary.md
	Vocabulary map[string]string `json:"vocabulary,omitempty"`
	// From prd.md
	PRD string `json:"prd"`
	// From design.md
	Design string `json:"design"`
	// From modules.md
	ModuleIndex string `json:"module_index,omitempty"`
}

// TeamState tracks a single team's progress.
type TeamState struct {
	ID             string                 `json:"id"`
	Role           string                 `json:"role"`
	AssignedSteps  []int                  `json:"assigned_steps"` // indices into Plan
	Status         string                 `json:"status"`         // queued, running, blocked, done, failed
	DependsOn      []string               `json:"depends_on"`     // team IDs this blocks on
	Feedback       string                 `json:"feedback,omitempty"`
	LastError      string                 `json:"last_error,omitempty"`
	StartedAt      time.Time              `json:"started_at,omitempty"`
	CompletedAt    time.Time              `json:"completed_at,omitempty"`
	Progress       map[string]interface{} `json:"progress,omitempty"`
	Metadata       map[string]interface{} `json:"metadata,omitempty"`
}

// OrchestrationBrain is the persistent brain: requirements, plan, team state, and validation.
type OrchestrationBrain struct {
	mu sync.RWMutex

	// Immutable once loaded
	ProjectPath string
	Owner       string
	Repo        string
	Brief       string

	// Mutable state
	Requirements ProjectRequirements `json:"requirements"`
	Plan         []PlanStep          `json:"plan"`
	Teams        map[string]TeamState `json:"teams"`

	// Lifecycle
	OuterIterations int       `json:"outer_iterations"`
	StartedAt       time.Time `json:"started_at"`
	UpdatedAt       time.Time `json:"updated_at"`
	CompletedAt     time.Time `json:"completed_at,omitempty"`
	LastValidation  string    `json:"last_validation,omitempty"`

	// Session tracking
	SessionIDPrefix string `json:"session_id_prefix"`
}

// NewOrchestrationBrain initializes the brain from persisted state or fresh.
func NewOrchestrationBrain(projectPath, owner, repo, brief, sessionIDPrefix string) (*OrchestrationBrain, error) {
	brain := &OrchestrationBrain{
		ProjectPath:     projectPath,
		Owner:           owner,
		Repo:            repo,
		Brief:           brief,
		SessionIDPrefix: sessionIDPrefix,
		Teams:           make(map[string]TeamState),
		StartedAt:       time.Now(),
		UpdatedAt:       time.Now(),
	}

	// Try loading from disk
	engineDir := filepath.Join(projectPath, ".engine")
	statePath := filepath.Join(engineDir, "brain.json")
	if data, err := os.ReadFile(statePath); err == nil {
		if err := json.Unmarshal(data, brain); err == nil {
			brain.UpdatedAt = time.Now()
			return brain, nil
		}
	}

	return brain, nil
}

// UpdateRequirements loads the three doc layers into brain.
func (b *OrchestrationBrain) UpdateRequirements(design, vocabulary, prd, moduleIndex string) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.Requirements.Design = design
	b.Requirements.PRD = prd
	b.Requirements.ModuleIndex = moduleIndex

	// Parse vocabulary (simple key: value format for now)
	b.Requirements.Vocabulary = make(map[string]string)
	if vocabulary != "" {
		// TODO: parse vocabulary.md into structured form
	}

	b.UpdatedAt = time.Now()
	return b.persist()
}

// UpdatePlan replaces the plan with a new one.
func (b *OrchestrationBrain) UpdatePlan(steps []PlanStep) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.Plan = steps
	b.UpdatedAt = time.Now()
	return b.persist()
}

// AddTeam registers a new team.
func (b *OrchestrationBrain) AddTeam(teamID string, role string, steps []int, dependsOn []string) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.Teams[teamID] = TeamState{
		ID:            teamID,
		Role:          role,
		AssignedSteps: steps,
		Status:        "queued",
		DependsOn:     dependsOn,
		Progress:      make(map[string]interface{}),
		Metadata:      make(map[string]interface{}),
	}

	b.UpdatedAt = time.Now()
	return b.persist()
}

// UpdateTeamStatus changes a team's status.
func (b *OrchestrationBrain) UpdateTeamStatus(teamID, newStatus string) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	t, ok := b.Teams[teamID]
	if !ok {
		return fmt.Errorf("team %s not found", teamID)
	}

	t.Status = newStatus
	if newStatus == "running" && t.StartedAt.IsZero() {
		t.StartedAt = time.Now()
	}
	if newStatus == "done" || newStatus == "failed" {
		t.CompletedAt = time.Now()
	}
	b.Teams[teamID] = t
	b.UpdatedAt = time.Now()
	return b.persist()
}

// UpdateTeamFeedback records feedback for a team (e.g., validation failure).
func (b *OrchestrationBrain) UpdateTeamFeedback(teamID, feedback string) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	t, ok := b.Teams[teamID]
	if !ok {
		return fmt.Errorf("team %s not found", teamID)
	}

	t.Feedback = feedback
	b.Teams[teamID] = t
	b.UpdatedAt = time.Now()
	return b.persist()
}

// TeamBlockedOn returns true if teamID is blocked by any uncompleted dependencies.
func (b *OrchestrationBrain) TeamBlockedOn(teamID string) []string {
	b.mu.RLock()
	defer b.mu.RUnlock()

	t, ok := b.Teams[teamID]
	if !ok {
		return nil
	}

	var blocked []string
	for _, depID := range t.DependsOn {
		depTeam, ok := b.Teams[depID]
		if !ok || depTeam.Status != "done" {
			blocked = append(blocked, depID)
		}
	}
	return blocked
}

// ReadyTeams returns all teams that are queued and have no blocking dependencies.
func (b *OrchestrationBrain) ReadyTeams() []TeamState {
	b.mu.RLock()
	defer b.mu.RUnlock()

	var ready []TeamState
	for _, t := range b.Teams {
		if t.Status != "queued" {
			continue
		}

		blocked := false
		for _, depID := range t.DependsOn {
			depTeam, ok := b.Teams[depID]
			if !ok || depTeam.Status != "done" {
				blocked = true
				break
			}
		}

		if !blocked {
			ready = append(ready, t)
		}
	}

	return ready
}

// GetPlan returns a snapshot of the plan.
func (b *OrchestrationBrain) GetPlan() []PlanStep {
	b.mu.RLock()
	defer b.mu.RUnlock()

	plan := make([]PlanStep, len(b.Plan))
	copy(plan, b.Plan)
	return plan
}

// GetTeam returns a snapshot of a team.
func (b *OrchestrationBrain) GetTeam(teamID string) (TeamState, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	t, ok := b.Teams[teamID]
	if !ok {
		return TeamState{}, fmt.Errorf("team %s not found", teamID)
	}
	return t, nil
}

// AllTeamsDone returns true if all teams are in done or failed state.
func (b *OrchestrationBrain) AllTeamsDone() bool {
	b.mu.RLock()
	defer b.mu.RUnlock()

	if len(b.Teams) == 0 {
		return true
	}

	for _, t := range b.Teams {
		if t.Status != "done" && t.Status != "failed" {
			return false
		}
	}
	return true
}

// AnyTeamFailed returns true if any team is in failed state.
func (b *OrchestrationBrain) AnyTeamFailed() bool {
	b.mu.RLock()
	defer b.mu.RUnlock()

	for _, t := range b.Teams {
		if t.Status == "failed" {
			return true
		}
	}
	return false
}

// MarkCompleted marks the entire project as done.
func (b *OrchestrationBrain) MarkCompleted() error {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.CompletedAt = time.Now()
	b.UpdatedAt = time.Now()
	return b.persist()
}

func (b *OrchestrationBrain) persist() error {
	engineDir := filepath.Join(b.ProjectPath, ".engine")
	_ = os.MkdirAll(engineDir, 0755)

	statePath := filepath.Join(engineDir, "brain.json")
	data, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(statePath, data, 0644)
}
