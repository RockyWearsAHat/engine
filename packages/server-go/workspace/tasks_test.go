package workspace

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestDetectTasks_PackageJSON(t *testing.T) {
	dir := t.TempDir()
	pkg := map[string]interface{}{
		"scripts": map[string]string{
			"dev:desktop": "vite",
			"build":       "tsc && vite build",
			"test":        "vitest",
			"typecheck":   "tsc --noEmit",
		},
	}
	data, _ := json.Marshal(pkg)
	if err := os.WriteFile(filepath.Join(dir, "package.json"), data, 0644); err != nil {
		t.Fatalf("write package.json: %v", err)
	}

	ts := DetectTasks(dir)
	if len(ts.Tasks) == 0 {
		t.Fatal("expected tasks from package.json")
	}

	hasRun := false
	hasBuild := false
	hasTest := false
	for _, task := range ts.Tasks {
		switch task.Kind {
		case "run":
			hasRun = true
		case "build":
			hasBuild = true
		case "test":
			hasTest = true
		}
	}
	if !hasRun {
		t.Error("expected a run task")
	}
	if !hasBuild {
		t.Error("expected a build task")
	}
	if !hasTest {
		t.Error("expected a test task")
	}
}

func TestDetectTasks_PackageJSON_PNPM(t *testing.T) {
	dir := t.TempDir()
	pkg := map[string]interface{}{
		"scripts": map[string]string{"dev": "vite"},
	}
	data, _ := json.Marshal(pkg)
	if err := os.WriteFile(filepath.Join(dir, "package.json"), data, 0644); err != nil {
		t.Fatalf("write package.json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "pnpm-lock.yaml"), []byte("lockfileVersion: '9.0'"), 0644); err != nil {
		t.Fatalf("write pnpm-lock.yaml: %v", err)
	}

	ts := DetectTasks(dir)
	found := false
	for _, task := range ts.Tasks {
		if task.Kind == "run" && task.Command == "pnpm dev" {
			found = true
		}
	}
	if !found {
		t.Error("expected pnpm dev command")
	}
}

func TestDetectTasks_PackageJSON_Yarn(t *testing.T) {
	dir := t.TempDir()
	pkg := map[string]interface{}{
		"scripts": map[string]string{"start": "node server.js"},
	}
	data, _ := json.Marshal(pkg)
	if err := os.WriteFile(filepath.Join(dir, "package.json"), data, 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "yarn.lock"), []byte(""), 0644); err != nil {
		t.Fatalf("write yarn.lock: %v", err)
	}

	ts := DetectTasks(dir)
	for _, task := range ts.Tasks {
		if task.Kind == "run" && !containsStr(task.Command, "yarn") {
			t.Errorf("expected yarn command, got %q", task.Command)
		}
	}
}

func TestDetectTasks_PackageJSON_Bun(t *testing.T) {
	dir := t.TempDir()
	pkg := map[string]interface{}{
		"scripts": map[string]string{"dev": "bun run index.ts"},
	}
	data, _ := json.Marshal(pkg)
	if err := os.WriteFile(filepath.Join(dir, "package.json"), data, 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "bun.lockb"), []byte(""), 0644); err != nil {
		t.Fatalf("write bun.lockb: %v", err)
	}

	ts := DetectTasks(dir)
	for _, task := range ts.Tasks {
		if task.Kind == "run" && !containsStr(task.Command, "bun") {
			t.Errorf("expected bun command, got %q", task.Command)
		}
	}
}

func TestDetectTasks_PackageJSON_Bun_BunLock(t *testing.T) {
	dir := t.TempDir()
	pkg := map[string]interface{}{
		"scripts": map[string]string{"preview": "bun preview"},
	}
	data, _ := json.Marshal(pkg)
	if err := os.WriteFile(filepath.Join(dir, "package.json"), data, 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "bun.lock"), []byte(""), 0644); err != nil {
		t.Fatalf("write bun.lock: %v", err)
	}

	ts := DetectTasks(dir)
	_ = ts
}

func TestDetectTasks_CargoToml(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Cargo.toml"), []byte("[package]\nname = \"myapp\""), 0644); err != nil {
		t.Fatalf("write Cargo.toml: %v", err)
	}

	ts := DetectTasks(dir)
	if len(ts.Tasks) == 0 {
		t.Fatal("expected cargo tasks")
	}
	if ts.DefaultBuildTask != "cargo:build" {
		t.Errorf("DefaultBuildTask = %q, want cargo:build", ts.DefaultBuildTask)
	}
	if ts.DefaultRunTask != "cargo:run" {
		t.Errorf("DefaultRunTask = %q, want cargo:run", ts.DefaultRunTask)
	}
}

func TestDetectTasks_GoMod(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/myapp\ngo 1.21"), 0644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}

	ts := DetectTasks(dir)
	if len(ts.Tasks) == 0 {
		t.Fatal("expected go tasks")
	}
	if ts.DefaultBuildTask != "go:build" {
		t.Errorf("DefaultBuildTask = %q, want go:build", ts.DefaultBuildTask)
	}
}

func TestDetectTasks_Empty(t *testing.T) {
	dir := t.TempDir()
	ts := DetectTasks(dir)
	if len(ts.Tasks) != 0 {
		t.Errorf("expected 0 tasks, got %d", len(ts.Tasks))
	}
}

func TestDetectTasks_EmptyRoot(t *testing.T) {
	ts := DetectTasks("")
	if len(ts.Tasks) != 0 {
		t.Errorf("expected 0 tasks for empty root")
	}
}

func TestDetectTasks_InvalidPackageJSON(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte("{invalid json"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	ts := DetectTasks(dir)
	// Should fall through to other detectors
	_ = ts
}

func TestDetectTasks_EmptyScripts(t *testing.T) {
	dir := t.TempDir()
	pkg := map[string]interface{}{
		"scripts": map[string]string{},
	}
	data, _ := json.Marshal(pkg)
	if err := os.WriteFile(filepath.Join(dir, "package.json"), data, 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	ts := DetectTasks(dir)
	// Empty scripts → falls through to other detectors
	_ = ts
}

func TestDetectTasks_AllScriptTypes(t *testing.T) {
	dir := t.TempDir()
	pkg := map[string]interface{}{
		"scripts": map[string]string{
			"dev:tauri":            "tauri dev",
			"build:desktop-debug":  "cargo build",
			"build:tauri":          "tauri build",
			"check:desktop":        "cargo check",
			"smoke:system":         "node smoke.mjs",
			"dev":                  "vite",
			"start":                "node dist/server.js",
		},
	}
	data, _ := json.Marshal(pkg)
	if err := os.WriteFile(filepath.Join(dir, "package.json"), data, 0644); err != nil {
		t.Fatalf("write package.json: %v", err)
	}

	ts := DetectTasks(dir)
	if len(ts.Tasks) == 0 {
		t.Fatal("expected tasks")
	}
}

func containsStr(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && (s[:len(sub)] == sub || contains(s, sub)))
}

func contains(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
