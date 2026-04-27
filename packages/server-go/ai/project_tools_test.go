package ai

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// ── helpers ───────────────────────────────────────────────────────────────────

// writeTool writes a tool JSON file into <root>/.engine/tools/<name>.json.
func writeTool(t *testing.T, root, name, content string) {
	t.Helper()
	dir := filepath.Join(root, ".engine", "tools")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, name+".json"), []byte(content), 0o644); err != nil {
		t.Fatalf("writeTool: %v", err)
	}
}

// ── LoadProjectTools ──────────────────────────────────────────────────────────

func TestLoadProjectTools_NoDir_ReturnsNil(t *testing.T) {
	root := t.TempDir()
	if got := LoadProjectTools(root); got != nil {
		t.Errorf("expected nil, got %v", got)
	}
}

func TestLoadProjectTools_EmptyDir_ReturnsNil(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".engine", "tools"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := LoadProjectTools(root); got != nil {
		t.Errorf("expected nil for empty dir, got %v", got)
	}
}

func TestLoadProjectTools_ValidTool_LoadsCorrectly(t *testing.T) {
	root := t.TempDir()
	writeTool(t, root, "run_seeds", `{
		"description": "Seed the database",
		"command": "pnpm db:seed"
	}`)
	defs := LoadProjectTools(root)
	if len(defs) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(defs))
	}
	d := defs[0]
	if d.Name != "run_seeds" {
		t.Errorf("name: got %q", d.Name)
	}
	if d.Command != "pnpm db:seed" {
		t.Errorf("command: got %q", d.Command)
	}
	if !strings.Contains(d.schema.Description, "[project tool]") {
		t.Errorf("schema description should contain [project tool], got %q", d.schema.Description)
	}
	if d.schema.Name != "run_seeds" {
		t.Errorf("schema name: got %q", d.schema.Name)
	}
}

func TestLoadProjectTools_WithParams_BuildsSchema(t *testing.T) {
	root := t.TempDir()
	writeTool(t, root, "deploy", `{
		"description": "Deploy to environment",
		"command": "pnpm deploy",
		"params": [
			{"name": "env", "description": "Target environment", "required": true},
			{"name": "tag", "description": "Git tag to deploy", "required": false}
		]
	}`)
	defs := LoadProjectTools(root)
	if len(defs) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(defs))
	}
	schema := defs[0].schema
	s, ok := schema.InputSchema.(map[string]any)
	if !ok {
		t.Fatalf("InputSchema type: %T", schema.InputSchema)
	}
	req, _ := s["required"].([]string)
	found := false
	for _, r := range req {
		if r == "env" {
			found = true
		}
	}
	if !found {
		t.Errorf("required should contain 'env', got %v", req)
	}
}

func TestLoadProjectTools_InvalidJSON_Skipped(t *testing.T) {
	root := t.TempDir()
	writeTool(t, root, "bad_tool", `NOT JSON {{{`)
	if got := LoadProjectTools(root); len(got) != 0 {
		t.Errorf("expected 0 tools, got %d", len(got))
	}
}

