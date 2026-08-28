package ai

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
	ID            string                 `json:"id"`
	Role          string                 `json:"role"`
	AssignedSteps []int                  `json:"assigned_steps"` // indices into Plan
	Status        string                 `json:"status"`         // queued, running, blocked, done, failed
	DependsOn     []string               `json:"depends_on"`     // team IDs this blocks on
	Feedback      string                 `json:"feedback,omitempty"`
	LastError     string                 `json:"last_error,omitempty"`
	StartedAt     time.Time              `json:"started_at,omitempty"`
	CompletedAt   time.Time              `json:"completed_at,omitempty"`
	Progress      map[string]interface{} `json:"progress,omitempty"`
	Metadata      map[string]interface{} `json:"metadata,omitempty"`
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
	Requirements ProjectRequirements  `json:"requirements"`
	Plan         []PlanStep           `json:"plan"`
	Teams        map[string]TeamState `json:"teams"`

	// Lifecycle
	OuterIterations int       `json:"outer_iterations"`
	StartedAt       time.Time `json:"started_at"`
	UpdatedAt       time.Time `json:"updated_at"`
	CompletedAt     time.Time `json:"completed_at,omitempty"`
	LastValidation  string    `json:"last_validation,omitempty"`

	// Session tracking
	SessionIDPrefix string `json:"session_id_prefix"`
	// StateSlug namespaces the on-disk file. Empty = brain.json (whole
	// project). Task-mode runs set it so two items on one repo keep two brains.
	StateSlug string `json:"state_slug,omitempty"`
}

// brainStatePath: brain.json, or brain-<slug>.json when namespaced.
func brainStatePath(projectPath, slug string) string {
	name := "brain.json"
	if slug = strings.TrimSpace(slug); slug != "" {
		name = "brain-" + slug + ".json"
	}
	return filepath.Join(projectPath, ".engine", name)
}

// stageLoadBrainFromDisk attempts to load persisted brain state from disk.
func stageLoadBrainFromDisk(projectPath string) (*OrchestrationBrain, error) {
	return stageLoadBrainFromDiskSlug(projectPath, "")
}

// stageLoadBrainFromDiskSlug loads the namespaced brain file.
func stageLoadBrainFromDiskSlug(projectPath, slug string) (*OrchestrationBrain, error) {
	data, err := os.ReadFile(brainStatePath(projectPath, slug))
	if err != nil {
		return nil, err
	}
	brain := &OrchestrationBrain{}
	if err := json.Unmarshal(data, brain); err != nil {
		return nil, err
	}
	return brain, nil
}

// stageInitFreshBrain creates a new OrchestrationBrain with default initialization.
func stageInitFreshBrain(projectPath, owner, repo, brief, sessionIDPrefix string) *OrchestrationBrain {
	return &OrchestrationBrain{
		ProjectPath:     projectPath,
		Owner:           owner,
		Repo:            repo,
		Brief:           brief,
		SessionIDPrefix: sessionIDPrefix,
		Teams:           make(map[string]TeamState),
		StartedAt:       time.Now(),
		UpdatedAt:       time.Now(),
	}
}

// NewOrchestrationBrain initializes the brain from persisted state or fresh.
func NewOrchestrationBrain(projectPath, owner, repo, brief, sessionIDPrefix string) (*OrchestrationBrain, error) {
	return NewOrchestrationBrainSlug(projectPath, owner, repo, brief, sessionIDPrefix, "")
}

// NewOrchestrationBrainSlug is NewOrchestrationBrain with a state namespace.
// Task mode passes taskSlug(TaskID); whole-project runs pass "".
func NewOrchestrationBrainSlug(projectPath, owner, repo, brief, sessionIDPrefix, slug string) (*OrchestrationBrain, error) {
	slug = strings.TrimSpace(slug)
	// Try loading from disk
	if brain, err := stageLoadBrainFromDiskSlug(projectPath, slug); err == nil {
		brain.UpdatedAt = time.Now()
		brain.StateSlug = slug
		return brain, nil
	}

	// Fall back to fresh initialization
	brain := stageInitFreshBrain(projectPath, owner, repo, brief, sessionIDPrefix)
	brain.StateSlug = slug
	return brain, nil
}

// stageParseVocab transforms a vocabulary string into a map of key-value pairs.
func stageParseVocab(vocabulary string) map[string]string {
	return parseVocabularyMap(vocabulary)
}

// stageUpdateRequirementsState mutates the brain's requirements with doc layers and parsed vocabulary.
func stageUpdateRequirementsState(b *OrchestrationBrain, design, vocabulary, prd, moduleIndex string) {
	b.Requirements.Design = design
	b.Requirements.PRD = prd
	b.Requirements.ModuleIndex = moduleIndex
	b.Requirements.Vocabulary = stageParseVocab(vocabulary)
}

