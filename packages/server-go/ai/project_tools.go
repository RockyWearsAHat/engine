package ai

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// projectToolsDir is the path relative to the project root where tool definitions live.
const projectToolsDir = ".engine/tools"

// validToolName matches names that are safe to register: lowercase, digits, underscores.
var validToolName = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

// projectToolParam describes one input parameter for a project-defined tool.
type projectToolParam struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Required    bool   `json:"required,omitempty"`
}

// projectToolDef is the parsed on-disk definition of a project-local tool.
// Name is derived from the filename; schema is pre-built at load time.
type projectToolDef struct {
	Name        string
	Description string             `json:"description"`
	Command     string             `json:"command"`
	Params      []projectToolParam `json:"params,omitempty"`
	schema      anthropicTool
}

// projectToolToSchema builds the anthropicTool schema for a project tool definition.
// The description is prefixed with "[project tool]" so the AI can tell it apart from built-ins.
func projectToolToSchema(def projectToolDef) anthropicTool {
	props := make(map[string]any)
	required := []string{}
	for _, p := range def.Params {
		props[p.Name] = strProp(p.Description)
		if p.Required {
			required = append(required, p.Name)
		}
	}
	var schema any
	if len(props) > 0 {
		schema = objSchema(required, props)
	} else {
		schema = objSchema([]string{}, map[string]any{})
	}
	return anthropicTool{
		Name:        def.Name,
		Description: fmt.Sprintf("[project tool] %s", def.Description),
		InputSchema: schema,
	}
}

// LoadProjectTools discovers and parses all tool definitions under
// <projectRoot>/.engine/tools/*.json. Files that are not valid JSON, have
// missing required fields, invalid names, or names that collide with a built-in
// tool are skipped without error so a bad definition never breaks a session.
func LoadProjectTools(projectRoot string) []projectToolDef {
	dir := filepath.Join(projectRoot, projectToolsDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}

	var defs []projectToolDef
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".json")
		if !validToolName.MatchString(name) {
			continue
		}
		if _, conflict := toolRegistryIndex[name]; conflict {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var def projectToolDef
		if err := json.Unmarshal(data, &def); err != nil {
			continue
		}
		if strings.TrimSpace(def.Description) == "" || strings.TrimSpace(def.Command) == "" {
			continue
		}
		def.Name = name
		def.schema = projectToolToSchema(def)
		defs = append(defs, def)
	}
	return defs
}

// projectToolShellCommand is overridable in tests.
var projectToolShellCommand = func(name string, args ...string) *exec.Cmd {
	return exec.Command(name, args...)
}

// executeProjectTool runs the command for a project-defined tool by name.
// Inputs are passed as INPUT_<NAME>=<value> environment variables so callers
// cannot inject arbitrary shell syntax through parameter values.
// Returns (result, isError, found). found=false when no project tool matches name.
func executeProjectTool(name string, input map[string]any, ctx *ChatContext) (string, bool, bool) {
	if ctx == nil {
		return "", false, false
	}
	for _, def := range ctx.ProjectTools {
		if def.Name != name {
			continue
		}
		if intentErr := ValidatePublishIntentForAction(ctx.ProjectPath, def.Name+" "+def.Description+" "+def.Command); intentErr != nil {
			return intentErr.Error(), true, true
		}
		env := os.Environ()
		for k, v := range input {
			env = append(env, "INPUT_"+strings.ToUpper(k)+"="+fmt.Sprintf("%v", v))
		}
		if ctx.ProjectPath != "" {
			env = append(env, "ENGINE_PROJECT_ROOT="+ctx.ProjectPath)
		}
		shell := os.Getenv("SHELL")
		if shell == "" {
			shell = "/bin/bash"
		}
		cwd := ctx.ProjectPath
		if cwd == "" {
			cwd = "."
		}
		cmd := projectToolShellCommand(shell, "-l", "-c", def.Command)
		cmd.Dir = cwd
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		result := strings.TrimSpace(string(out))
		if result == "" {
			result = "(no output)"
		}
		return result, err != nil && len(out) == 0, true
	}
	return "", false, false
}