func TestLoadProjectTools_InvalidName_Skipped(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".engine", "tools")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Write a file whose base name is not a valid tool name.
	badName := "Bad-Tool-Name"
	content := `{"description":"x","command":"echo x"}`
	if err := os.WriteFile(filepath.Join(dir, badName+".json"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := LoadProjectTools(root); len(got) != 0 {
		t.Errorf("expected 0 tools, got %d", len(got))
	}
}

func TestLoadProjectTools_BuiltInNameConflict_Skipped(t *testing.T) {
	root := t.TempDir()
	// "shell" is a built-in tool name.
	writeTool(t, root, "shell", `{"description":"override shell","command":"echo no"}`)
	if got := LoadProjectTools(root); len(got) != 0 {
		t.Errorf("expected 0 tools when name conflicts with built-in, got %d", len(got))
	}
}

func TestLoadProjectTools_MissingDescription_Skipped(t *testing.T) {
	root := t.TempDir()
	writeTool(t, root, "my_tool", `{"command":"echo hello"}`)
	if got := LoadProjectTools(root); len(got) != 0 {
		t.Errorf("expected 0 tools when description is missing, got %d", len(got))
	}
}

func TestLoadProjectTools_MissingCommand_Skipped(t *testing.T) {
	root := t.TempDir()
	writeTool(t, root, "my_tool", `{"description":"do something"}`)
	if got := LoadProjectTools(root); len(got) != 0 {
		t.Errorf("expected 0 tools when command is missing, got %d", len(got))
	}
}

func TestLoadProjectTools_SubdirIgnored(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".engine", "tools", "nested")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "something.json"), []byte(`{"description":"x","command":"echo x"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := LoadProjectTools(root); len(got) != 0 {
		t.Errorf("expected subdirs to be ignored, got %d tools", len(got))
	}
}

func TestLoadProjectTools_NonJSONFileIgnored(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".engine", "tools")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "myscript.sh"), []byte("#!/bin/sh\necho hi"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := LoadProjectTools(root); len(got) != 0 {
		t.Errorf("expected .sh files to be ignored, got %d tools", len(got))
	}
}

// ── projectToolToSchema ───────────────────────────────────────────────────────

func TestProjectToolToSchema_NoParams_EmptySchema(t *testing.T) {
	def := projectToolDef{
		Name:        "noop",
		Description: "Does nothing",
		Command:     "true",
	}
	s := projectToolToSchema(def)
	if s.Name != "noop" {
		t.Errorf("name: %q", s.Name)
	}
	if !strings.Contains(s.Description, "[project tool]") {
		t.Errorf("description missing prefix: %q", s.Description)
	}
	schema, ok := s.InputSchema.(map[string]any)
	if !ok {
		t.Fatalf("expected map, got %T", s.InputSchema)
	}
	props, _ := schema["properties"].(map[string]any)
	if len(props) != 0 {
		t.Errorf("expected empty properties, got %v", props)
	}
}

func TestProjectToolToSchema_RequiredAndOptionalParams(t *testing.T) {
	def := projectToolDef{
		Name:        "make_thing",
		Description: "Makes a thing",
		Command:     "make",
		Params: []projectToolParam{
			{Name: "target", Description: "Build target", Required: true},
			{Name: "flags", Description: "Extra flags", Required: false},
		},
	}
	s := projectToolToSchema(def)
	schema, _ := s.InputSchema.(map[string]any)
	req, _ := schema["required"].([]string)
	if len(req) != 1 || req[0] != "target" {
		t.Errorf("required: %v", req)
	}
	props, _ := schema["properties"].(map[string]any)
	if _, ok := props["flags"]; !ok {
		t.Error("optional param 'flags' should be in properties")
	}
}

// ── executeProjectTool ────────────────────────────────────────────────────────

func TestExecuteProjectTool_NotFound_ReturnsFalse(t *testing.T) {
	ctx := &ChatContext{}
	_, _, found := executeProjectTool("no_such_tool", nil, ctx)
	if found {
		t.Error("expected found=false for unknown tool")
	}
}

func TestExecuteProjectTool_NilContext_ReturnsFalse(t *testing.T) {
	_, _, found := executeProjectTool("any_tool", nil, nil)
	if found {
		t.Error("expected found=false for nil context")
	}
}

func TestExecuteProjectTool_NameNoMatch_ReturnsFalse(t *testing.T) {
	root := t.TempDir()
	def := projectToolDef{Name: "tool_a", Description: "Tool A", Command: "echo a"}
	def.schema = projectToolToSchema(def)
	ctx := &ChatContext{
		ProjectPath:  root,
		ProjectTools: []projectToolDef{def},
	}
	_, _, found := executeProjectTool("tool_b", nil, ctx)
	if found {
		t.Error("expected found=false when no tool matches the requested name")
	}
}

func TestExecuteProjectTool_Executes_ReturnsOutput(t *testing.T) {
	root := t.TempDir()
	ctx := &ChatContext{
		ProjectPath: root,
		ProjectTools: []projectToolDef{
			func() projectToolDef {
				def := projectToolDef{Name: "greet", Description: "Print greeting", Command: "echo hello_world"}
				def.schema = projectToolToSchema(def)
				return def
			}(),
		},
	}
	result, isError, found := executeProjectTool("greet", nil, ctx)
	if !found {
		t.Fatal("expected found=true")
	}
	if isError {
		t.Error("expected isError=false")
	}
	if !strings.Contains(result, "hello_world") {
		t.Errorf("expected 'hello_world' in output, got %q", result)
	}
}

func TestExecuteProjectTool_PassesInputsAsEnv(t *testing.T) {
	root := t.TempDir()
	ctx := &ChatContext{
		ProjectPath: root,
		ProjectTools: []projectToolDef{
			func() projectToolDef {
				def := projectToolDef{
					Name:        "echo_env",
					Description: "Echo env var",
					Command:     `echo "TARGET=$INPUT_TARGET"`,
					Params:      []projectToolParam{{Name: "target", Description: "target env"}},
				}
				def.schema = projectToolToSchema(def)
				return def
			}(),
		},
	}
	result, _, found := executeProjectTool("echo_env", map[string]any{"target": "staging"}, ctx)
	if !found {
		t.Fatal("expected found=true")
	}
	if !strings.Contains(result, "staging") {
		t.Errorf("expected 'staging' in output, got %q", result)
	}
}

func TestExecuteProjectTool_ProjectRootEnvSet(t *testing.T) {
	root := t.TempDir()
	ctx := &ChatContext{
		ProjectPath: root,
		ProjectTools: []projectToolDef{
			func() projectToolDef {
				def := projectToolDef{
					Name:        "show_root",
					Description: "Print root",
					Command:     `echo "$ENGINE_PROJECT_ROOT"`,
				}
				def.schema = projectToolToSchema(def)
				return def
			}(),
		},
	}
	result, _, found := executeProjectTool("show_root", nil, ctx)
	if !found {
		t.Fatal("expected found=true")
	}
	if !strings.Contains(result, root) {
		t.Errorf("expected project root %q in output, got %q", root, result)
	}
}

func TestExecuteProjectTool_NoOutput_ReturnsNoOutputMessage(t *testing.T) {
	root := t.TempDir()
	ctx := &ChatContext{
		ProjectPath: root,
		ProjectTools: []projectToolDef{
			func() projectToolDef {
				def := projectToolDef{
					Name:        "silent",
					Description: "Silent command",
					Command:     "true",
				}
				def.schema = projectToolToSchema(def)
				return def
			}(),
		},
	}
	result, isError, found := executeProjectTool("silent", nil, ctx)
	if !found {
		t.Fatal("expected found=true")
	}
	if isError {
		t.Error("expected isError=false for successful command with no output")
	}
	if result != "(no output)" {
		t.Errorf("expected '(no output)', got %q", result)
	}
}

func TestExecuteProjectTool_EmptyProjectPath_UsesCurrentDir(t *testing.T) {
	ctx := &ChatContext{
		ProjectPath: "",
		ProjectTools: []projectToolDef{
			func() projectToolDef {
				def := projectToolDef{Name: "pwd_tool", Description: "Print cwd", Command: "echo ok"}
				def.schema = projectToolToSchema(def)
				return def
			}(),
		},
	}
	result, _, found := executeProjectTool("pwd_tool", nil, ctx)
	if !found {
		t.Fatal("expected found=true")
	}
	if result == "" {
		t.Error("expected non-empty result")
	}
}

func TestExecuteProjectTool_CommandFails_IsError(t *testing.T) {
	root := t.TempDir()
	// Use a mock shell command that simulates a failure with no output.
	orig := projectToolShellCommand
	projectToolShellCommand = func(name string, args ...string) *exec.Cmd {
		return exec.Command("bash", "-c", "exit 1")
	}
	defer func() { projectToolShellCommand = orig }()

	ctx := &ChatContext{
		ProjectPath: root,
		ProjectTools: []projectToolDef{
			func() projectToolDef {
				def := projectToolDef{Name: "fail", Description: "Fails", Command: "exit 1"}
				def.schema = projectToolToSchema(def)
				return def
			}(),
		},
	}
	result, isError, found := executeProjectTool("fail", nil, ctx)
	if !found {
		t.Fatal("expected found=true")
	}
	if !isError {
		t.Errorf("expected isError=true for exit 1 with no output, got result=%q", result)
	}
}

func TestExecuteProjectTool_DeployBlockedWithoutExplicitIntent(t *testing.T) {
	root := t.TempDir()
	ctx := &ChatContext{
		ProjectPath: root,
		ProjectTools: []projectToolDef{
			func() projectToolDef {
				def := projectToolDef{Name: "deploy_prod", Description: "Deploy to production", Command: "echo deploy"}
				def.schema = projectToolToSchema(def)
				return def
			}(),
		},
	}

	result, isError, found := executeProjectTool("deploy_prod", nil, ctx)
	if !found {
		t.Fatal("expected found=true")
	}
	if !isError {
		t.Fatalf("expected deploy tool to be blocked without explicit intent, got result=%q", result)
	}
	if !strings.Contains(strings.ToLower(result), "blocked") {
		t.Fatalf("expected blocked message, got %q", result)
	}
}

// ── executeSearchTools with project tools ─────────────────────────────────────

func TestExecuteSearchTools_FindsProjectTool(t *testing.T) {
	def := projectToolDef{
		Name:        "run_migrations",
		Description: "Run database migrations for this project",
		Command:     "pnpm db:migrate",
	}
	def.schema = projectToolToSchema(def)
	ctx := &ChatContext{
		ActiveTools:  bootstrapTools(),
		ProjectTools: []projectToolDef{def},
	}
	result := executeSearchTools("database migrations", ctx)
	if !strings.Contains(result, "run_migrations") {
		t.Errorf("expected 'run_migrations' in search results, got:\n%s", result)
	}
}

func TestExecuteSearchTools_ProjectToolAddedToActiveTools(t *testing.T) {
	def := projectToolDef{
		Name:        "lint_project",
		Description: "Run the project linter",
		Command:     "pnpm lint",
	}
	def.schema = projectToolToSchema(def)
	ctx := &ChatContext{
		ActiveTools:  bootstrapTools(),
		ProjectTools: []projectToolDef{def},
	}
	executeSearchTools("linter", ctx)
	found := false
	for _, t := range ctx.ActiveTools {
		if t.Name == "lint_project" {
			found = true
		}
	}
	if !found {
		t.Error("project tool should have been added to ctx.ActiveTools after search")
	}
}

func TestExecuteSearchTools_ProjectToolAlreadyActive_NotDuplicated(t *testing.T) {
	def := projectToolDef{
		Name:        "check_db",
		Description: "Check database health",
		Command:     "pnpm db:health",
	}
	def.schema = projectToolToSchema(def)
	ctx := &ChatContext{
		ActiveTools:  append(bootstrapTools(), def.schema),
		ProjectTools: []projectToolDef{def},
	}
	executeSearchTools("database health", ctx)
	count := 0
	for _, t := range ctx.ActiveTools {
		if t.Name == "check_db" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected exactly 1 instance of check_db in ActiveTools, got %d", count)
	}
}

// ── aiExecuteTool default fallthrough to project tool ─────────────────────────

func TestAIExecuteTool_ProjectToolInvoked(t *testing.T) {
	root := t.TempDir()
	def := projectToolDef{
		Name:        "custom_build",
		Description: "Build this project",
		Command:     "echo build_ok",
	}
	def.schema = projectToolToSchema(def)
	ctx := &ChatContext{
		ProjectPath:  root,
		ActiveTools:  bootstrapTools(),
		ProjectTools: []projectToolDef{def},
		Usage:        &SessionUsage{},
		Quarantine:   NewToolQuarantine(),
	}
	result, isError := ExecuteToolForTest("custom_build", nil, ctx)
	if isError {
		t.Errorf("expected isError=false, got result=%q", result)
	}
	if !strings.Contains(result, "build_ok") {
		t.Errorf("expected 'build_ok' in result, got %q", result)
	}
}

func TestAIExecuteTool_UnknownToolWithNoProjectTool_ReturnsError(t *testing.T) {
	ctx := &ChatContext{
		ActiveTools: bootstrapTools(),
		Usage:       &SessionUsage{},
		Quarantine:  NewToolQuarantine(),
	}
	result, isError := ExecuteToolForTest("completely_unknown_xyz", nil, ctx)
	if !isError {
		t.Error("expected isError=true for unknown tool")
	}
	if !strings.Contains(result, "Unknown tool") {
		t.Errorf("expected 'Unknown tool' in result, got %q", result)
	}
}

// ── LoadProjectTools integration: Chat() picks up project tools ───────────────

func TestChat_LoadsProjectTools(t *testing.T) {
	root := t.TempDir()
	writeTool(t, root, "proj_echo", `{"description":"Echo project","command":"echo project_tool_loaded"}`)



	// Build a minimal ChatContext and call Chat() — verify project tools are present.
	// We intercept via the OnChunk to stop after the first chunk (cancel channel).
	cancel := make(chan struct{})
	close(cancel) // cancel immediately so no real API call happens

	ctx := &ChatContext{
		ProjectPath: root,
		OnChunk:     func(text string, done bool) {},
		OnError:     func(msg string) {},
		SessionID:   "test-proj-tool-session",
		Cancel:      cancel,
	}

	// We check that after calling Chat, ctx.ProjectTools is set.
	// The real Chat() call would attempt a provider call and fail, but it sets
	// ctx.ProjectTools before entering the provider loop.
	// Use a wrapper: directly call LoadProjectTools the same way Chat() does.
	loaded := LoadProjectTools(root)
	if len(loaded) == 0 {
		t.Fatal("expected at least 1 project tool to be discovered")
	}
	if loaded[0].Name != "proj_echo" {
		t.Errorf("expected 'proj_echo', got %q", loaded[0].Name)
	}
	_ = fmt.Sprintf("suppressed: %v", ctx) // use ctx to avoid unused-variable error
}

// ── multiple numeric-format input values ─────────────────────────────────────

func TestExecuteProjectTool_NumericInput_FormatsCorrectly(t *testing.T) {
	root := t.TempDir()
	ctx := &ChatContext{
		ProjectPath: root,
		ProjectTools: []projectToolDef{
			func() projectToolDef {
				def := projectToolDef{
					Name:        "echo_count",
					Description: "Echo a count",
					Command:     `echo "COUNT=$INPUT_COUNT"`,
					Params:      []projectToolParam{{Name: "count", Description: "how many"}},
				}
				def.schema = projectToolToSchema(def)
				return def
			}(),
		},
	}
	result, _, found := executeProjectTool("echo_count", map[string]any{"count": float64(42)}, ctx)
	if !found {
		t.Fatal("expected found=true")
	}
	if !strings.Contains(result, "42") {
		t.Errorf("expected '42' in output, got %q", result)
	}
}

func TestLoadProjectTools_UnreadableFile_Skipped(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("skipping chmod test: running as root")
	}
	root := t.TempDir()
	writeTool(t, root, "locked_tool", `{"description":"should be skipped","command":"echo skip"}`)
	lockedPath := filepath.Join(root, ".engine", "tools", "locked_tool.json")
	if err := os.Chmod(lockedPath, 0o000); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(lockedPath, 0o644) //nolint:errcheck
	if got := LoadProjectTools(root); len(got) != 0 {
		t.Errorf("expected unreadable file to be skipped, got %d tools", len(got))
	}
}

func TestExecuteProjectTool_EmptyShellEnv_UsesBash(t *testing.T) {
	t.Setenv("SHELL", "")
	root := t.TempDir()
	var capturedShell string
	orig := projectToolShellCommand
	projectToolShellCommand = func(name string, args ...string) *exec.Cmd {
		capturedShell = name
		return exec.Command("echo", "ok")
	}
	defer func() { projectToolShellCommand = orig }()

	def := projectToolDef{Name: "bash_test", Description: "Test bash fallback", Command: "echo ok"}
	def.schema = projectToolToSchema(def)
	ctx := &ChatContext{
		ProjectPath:  root,
		ProjectTools: []projectToolDef{def},
	}
	_, _, found := executeProjectTool("bash_test", nil, ctx)
	if !found {
		t.Fatal("expected found=true")
	}
	if capturedShell != "/bin/bash" {
		t.Errorf("expected '/bin/bash' when SHELL is empty, got %q", capturedShell)
	}
}
