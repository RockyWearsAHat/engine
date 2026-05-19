package ai

import (
	stdctx "context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/engine/server/mesh"
)

// meshLoadConfigFn and meshClientFactory are injectable for tests so the
// mesh_exec tool can be exercised without an actual peer.
var (
	meshLoadConfigFn   = mesh.LoadConfig
	meshClientFactory  = func(selfName string) meshClient { return mesh.NewClient(selfName) }
)

// meshClient is the narrow interface the tool consumes — keeps the test
// substitution surface small.
type meshClient interface {
	Exec(ctx stdctx.Context, peer *mesh.Peer, req mesh.ExecRequest) (*mesh.ExecResponse, error)
}

// executeMeshExec is the dispatcher for the mesh_exec AI tool. The model
// passes a peer name (or a role to pick by), command + args, and an optional
// timeout. The signed request is delivered to the configured peer and the
// captured stdout/stderr is returned to the model verbatim.
func executeMeshExec(input map[string]any, _ *ChatContext) (string, bool) {
	cfg, err := meshLoadConfigFn("")
	if err != nil {
		return fmt.Sprintf("mesh_exec: load config: %v", err), true
	}
	if cfg == nil || len(cfg.Peers) == 0 {
		return "mesh_exec: no peers configured in ~/.engine/mesh.json — pair a machine first", true
	}

	peerArg, _ := input["peer"].(string)
	roleArg, _ := input["role"].(string)
	var peer *mesh.Peer
	if strings.TrimSpace(peerArg) != "" {
		peer = cfg.FindPeer(peerArg)
	}
	if peer == nil && strings.TrimSpace(roleArg) != "" {
		peer = cfg.PeerWithRole(roleArg)
	}
	if peer == nil {
		// Fall back to the first peer so 'just dispatch this' works even
		// without an explicit name or role.
		peer = &cfg.Peers[0]
	}

	command, _ := input["command"].(string)
	if strings.TrimSpace(command) == "" {
		return "mesh_exec: command is required", true
	}

	req := mesh.ExecRequest{
		Cwd:     stringOf(input, "cwd"),
		Command: command,
		Args:    stringSliceOf(input, "args"),
		Env:     stringSliceOf(input, "env"),
	}
	if t, ok := input["timeoutMs"].(float64); ok && t > 0 {
		req.TimeoutMs = int(t)
	}

	timeout := time.Duration(req.TimeoutMs) * time.Millisecond
	if timeout <= 0 {
		timeout = 10 * time.Minute
	}
	dispatchCtx, cancel := stdctx.WithTimeout(stdctx.Background(), timeout+30*time.Second)
	defer cancel()

	client := meshClientFactory(cfg.SelfName)
	resp, err := client.Exec(dispatchCtx, peer, req)
	if err != nil {
		return fmt.Sprintf("mesh_exec: dispatch to %s failed: %v", peer.Name, err), true
	}

	out, _ := json.MarshalIndent(map[string]any{
		"peer":       peer.Name,
		"exitCode":   resp.ExitCode,
		"durationMs": resp.DurationMs,
		"stdout":     resp.Stdout,
		"stderr":     resp.Stderr,
		"error":      resp.Error,
	}, "", "  ")
	isError := resp.ExitCode != 0 || strings.TrimSpace(resp.Error) != ""
	return string(out), isError
}

func stringOf(input map[string]any, key string) string {
	v, _ := input[key].(string)
	return v
}

func stringSliceOf(input map[string]any, key string) []string {
	raw, ok := input[key].([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
			out = append(out, s)
		}
	}
	return out
}
