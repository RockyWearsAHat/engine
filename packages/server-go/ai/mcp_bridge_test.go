package ai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// ── routing rule ────────────────────────────────────────────────────────────

func setQuotaConcurrencyForTest(t *testing.T, n int) {
	t.Helper()
	t.Setenv("ENGINE_QUOTA", "0")
	t.Setenv("ENGINE_QUOTA_MAX_CONCURRENCY", fmt.Sprint(n))
	t.Setenv(envEventOrchestrator, "")
	resetQuotaGateForTest()
	t.Cleanup(resetQuotaGateForTest)
}

// Task mode + wide plan + room → parallel. Old code said "task mode: serial".
func TestShouldRunEventOrchestrator_TaskModeWidePlanParallel(t *testing.T) {
	setQuotaConcurrencyForTest(t, 4)
	ok, why := ShouldRunEventOrchestrator(context.Background(), OrchestratorConfig{
		ProjectPath: t.TempDir(), TaskMode: true, TaskID: "rui-1", PlanSteps: 5,
	})
	if !ok {
		t.Fatalf("task mode, 5 steps, concurrency 4 must be parallel; got serial: %s", why)
	}
	if !strings.Contains(why, "comms on") {
		t.Errorf("reason should say comms on; got %q", why)
	}
}

// Room alone is enough: plan size unknown, concurrency 4 → parallel.
func TestShouldRunEventOrchestrator_TaskModeUnknownPlanWithRoom(t *testing.T) {
	setQuotaConcurrencyForTest(t, 4)
	ok, _ := ShouldRunEventOrchestrator(context.Background(), OrchestratorConfig{TaskMode: true, TaskID: "x"})
	if !ok {
		t.Fatal("concurrency 4 with unknown plan must be parallel")
	}
}

// Serial only when small AND no room.
func TestShouldRunEventOrchestrator_SmallPlanNoRoomSerial(t *testing.T) {
	setQuotaConcurrencyForTest(t, 1)
	ok, why := ShouldRunEventOrchestrator(context.Background(), OrchestratorConfig{TaskMode: true, PlanSteps: 2})
	if ok {
		t.Fatalf("2 steps + concurrency 1 must be serial; got parallel: %s", why)
	}
	// Wide plan, no room: still event path (cap narrows to 1 team, comms on).
	ok, _ = ShouldRunEventOrchestrator(context.Background(), OrchestratorConfig{TaskMode: true, PlanSteps: 5})
	if !ok {
		t.Fatal("5 steps + concurrency 1 must still be parallel (comms on)")
	}
}

// ── brain namespacing ───────────────────────────────────────────────────────

