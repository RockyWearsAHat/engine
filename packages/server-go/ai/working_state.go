package ai

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/engine/server/db"
)

// WorkingState tracks the agent's in-progress task state across loop iterations.
// It is serialized into the LLM context so the AI retains orientation even when
// old messages are trimmed from the conversation window.
type WorkingState struct {
	CurrentTask       string    `json:"currentTask"`
	AttemptCount      int       `json:"attemptCount"`
	FailedApproaches  []string  `json:"failedApproaches"`
	CurrentHypothesis string    `json:"currentHypothesis"`
	Observations      []string  `json:"observations"`
	LastSuccess       string    `json:"lastSuccess"`
	UpdatedAt         time.Time `json:"updatedAt"`
}

// RecordAttempt increments the attempt counter and notes the approach if provided.
func (ws *WorkingState) RecordAttempt(approach string) {
	ws.AttemptCount++
	ws.UpdatedAt = time.Now()
	if approach != "" {
		ws.FailedApproaches = append(ws.FailedApproaches, approach)
	}
}

// AddObservation appends a tool-call or test result observation.
func (ws *WorkingState) AddObservation(obs string) {
	if obs == "" {
		return
	}
	ws.Observations = append(ws.Observations, obs)
	ws.UpdatedAt = time.Now()
}

// SetSuccess records the last thing that worked.
func (ws *WorkingState) SetSuccess(desc string) {
	ws.LastSuccess = desc
	ws.UpdatedAt = time.Now()
}

// ContextBlock renders WorkingState as a compact LLM-context block.
// Always injected at the top of the system prompt so the AI knows where it is
// even when the conversation history has been trimmed.
func (ws *WorkingState) ContextBlock() string {
	if ws == nil || ws.CurrentTask == "" {
		return ""
	}
	var b strings.Builder
	b.WriteString("<working_state>\n")
	fmt.Fprintf(&b, "task: %s\n", ws.CurrentTask)
	fmt.Fprintf(&b, "attempts: %d\n", ws.AttemptCount)
	if ws.CurrentHypothesis != "" {
		fmt.Fprintf(&b, "hypothesis: %s\n", ws.CurrentHypothesis)
	}
	if len(ws.FailedApproaches) > 0 {
		fmt.Fprintf(&b, "failed_approaches: %s\n", strings.Join(ws.FailedApproaches, " | "))
	}
	if len(ws.Observations) > 0 {
		recent := ws.Observations
		if len(recent) > 5 {
			recent = recent[len(recent)-5:]
		}
		fmt.Fprintf(&b, "recent_observations: %s\n", strings.Join(recent, " | "))
	}
	if ws.LastSuccess != "" {
		fmt.Fprintf(&b, "last_success: %s\n", ws.LastSuccess)
	}
	b.WriteString("</working_state>")
	return b.String()
}

// saveWorkingStateFn is injectable for tests.
var saveWorkingStateFn = db.SaveWorkingState

// PersistWorkingState writes the current working state to the DB.
func PersistWorkingState(sessionID string, ws *WorkingState) {
	if sessionID == "" || ws == nil {
		return
	}
	// WorkingState contains only basic types and slices of strings — Marshal never fails.
	data, _ := json.Marshal(ws)
	_ = saveWorkingStateFn(sessionID, string(data))
}

// LoadWorkingState retrieves the most recent working state for a session.
// Returns a zero-value WorkingState if none is stored yet.
var loadWorkingStateFn = db.LoadWorkingState

func LoadWorkingStateForSession(sessionID string) WorkingState {
	if sessionID == "" {
		return WorkingState{}
	}
	raw, err := loadWorkingStateFn(sessionID)
	if err != nil || raw == "" {
		return WorkingState{}
	}
	var ws WorkingState
	if err := json.Unmarshal([]byte(raw), &ws); err != nil {
		return WorkingState{}
	}
	return ws
}
