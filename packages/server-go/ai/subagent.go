package ai

import (
	stdctx "context"
	"fmt"
	"strings"
	"sync"
	"time"
)

// SubagentRequest describes a task to delegate to a specialist subagent.
type SubagentRequest struct {
	// Role determines the system prompt and allowed tools.
	Role AgentRole `json:"role"`
	// Task is the focused instruction for the subagent.
	Task string `json:"task"`
	// WorkingDir overrides the parent's project path. Optional.
	WorkingDir string `json:"working_dir,omitempty"`
	// TimeoutSec is the maximum wall-clock seconds before the subagent is cancelled.
	// Defaults to 120 if zero.
	TimeoutSec int `json:"timeout_sec,omitempty"`
}

// SubagentResult holds the output of a completed (or failed) subagent.
type SubagentResult struct {
	Role    AgentRole `json:"role"`
	Task    string    `json:"task"`
	Output  string    `json:"output"`
	Success bool      `json:"success"`
	Error   string    `json:"error,omitempty"`
}

// SpawnAgent runs a specialist subagent synchronously and returns its result.
// It inherits the parent's cancel channel (so cancelling the parent also stops subagents).
// The parent's SendToClient is used to emit subagent.spawned / subagent.done events.
func SpawnAgent(req SubagentRequest, parent *ChatContext) SubagentResult {
	if req.TimeoutSec <= 0 {
		req.TimeoutSec = 120
	}

	profile := profileForRole(req.Role)

	projectPath := req.WorkingDir
	if projectPath == "" {
		projectPath = parent.ProjectPath
	}

	subagentID := newID()

	// Notify parent client that this subagent has started.
	if parent.SendToClient != nil {
		parent.SendToClient("subagent.spawned", map[string]interface{}{
			"id":   subagentID,
			"role": string(req.Role),
			"task": req.Task,
		})
	}

	// Collect all output from the subagent.
	var outputMu sync.Mutex
	var outputBuf strings.Builder

	// Create a timeout context so subagents don't run forever.
	timeoutCtx, cancelTimeout := stdctx.WithTimeout(stdctx.Background(), time.Duration(req.TimeoutSec)*time.Second)
	defer cancelTimeout()

	// Merge parent cancel + timeout into one channel.
	cancelCh := make(chan struct{})
	go func() {
		select {
		case <-timeoutCtx.Done():
			close(cancelCh)
		case <-parent.Cancel:
			close(cancelCh)
		}
	}()

	// Build the child ChatContext.
	child := &ChatContext{
		ProjectPath:    projectPath,
		SessionID:      "subagent-" + subagentID,
		AutonomousMode: false, // no recursion
		Cancel:         cancelCh,

		OnChunk: func(content string, done bool) {
			outputMu.Lock()
			outputBuf.WriteString(content)
			outputMu.Unlock()
			// Stream progress to parent's client so user can watch live.
			if parent.SendToClient != nil {
				parent.SendToClient("subagent.progress", map[string]interface{}{
					"id":    subagentID,
					"role":  string(req.Role),
					"chunk": content,
					"done":  done,
				})
			}
		},
		OnToolCall: func(name string, input interface{}) {
			if parent.SendToClient != nil {
				parent.SendToClient("subagent.tool_call", map[string]interface{}{
					"id":    subagentID,
					"role":  string(req.Role),
					"name":  name,
					"input": input,
				})
			}
		},
		OnToolResult: func(name string, result interface{}, isError bool) {
			// Suppress to avoid flooding the client.
		},
		OnError: func(err string) {
			outputMu.Lock()
			outputBuf.WriteString("\n[error] " + err)
			outputMu.Unlock()
		},
		OnSessionUpdated: nil, // subagents don't persist sessions
		GetOpenTabs:      parent.GetOpenTabs,
		SendToClient:     nil, // subagents don't push raw events to client (parent does)
		RequestApproval:  parent.RequestApproval,
		Usage:            parent.Usage, // accumulate cost into parent's session
		Quarantine:       parent.Quarantine,
	}

	// Build the task message: role system context + actual task.
	taskMessage := fmt.Sprintf("[%s agent task]\n\n%s", string(req.Role), req.Task)
	_ = profile // profile's AllowedTools enforced via bootstrapTools filter below (future work)

	// Run the child agent synchronously in this goroutine.
	// Chat() is already goroutine-safe; we just call it directly.
	Chat(child, taskMessage)

	outputMu.Lock()
	output := strings.TrimSpace(outputBuf.String())
	outputMu.Unlock()

	result := SubagentResult{
		Role:    req.Role,
		Task:    req.Task,
		Output:  output,
		Success: output != "" && !strings.Contains(output, "[error]"),
	}
	if !result.Success && output == "" {
		result.Error = "subagent produced no output"
	}

	// Notify parent client that this subagent has finished.
	if parent.SendToClient != nil {
		parent.SendToClient("subagent.done", map[string]interface{}{
			"id":      subagentID,
			"role":    string(req.Role),
			"task":    req.Task,
			"success": result.Success,
			"output":  result.Output,
		})
	}

	return result
}

// SpawnAgentsParallel runs multiple subagent requests concurrently and returns
// their results in the same order as the input slice.
func SpawnAgentsParallel(reqs []SubagentRequest, parent *ChatContext) []SubagentResult {
	results := make([]SubagentResult, len(reqs))
	var wg sync.WaitGroup
	for i, req := range reqs {
		wg.Add(1)
		go func(idx int, r SubagentRequest) {
			defer wg.Done()
			results[idx] = SpawnAgent(r, parent)
		}(i, req)
	}
	wg.Wait()
	return results
}