func TestBrainStatePath_TaskSlugNamespaced(t *testing.T) {
	dir := t.TempDir()
	b, _ := NewOrchestrationBrainSlug(dir, "o", "r", "brief", "sess", "rui-1")
	if err := b.UpdatePlan([]PlanStep{{Title: "a"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".engine", "brain-rui-1.json")); err != nil {
		t.Fatalf("namespaced brain missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".engine", "brain.json")); err == nil {
		t.Fatal("task brain must not write brain.json")
	}
	again, _ := NewOrchestrationBrainSlug(dir, "o", "r", "brief", "sess", "rui-1")
	if len(again.GetPlan()) != 1 {
		t.Fatal("namespaced brain did not reload")
	}
}

// ── bridge wire ─────────────────────────────────────────────────────────────

func rpc(t *testing.T, w io.Writer, r *bufio.Reader, id int, method string, params any) map[string]any {
	t.Helper()
	req := map[string]any{"jsonrpc": "2.0", "id": id, "method": method}
	if params != nil {
		req["params"] = params
	}
	b, _ := json.Marshal(req)
	if _, err := w.Write(append(b, '\n')); err != nil {
		t.Fatal(err)
	}
	line, err := r.ReadBytes('\n')
	if err != nil {
		t.Fatalf("read reply for %s: %v", method, err)
	}
	var resp map[string]any
	if err := json.Unmarshal(line, &resp); err != nil {
		t.Fatalf("bad reply %q: %v", line, err)
	}
	return resp
}

func TestMCPBridge_ToolsListHasAgentSend(t *testing.T) {
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	done := make(chan error, 1)
	go func() { done <- ServeMCPBridge(inR, outW, MCPBridgeIdentity{Project: t.TempDir(), Agent: "w1"}) }()
	rd := bufio.NewReader(outR)

	init := rpc(t, inW, rd, 1, "initialize", map[string]any{"protocolVersion": "2024-11-05"})
	if init["result"] == nil {
		t.Fatalf("initialize: %v", init)
	}
	// notification: no reply expected
	_, _ = inW.Write([]byte(`{"jsonrpc":"2.0","method":"notifications/initialized"}` + "\n"))

	list := rpc(t, inW, rd, 2, "tools/list", nil)
	names := []string{}
	for _, tl := range list["result"].(map[string]any)["tools"].([]any) {
		names = append(names, tl.(map[string]any)["name"].(string))
	}
	joined := strings.Join(names, ",")
	for _, want := range mcpBridgeToolNames {
		if !strings.Contains(joined, want) {
			t.Errorf("tools/list missing %s; got %s", want, joined)
		}
	}
	inW.Close()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

// In-process addr="" path: tools/call agent_send lands in the hub.
func TestMCPBridge_ToolsCallAgentSend_InProcess(t *testing.T) {
	project := t.TempDir()
	hub := AgentCommsForProject(project)
	hub.Register("lead", "orchestrator", "running")

	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	go ServeMCPBridge(inR, outW, MCPBridgeIdentity{Project: project, Agent: "w1", Role: "builder"}) //nolint:errcheck
	rd := bufio.NewReader(outR)

	resp := rpc(t, inW, rd, 1, "tools/call", map[string]any{
		"name": "agent_send", "arguments": map[string]any{"to": "lead", "subject": "s", "body": "hi lead"},
	})
	res := resp["result"].(map[string]any)
	if res["isError"] == true {
		t.Fatalf("agent_send errored: %v", res)
	}
	inbox := hub.Inbox("lead", false)
	if len(inbox) != 1 || inbox[0].From != "w1" || inbox[0].Body != "hi lead" {
		t.Fatalf("hub inbox = %+v", inbox)
	}
	// Worker self-registers with its role.
	found := false
	for _, p := range hub.List() {
		if p.ID == "w1" && p.Role == "builder" {
			found = true
		}
	}
	if !found {
		t.Errorf("w1 not in hub: %+v", hub.List())
	}
	// Non-exposed tool refused.
	resp = rpc(t, inW, rd, 2, "tools/call", map[string]any{"name": "shell", "arguments": map[string]any{}})
	if resp["result"].(map[string]any)["isError"] != true {
		t.Error("shell must not be callable over the bridge")
	}
	inW.Close()
}

// ── launch args ─────────────────────────────────────────────────────────────

func TestBuildClaudeArgs_MCPConfig(t *testing.T) {
	t.Setenv(envMCPAddr, "http://127.0.0.1:9")
	ctx := &ChatContext{ProjectPath: "/tmp/proj", AgentName: "team-db", Role: RoleAutonomousBuilder}
	args := buildClaudeArgs(ctx, "claude-haiku-4-5", "sys", 2)
	path := mcpConfigPathFromArgs(args)
	if path == "" {
		t.Fatalf("no --mcp-config in %q", args)
	}
	defer os.Remove(path)
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--allowedTools mcp__engine__*") {
		t.Errorf("missing allowedTools; got %q", joined)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		Servers map[string]struct {
			Command string            `json:"command"`
			Args    []string          `json:"args"`
			Env     map[string]string `json:"env"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	srv, ok := cfg.Servers[MCPBridgeServerName]
	if !ok {
		t.Fatalf("no %q server in %s", MCPBridgeServerName, raw)
	}
	if srv.Env[envMCPAgent] != "team-db" || srv.Env[envMCPProject] != "/tmp/proj" || srv.Env[envMCPAddr] != "http://127.0.0.1:9" {
		t.Errorf("env = %v", srv.Env)
	}
	if srv.Command == "" || len(srv.Args) == 0 {
		t.Errorf("command/args empty: %+v", srv)
	}
}

// fanout 0 → no Task. fanout > 0 → Task allowed. Unchanged rule, restated
// next to the mcp args so a regression here is caught with the bridge.
func TestBuildClaudeArgs_DisallowTaskOnlyAtZeroFanout(t *testing.T) {
	ctx := &ChatContext{ProjectPath: "/tmp/proj"}
	for fan, want := range map[int]bool{0: true, 1: false, 3: false, -1: false} {
		args := buildClaudeArgs(ctx, "claude-haiku-4-5", "sys", fan)
		if p := mcpConfigPathFromArgs(args); p != "" {
			os.Remove(p)
		}
		got := strings.Contains(strings.Join(args, " "), "--disallowedTools Task")
		if got != want {
			t.Errorf("fanout %d: disallow Task = %v, want %v", fan, got, want)
		}
	}
}

// ── end to end: fake claude → bridge → HTTP → hub ───────────────────────────
//
// Helper-process pattern. The test binary plays three parts:
//   - test: builds args, runs stub, owns the hub + HTTP callback.
//   - fake claude (TestHelperFakeClaude): parses --mcp-config, spawns the bridge
//     command from it, calls tools/call agent_send, prints a stream-json result.
//   - bridge (TestHelperMCPBridge): ServeMCPBridge on stdio, identity from env.

const envTestHelper = "ENGINE_TEST_HELPER"

func TestHelperMCPBridge(t *testing.T) {
	if os.Getenv(envTestHelper) != "bridge" {
		t.Skip("helper process")
	}
	if err := ServeMCPBridge(os.Stdin, os.Stdout, MCPBridgeIdentityFromEnv(os.Getenv)); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(0)
}

func TestHelperFakeClaude(t *testing.T) {
	if os.Getenv(envTestHelper) != "fakeclaude" {
		t.Skip("helper process")
	}
	fail := func(msg string) { fmt.Fprintln(os.Stderr, "fake claude:", msg); os.Exit(2) }
	path := mcpConfigPathFromArgs(os.Args)
	if path == "" {
		fail("no --mcp-config")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		fail(err.Error())
	}
	var cfg struct {
		Servers map[string]struct {
			Command string            `json:"command"`
			Args    []string          `json:"args"`
			Env     map[string]string `json:"env"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		fail(err.Error())
	}
	srv := cfg.Servers[MCPBridgeServerName]
	cmd := exec.Command(srv.Command, srv.Args...)
	cmd.Env = append(os.Environ(), envTestHelper+"=bridge")
	for k, v := range srv.Env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	stdin, _ := cmd.StdinPipe()
	stdout, _ := cmd.StdoutPipe()
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		fail(err.Error())
	}
	rd := bufio.NewReader(stdout)
	send := func(id int, method string, params any) map[string]any {
		b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
		stdin.Write(append(b, '\n'))
		line, err := rd.ReadBytes('\n')
		if err != nil {
			fail("bridge reply: " + err.Error())
		}
		var m map[string]any
		json.Unmarshal(line, &m)
		return m
	}
	send(1, "initialize", map[string]any{})
	res := send(2, "tools/call", map[string]any{
		"name": "agent_send", "arguments": map[string]any{"to": "lead", "body": "stub says hi"},
	})
	stdin.Close()
	cmd.Wait()
	text, _ := json.Marshal(res)
	fmt.Printf(`{"type":"result","subtype":"success","result":%q}`+"\n", string(text))
	os.Exit(0)
}

func TestFakeClaudeStub_AgentSendLandsInHub(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sh stub")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	project := t.TempDir()
	hub := AgentCommsForProject(project)
	hub.Register("lead", "orchestrator", "running")

	// Engine side: HTTP callback the bridge hits.
	srv := httptest.NewServer(http.HandlerFunc(MCPBridgeHTTPHandler))
	defer srv.Close()
	t.Setenv(envMCPAddr, srv.URL)
	t.Setenv(envMCPBridgeCmd, exe+" -test.run ^TestHelperMCPBridge$")

	// Stub claude: shell → test binary as fake claude.
	stub := filepath.Join(t.TempDir(), "claude")
	script := "#!/bin/sh\n" + envTestHelper + "=fakeclaude exec " + exe + " -test.run '^TestHelperFakeClaude$' -- \"$@\"\n"
	if err := os.WriteFile(stub, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}

	ctx := &ChatContext{ProjectPath: project, AgentName: "team-api", Role: RoleAutonomousBuilder}
	registerChatAgent(ctx)
	args := buildClaudeArgs(ctx, "claude-haiku-4-5", "sys", 0)
	path := mcpConfigPathFromArgs(args)
	if path == "" {
		t.Fatal("no --mcp-config")
	}
	defer os.Remove(path)

	cmd := exec.Command(stub, args...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	cmd.Stdin = strings.NewReader("do the thing")
	if err := cmd.Run(); err != nil {
		t.Fatalf("stub failed: %v\nstdout=%s\nstderr=%s", err, out.String(), errb.String())
	}
	if !strings.Contains(out.String(), `"type":"result"`) {
		t.Fatalf("stub output not stream-json: %s", out.String())
	}
	inbox := hub.Inbox("lead", false)
	if len(inbox) != 1 || inbox[0].From != "team-api" || inbox[0].Body != "stub says hi" {
		t.Fatalf("hub inbox = %+v (stderr=%s)", inbox, errb.String())
	}
	// Worker visible to peers.
	seen := false
	for _, p := range hub.List() {
		if p.ID == "team-api" {
			seen = true
		}
	}
	if !seen {
		t.Errorf("team-api missing from hub: %+v", hub.List())
	}
}

// ── status ownership ────────────────────────────────────────────────────────

// Bridge self-registers an unknown worker, but never stomps a known peer's
// status — the dispatcher owns that.
func TestExecuteBridgeTool_DoesNotClobberKnownStatus(t *testing.T) {
	project := t.TempDir()
	hub := AgentCommsForProject(project)
	hub.Register("lead", "orchestrator", "running")
	hub.Register("w1", "builder", "done")

	resp := ExecuteBridgeTool(BridgeToolRequest{Project: project, Agent: "w1", Role: "builder", Name: "agent_list"})
	if resp.IsError {
		t.Fatalf("agent_list errored: %s", resp.Result)
	}
	for _, p := range hub.List() {
		if p.ID == "w1" && p.Status != "done" {
			t.Fatalf("w1 status clobbered to %q", p.Status)
		}
	}

	// Unknown worker: self-heal registers it active.
	resp = ExecuteBridgeTool(BridgeToolRequest{Project: project, Agent: "w2", Name: "agent_list"})
	if resp.IsError {
		t.Fatalf("agent_list errored: %s", resp.Result)
	}
	if !hub.IsRegistered("w2") {
		t.Fatal("w2 must self-register")
	}
	for _, p := range hub.List() {
		if p.ID == "w2" && (p.Status != "active" || p.Role != "worker") {
			t.Fatalf("w2 = %+v", p)
		}
	}
}
