package ai

import (
	stdctx "context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/engine/server/mesh"
)

type fakeMeshClient struct {
	resp *mesh.ExecResponse
	err  error
}

func (f *fakeMeshClient) Exec(ctx stdctx.Context, peer *mesh.Peer, req mesh.ExecRequest) (*mesh.ExecResponse, error) {
	if f.err != nil {
		return nil, f.err
	}
	if f.resp != nil {
		return f.resp, nil
	}
	return &mesh.ExecResponse{}, nil
}

func TestStringOf_Present(t *testing.T) {
	m := map[string]any{"key": "val"}
	if got := stringOf(m, "key"); got != "val" {
		t.Errorf("got %q, want %q", got, "val")
	}
}

func TestStringOf_Missing(t *testing.T) {
	m := map[string]any{}
	if got := stringOf(m, "missing"); got != "" {
		t.Errorf("expected empty for missing key, got %q", got)
	}
}

func TestStringOf_WrongType(t *testing.T) {
	m := map[string]any{"num": 42}
	if got := stringOf(m, "num"); got != "" {
		t.Errorf("expected empty for wrong type, got %q", got)
	}
}

func TestStringSliceOf_Present(t *testing.T) {
	m := map[string]any{"list": []any{"a", "b", "c"}}
	got := stringSliceOf(m, "list")
	if len(got) != 3 || got[0] != "a" || got[1] != "b" || got[2] != "c" {
		t.Errorf("got %v", got)
	}
}

func TestStringSliceOf_Missing(t *testing.T) {
	m := map[string]any{}
	if got := stringSliceOf(m, "missing"); got != nil {
		t.Errorf("expected nil for missing key, got %v", got)
	}
}

func TestStringSliceOf_WrongType(t *testing.T) {
	m := map[string]any{"key": "not-a-slice"}
	if got := stringSliceOf(m, "key"); got != nil {
		t.Errorf("expected nil for wrong type, got %v", got)
	}
}

func TestStringSliceOf_FiltersBlanks(t *testing.T) {
	m := map[string]any{"list": []any{"a", "  ", "b"}}
	got := stringSliceOf(m, "list")
	// blanks are filtered per implementation
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Errorf("got %v, expected [a b]", got)
	}
}

func TestExecuteMeshExec_LoadConfigError(t *testing.T) {
	orig := meshLoadConfigFn
	defer func() { meshLoadConfigFn = orig }()
	meshLoadConfigFn = func(path string) (*mesh.Config, error) {
		return nil, fmt.Errorf("no config")
	}
	input := map[string]any{"command": "ls"}
	result, isErr := executeMeshExec(input, &ChatContext{})
	if !isErr {
		t.Error("expected error flag when config fails")
	}
	if result == "" {
		t.Error("expected non-empty error message")
	}
}

func TestExecuteMeshExec_NoPeers(t *testing.T) {
	orig := meshLoadConfigFn
	defer func() { meshLoadConfigFn = orig }()
	meshLoadConfigFn = func(path string) (*mesh.Config, error) {
		return &mesh.Config{Peers: []mesh.Peer{}}, nil
	}
	input := map[string]any{"command": "ls"}
	result, isErr := executeMeshExec(input, &ChatContext{})
	if !isErr {
		t.Error("expected error flag for no peers")
	}
	if result == "" {
		t.Error("expected non-empty message")
	}
}

func TestExecuteMeshExec_NoCommand(t *testing.T) {
	orig := meshLoadConfigFn
	origFactory := meshClientFactory
	defer func() {
		meshLoadConfigFn = orig
		meshClientFactory = origFactory
	}()
	meshLoadConfigFn = func(path string) (*mesh.Config, error) {
		return &mesh.Config{SelfName: "self", Peers: []mesh.Peer{{Name: "box", Address: "localhost:9091"}}}, nil
	}
	input := map[string]any{}
	result, isErr := executeMeshExec(input, &ChatContext{})
	if !isErr {
		t.Error("expected error flag for missing command")
	}
	if result == "" {
		t.Error("expected non-empty message")
	}
}

func TestExecuteMeshExec_Success_DefaultFirstPeer(t *testing.T) {
	origLoad := meshLoadConfigFn
	origFactory := meshClientFactory
	defer func() {
		meshLoadConfigFn = origLoad
		meshClientFactory = origFactory
	}()

	meshLoadConfigFn = func(path string) (*mesh.Config, error) {
		return &mesh.Config{
			SelfName: "self",
			Peers: []mesh.Peer{
				{Name: "first-peer", Address: "localhost:24445"},
				{Name: "second-peer", Address: "localhost:24446"},
			},
		}, nil
	}
	meshClientFactory = func(selfName string) meshClient {
		return &fakeMeshClient{resp: &mesh.ExecResponse{
			ExitCode:   0,
			DurationMs: 42,
			Stdout:     "ok",
		}}
	}

	input := map[string]any{
		"command": "echo",
		"args":    []any{"hello"},
	}
	out, isErr := executeMeshExec(input, &ChatContext{})
	if isErr {
		t.Fatalf("expected success, got error output: %s", out)
	}

	var payload map[string]any
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("output was not valid json: %v", err)
	}
	if gotPeer, _ := payload["peer"].(string); gotPeer != "first-peer" {
		t.Fatalf("expected default first peer, got %q", gotPeer)
	}
	if gotStdout, _ := payload["stdout"].(string); gotStdout != "ok" {
		t.Fatalf("expected stdout ok, got %q", gotStdout)
	}
}

func TestExecuteMeshExec_PeerByRole_AndExitError(t *testing.T) {
	origLoad := meshLoadConfigFn
	origFactory := meshClientFactory
	defer func() {
		meshLoadConfigFn = origLoad
		meshClientFactory = origFactory
	}()

	meshLoadConfigFn = func(path string) (*mesh.Config, error) {
		return &mesh.Config{
			SelfName: "self",
			Peers: []mesh.Peer{
				{Name: "tests-peer", Address: "localhost:24445", Roles: []string{"tests"}},
				{Name: "inference-peer", Address: "localhost:24446", Roles: []string{"inference"}},
			},
		}, nil
	}
	meshClientFactory = func(selfName string) meshClient {
		return &fakeMeshClient{resp: &mesh.ExecResponse{
			ExitCode:   2,
			DurationMs: 7,
			Stderr:     "boom",
			Error:      "command failed",
		}}
	}

	input := map[string]any{
		"role":    "inference",
		"command": "false",
	}
	out, isErr := executeMeshExec(input, &ChatContext{})
	if !isErr {
		t.Fatalf("expected error for non-zero exit, got output: %s", out)
	}

	var payload map[string]any
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("output was not valid json: %v", err)
	}
	if gotPeer, _ := payload["peer"].(string); gotPeer != "inference-peer" {
		t.Fatalf("expected role-selected peer, got %q", gotPeer)
	}
}