// stageLockAndPersist executes the lock-mutate-timestamp-persist side-effect chain for global state.
// mutator is called with the unlocked brain and is responsible for all state changes.
func (b *OrchestrationBrain) stageLockAndPersist(mutator func(*OrchestrationBrain) error) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if err := mutator(b); err != nil {
		return err
	}

	b.UpdatedAt = time.Now()
	return b.persist()
}

// UpdateRequirements loads the three doc layers into brain.
func (b *OrchestrationBrain) UpdateRequirements(design, vocabulary, prd, moduleIndex string) error {
	return b.stageLockAndPersist(func(brain *OrchestrationBrain) error {
		stageUpdateRequirementsState(brain, design, vocabulary, prd, moduleIndex)
		return nil
	})
}

func parseVocabularyMap(vocabulary string) map[string]string {
	parsed := make(map[string]string)
	for _, raw := range strings.Split(vocabulary, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		line = strings.TrimPrefix(line, "-")
		line = strings.TrimPrefix(line, "*")
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])
		if key == "" || value == "" {
			continue
		}
		parsed[key] = value
	}
	return parsed
}

// UpdatePlan replaces the plan with a new one.
func (b *OrchestrationBrain) UpdatePlan(steps []PlanStep) error {
	return b.stageLockAndPersist(func(brain *OrchestrationBrain) error {
		brain.Plan = steps
		return nil
	})
}

// stageInitTeamState creates a new TeamState with given parameters.
func stageInitTeamState(teamID, role string, steps []int, dependsOn []string) TeamState {
	return TeamState{
		ID:            teamID,
		Role:          role,
		AssignedSteps: steps,
		Status:        "queued",
		DependsOn:     dependsOn,
		Progress:      make(map[string]interface{}),
		Metadata:      make(map[string]interface{}),
	}
}

// AddTeam registers a new team.
func (b *OrchestrationBrain) AddTeam(teamID string, role string, steps []int, dependsOn []string) error {
	return b.stageLockAndPersist(func(brain *OrchestrationBrain) error {
		brain.Teams[teamID] = stageInitTeamState(teamID, role, steps, dependsOn)
		return nil
	})
}

// stageUpdateTeamTimestamps mutates team status and sets started/completed times as appropriate.
func stageUpdateTeamTimestamps(t *TeamState, newStatus string) {
	t.Status = newStatus
	if newStatus == "running" && t.StartedAt.IsZero() {
		t.StartedAt = time.Now()
	}
	if newStatus == "done" || newStatus == "failed" {
		t.CompletedAt = time.Now()
	}
}

// stageApplyTeamMutation executes the lock-fetch-mutate-timestamp-persist side-effect chain.
// mutator is called with the fetched TeamState and returns any error during mutation.
func (b *OrchestrationBrain) stageApplyTeamMutation(teamID string, mutator func(*TeamState) error) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	t, ok := b.Teams[teamID]
	if !ok {
		return fmt.Errorf("team %s not found", teamID)
	}

	if err := mutator(&t); err != nil {
		return err
	}

	b.Teams[teamID] = t
	b.UpdatedAt = time.Now()
	return b.persist()
}

// UpdateTeamStatus changes a team's status.
func (b *OrchestrationBrain) UpdateTeamStatus(teamID, newStatus string) error {
	return b.stageApplyTeamMutation(teamID, func(t *TeamState) error {
		stageUpdateTeamTimestamps(t, newStatus)
		return nil
	})
}

// stageUpdateTeamFeedback mutates the team with new feedback.
func stageUpdateTeamFeedback(t *TeamState, feedback string) {
	t.Feedback = feedback
}

// UpdateTeamFeedback records feedback for a team (e.g., validation failure).
func (b *OrchestrationBrain) UpdateTeamFeedback(teamID, feedback string) error {
	return b.stageApplyTeamMutation(teamID, func(t *TeamState) error {
		stageUpdateTeamFeedback(t, feedback)
		return nil
	})
}

func (b *OrchestrationBrain) evaluateTeamDependencies(depIDs []string) ([]string, bool) {
	var blocked []string
	isBlocked := false
	for _, depID := range depIDs {
		depTeam, ok := b.Teams[depID]
		if !ok || depTeam.Status != "done" {
			blocked = append(blocked, depID)
			isBlocked = true
		}
	}
	return blocked, isBlocked
}

