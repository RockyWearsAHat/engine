package workspace

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

type Task struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Command     string `json:"command"`
	Kind        string `json:"kind"`
	Source      string `json:"source"`
	Description string `json:"description,omitempty"`
}

type TaskSet struct {
	Tasks            []Task `json:"tasks"`
	DefaultBuildTask string `json:"defaultBuildTaskId,omitempty"`
	DefaultRunTask   string `json:"defaultRunTaskId,omitempty"`
}

type packageJSON struct {
	Scripts map[string]string `json:"scripts"`
}

func DetectTasks(root string) TaskSet {
	if root == "" {
		return TaskSet{Tasks: []Task{}}
	}

	if pkgTasks := detectPackageJSONTasks(root); len(pkgTasks.Tasks) > 0 {
		return pkgTasks
	}
	if cargoTasks := detectCargoTasks(root); len(cargoTasks.Tasks) > 0 {
		return cargoTasks
	}
	if goTasks := detectGoTasks(root); len(goTasks.Tasks) > 0 {
		return goTasks
	}
	return TaskSet{Tasks: []Task{}}
}

func detectPackageJSONTasks(root string) TaskSet {
	path := filepath.Join(root, "package.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return TaskSet{Tasks: []Task{}}
	}

	var pkg packageJSON
	if err := json.Unmarshal(data, &pkg); err != nil || len(pkg.Scripts) == 0 {
		return TaskSet{Tasks: []Task{}}
	}

	packageManager := detectPackageManager(root)
	tasks := make([]Task, 0, 8)
	seen := make(map[string]bool)
	addScript := func(name, kind, label, description string) {
		script, ok := pkg.Scripts[name]
		if !ok || strings.TrimSpace(script) == "" || seen[name] {
			return
		}
		seen[name] = true
		tasks = append(tasks, Task{
			ID:          "script:" + name,
			Label:       label,
			Command:     packageManagerCommand(packageManager, name),
			Kind:        kind,
			Source:      "package-json",
			Description: description,
		})
	}

	buildPriority := []struct {
		Name        string
		Label       string
		Description string
	}{
		{"build", "Build workspace", "Run the primary project build."},
		{"build:desktop-debug", "Build desktop shell", "Build the desktop shell in debug mode."},
		{"build:tauri", "Build desktop bundle", "Build the packaged desktop application."},
	}
	runPriority := []struct {
		Name        string
		Label       string
		Description string
	}{
		{"dev:desktop", "Run desktop app", "Start the desktop editor with the current workspace."},
		{"dev:tauri", "Run Tauri shell", "Launch the desktop shell against the active frontend."},
		{"dev", "Run development app", "Start the workspace development flow."},
		{"start", "Run app", "Start the workspace application."},
		{"preview", "Preview app", "Preview the production client locally."},
	}
	checkPriority := []struct {
		Name        string
		Label       string
		Description string
	}{
		{"typecheck", "Typecheck workspace", "Run the workspace type checker."},
		{"check:desktop", "Check desktop shell", "Run the desktop Rust check pass."},
	}
	testPriority := []struct {
		Name        string
		Label       string
		Description string
	}{
		{"test", "Run tests", "Run the workspace test suite."},
		{"smoke:system", "Run smoke tests", "Run the end-to-end system smoke checks."},
	}

	for _, item := range runPriority {
		addScript(item.Name, "run", item.Label, item.Description)
	}
	for _, item := range buildPriority {
		addScript(item.Name, "build", item.Label, item.Description)
	}
	for _, item := range checkPriority {
		addScript(item.Name, "check", item.Label, item.Description)
	}
	for _, item := range testPriority {
		addScript(item.Name, "test", item.Label, item.Description)
	}

	defaultRun := firstTaskID(tasks, "run")
	defaultBuild := firstTaskID(tasks, "build")

	return TaskSet{
		Tasks:            tasks,
		DefaultBuildTask: defaultBuild,
		DefaultRunTask:   defaultRun,
	}
}

func detectCargoTasks(root string) TaskSet {
	if _, err := os.Stat(filepath.Join(root, "Cargo.toml")); err != nil {
		return TaskSet{Tasks: []Task{}}
	}

	tasks := []Task{
		{
			ID:          "cargo:run",
			Label:       "Run cargo app",
			Command:     "cargo run",
			Kind:        "run",
			Source:      "cargo",
			Description: "Run the current Cargo project.",
		},
		{
			ID:          "cargo:build",
			Label:       "Build cargo app",
			Command:     "cargo build",
			Kind:        "build",
			Source:      "cargo",
			Description: "Build the current Cargo project.",
		},
		{
			ID:          "cargo:test",
			Label:       "Run cargo tests",
			Command:     "cargo test",
			Kind:        "test",
			Source:      "cargo",
			Description: "Run the Cargo test suite.",
		},
	}

	return TaskSet{
		Tasks:            tasks,
		DefaultBuildTask: "cargo:build",
		DefaultRunTask:   "cargo:run",
	}
}

func detectGoTasks(root string) TaskSet {
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		return TaskSet{Tasks: []Task{}}
	}

	tasks := []Task{
		{
			ID:          "go:run",
			Label:       "Run Go app",
			Command:     "go run .",
			Kind:        "run",
			Source:      "go",
			Description: "Run the current Go module.",
		},
		{
			ID:          "go:build",
			Label:       "Build Go module",
			Command:     "go build ./...",
			Kind:        "build",
			Source:      "go",
			Description: "Build every package in the current Go module.",
		},
		{
			ID:          "go:test",
			Label:       "Run Go tests",
			Command:     "go test ./...",
			Kind:        "test",
			Source:      "go",
			Description: "Run the current Go test suite.",
		},
	}

	return TaskSet{
		Tasks:            tasks,
		DefaultBuildTask: "go:build",
		DefaultRunTask:   "go:run",
	}
}

func detectPackageManager(root string) string {
	switch {
	case fileExists(filepath.Join(root, "pnpm-lock.yaml")):
		return "pnpm"
	case fileExists(filepath.Join(root, "yarn.lock")):
		return "yarn"
	case fileExists(filepath.Join(root, "bun.lockb")) || fileExists(filepath.Join(root, "bun.lock")):
		return "bun"
	default:
		return "npm"
	}
}

func packageManagerCommand(packageManager, script string) string {
	switch packageManager {
	case "pnpm":
		return "pnpm " + script
	case "yarn":
		return "yarn " + script
	case "bun":
		return "bun run " + script
	default:
		return "npm run " + script
	}
}

func firstTaskID(tasks []Task, kind string) string {
	for _, task := range tasks {
		if task.Kind == kind {
			return task.ID
		}
	}
	return ""
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