// TeamBlockedOn returns true if teamID is blocked by any uncompleted dependencies.
func (b *OrchestrationBrain) TeamBlockedOn(teamID string) []string {
	b.mu.RLock()
	defer b.mu.RUnlock()

	t, ok := b.Teams[teamID]
	if !ok {
		return nil
	}

	blocked, _ := b.evaluateTeamDependencies(t.DependsOn)
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

		_, isBlocked := b.evaluateTeamDependencies(t.DependsOn)
		if !isBlocked {
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

// MarkStepDone marks one plan step complete in place.
//
// This exists instead of the GetPlan → mutate → UpdatePlan sequence team
// workers used to run. That sequence is a lost update the moment two teams
// work in parallel: each snapshots the whole plan, marks its own step, and
// writes the whole plan back, so the second write silently reverts the first
// team's step to not-done. It never mattered while the event orchestrator was
// unreachable; it is a correctness bug the instant parallel teams are enabled.
// Returns false when idx is out of range.
func (b *OrchestrationBrain) MarkStepDone(idx int) bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	if idx < 0 || idx >= len(b.Plan) {
		return false
	}
	b.Plan[idx].Done = true
	b.Plan[idx].UpdatedAt = time.Now().Format(time.RFC3339)
	b.UpdatedAt = time.Now()
	_ = b.persist()
	return true
}

// GetRequirements returns a snapshot of the requirements.
//
// The map is copied rather than shared: handing out the live Vocabulary map
// would let a reader iterate it while the intake phase replaces it.
func (b *OrchestrationBrain) GetRequirements() ProjectRequirements {
	b.mu.RLock()
	defer b.mu.RUnlock()

	req := b.Requirements
	if b.Requirements.Vocabulary != nil {
		vocab := make(map[string]string, len(b.Requirements.Vocabulary))
		for k, v := range b.Requirements.Vocabulary {
			vocab[k] = v
		}
		req.Vocabulary = vocab
	}
	return req
}

// NextOuterIteration increments the outer-iteration counter and returns the
// new value, so the caller never has to read the field back unlocked.
func (b *OrchestrationBrain) NextOuterIteration() int {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.OuterIterations++
	b.UpdatedAt = time.Now()
	return b.OuterIterations
}

// OuterIterationCount reports the current outer-iteration count.
func (b *OrchestrationBrain) OuterIterationCount() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.OuterIterations
}

// SetLastValidation records the latest validation feedback.
func (b *OrchestrationBrain) SetLastValidation(feedback string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.LastValidation = feedback
	b.UpdatedAt = time.Now()
}

// GetLastValidation returns the latest validation feedback.
func (b *OrchestrationBrain) GetLastValidation() string {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.LastValidation
}

// ResetTeams clears the team table ahead of a re-plan.
func (b *OrchestrationBrain) ResetTeams() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.Teams = make(map[string]TeamState)
	b.UpdatedAt = time.Now()
}

// TeamCount reports how many teams are registered.
func (b *OrchestrationBrain) TeamCount() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.Teams)
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

// UnfinishedTeams counts teams that are neither done nor failed.
func (b *OrchestrationBrain) UnfinishedTeams() int {
	b.mu.RLock()
	defer b.mu.RUnlock()

	n := 0
	for _, t := range b.Teams {
		if t.Status != "done" && t.Status != "failed" {
			n++
		}
	}
	return n
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

// StateSnapshot renders the brain as an OrchestrationState under the read lock.
//
// Every field here is written by the event loop goroutine while the caller that
// wants a snapshot runs on another one — RunEventOrchestratorAsState returns to
// its caller the instant the loop starts, so "read the brain" and "the loop is
// mutating the brain" are simultaneous by construction, not by accident. Taking
// the lock here is what makes the wrapper safe to call at any moment.
func (b *OrchestrationBrain) StateSnapshot() *OrchestrationState {
	b.mu.RLock()
	defer b.mu.RUnlock()

	plan := make([]PlanStep, len(b.Plan))
	copy(plan, b.Plan)
	return &OrchestrationState{
		Repo:            b.Repo,
		Owner:           b.Owner,
		Brief:           b.Brief,
		Plan:            plan,
		OuterIterations: b.OuterIterations,
		StartedAt:       b.StartedAt.Format(time.RFC3339),
		UpdatedAt:       b.UpdatedAt.Format(time.RFC3339),
		CompletedAt:     b.CompletedAt.Format(time.RFC3339),
		LastValidation:  b.LastValidation,
	}
}

// stagePersistBrain persists the brain to disk by creating the .engine directory,
// marshaling to JSON, and writing the file.
func stagePersistBrain(projectPath string, brain *OrchestrationBrain) error {
	_ = os.MkdirAll(filepath.Join(projectPath, ".engine"), 0755)

	statePath := brainStatePath(projectPath, brain.StateSlug)
	data, _ := json.MarshalIndent(brain, "", "  ")

	return os.WriteFile(statePath, data, 0644)
}

func (b *OrchestrationBrain) persist() error {
	return stagePersistBrain(b.ProjectPath, b)
}
