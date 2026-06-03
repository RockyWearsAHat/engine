package quality

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestScanProject_FindsCoreIssueTypes(t *testing.T) {
	project := t.TempDir()
	if err := os.MkdirAll(filepath.Join(project, ".github"), 0o755); err != nil {
		t.Fatalf("mkdir behaviors: %v", err)
	}
	if err := os.WriteFile(filepath.Join(project, ".github", "WORKING_BEHAVIORS.md"), []byte("# Behaviors\n"), 0o644); err != nil {
		t.Fatalf("write behaviors: %v", err)
	}

	srcDir := filepath.Join(project, "packages", "demo")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatalf("mkdir src: %v", err)
	}
	goFile := `package demo

func lonelyThing() int {
	value := 0
	value += 1
	value += 2
	value += 3
	value += 4
	value += 5
	value += 6
	value += 7
	value += 8
	value += 9
	value += 10
	value += 11
	value += 12
	value += 13
	value += 14
	value += 15
	value += 16
	value += 17
	value += 18
	value += 19
	value += 20
	value += 21
	value += 22
	value += 23
	value += 24
	value += 25
	value += 26
	value += 27
	value += 28
	value += 29
	value += 30
	value += 31
	value += 32
	value += 33
	value += 34
	value += 35
	value += 36
	value += 37
	value += 38
	value += 39
	value += 40
	value += 41
	value += 42
	value += 43
	value += 44
	value += 45
	value += 46
	value += 47
	value += 48
	value += 49
	value += 50
	value += 51
	value += 52
	value += 53
	value += 54
	value += 55
	value += 56
	value += 57
	value += 58
	value += 59
	value += 60
	return value
}
`
	if err := os.WriteFile(filepath.Join(srcDir, "lonely.go"), []byte(goFile), 0o644); err != nil {
		t.Fatalf("write go file: %v", err)
	}

	report, err := ScanProject(project, 200)
	if err != nil {
		t.Fatalf("scan project: %v", err)
	}
	if report.IssueCount == 0 {
		t.Fatalf("expected at least one issue")
	}
	if report.HighCount+report.MediumCount+report.LowCount != report.IssueCount {
		t.Fatalf("severity totals must match issue count: %+v", report)
	}

	hasCategory := map[string]bool{}
	for _, issue := range report.Issues {
		hasCategory[issue.Category] = true
	}
	if !hasCategory["large-block-without-comment"] {
		t.Fatalf("expected large-block-without-comment category, got %+v", hasCategory)
	}
	if !hasCategory["dead-code"] {
		t.Fatalf("expected dead-code category, got %+v", hasCategory)
	}
}

func TestRefreshProjectIndex_PersistsAndUpdatesChangedFiles(t *testing.T) {
	project := t.TempDir()
	if err := os.MkdirAll(filepath.Join(project, ".github"), 0o755); err != nil {
		t.Fatalf("mkdir behaviors: %v", err)
	}
	if err := os.WriteFile(filepath.Join(project, ".github", "WORKING_BEHAVIORS.md"), []byte("# Behaviors\n"), 0o644); err != nil {
		t.Fatalf("write behaviors: %v", err)
	}

	srcDir := filepath.Join(project, "packages", "demo")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatalf("mkdir src: %v", err)
	}
	filePath := filepath.Join(srcDir, "demo.go")
	firstContent := strings.Join([]string{
		"package demo",
		"",
		"func lonelyThing() int {",
		"\treturn 1",
		"}",
		"",
	}, "\n")
	if err := os.WriteFile(filePath, []byte(firstContent), 0o644); err != nil {
		t.Fatalf("write first source: %v", err)
	}

	if err := RefreshProjectIndex(project); err != nil {
		t.Fatalf("refresh project index: %v", err)
	}

	indexPath := filepath.Join(project, ".engine", "quality-index.json")
	content, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatalf("read quality index: %v", err)
	}

	var firstIndex projectIndex
	if err := json.Unmarshal(content, &firstIndex); err != nil {
		t.Fatalf("unmarshal first index: %v", err)
	}
	entry, ok := firstIndex.Files["packages/demo/demo.go"]
	if !ok {
		t.Fatalf("expected indexed file entry, got %+v", firstIndex.Files)
	}
	if entry.IdentifierCounts["lonelyThing"] == 0 {
		t.Fatalf("expected lonelyThing identifier count in index entry: %+v", entry.IdentifierCounts)
	}

	secondContent := strings.Join([]string{
		"package demo",
		"",
		"func renamedThing() int {",
		"\treturn 2",
		"}",
		"",
	}, "\n")
	if err := os.WriteFile(filePath, []byte(secondContent), 0o644); err != nil {
		t.Fatalf("write second source: %v", err)
	}

	if err := RefreshProjectIndex(project); err != nil {
		t.Fatalf("refresh changed project index: %v", err)
	}

	content, err = os.ReadFile(indexPath)
	if err != nil {
		t.Fatalf("read updated quality index: %v", err)
	}
	var secondIndex projectIndex
	if err := json.Unmarshal(content, &secondIndex); err != nil {
		t.Fatalf("unmarshal second index: %v", err)
	}
	updated := secondIndex.Files["packages/demo/demo.go"]
	if updated.IdentifierCounts["renamedThing"] == 0 {
		t.Fatalf("expected renamedThing identifier count in updated entry: %+v", updated.IdentifierCounts)
	}
	if updated.IdentifierCounts["lonelyThing"] != 0 {
		t.Fatalf("expected lonelyThing identifier count to be removed after refresh: %+v", updated.IdentifierCounts)
	}
}

func TestRefreshProjectIndex_CompletesGeneratedMapBeforeScan(t *testing.T) {
	project := t.TempDir()
	if err := os.MkdirAll(filepath.Join(project, ".github"), 0o755); err != nil {
		t.Fatalf("mkdir behaviors: %v", err)
	}
	if err := os.WriteFile(filepath.Join(project, ".github", "WORKING_BEHAVIORS.md"), []byte("# Behaviors\n"), 0o644); err != nil {
		t.Fatalf("write behaviors: %v", err)
	}

	if err := os.MkdirAll(filepath.Join(project, "packages", "demo"), 0o755); err != nil {
		t.Fatalf("mkdir source dir: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(project, "dist"), 0o755); err != nil {
		t.Fatalf("mkdir dist dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(project, "packages", "demo", "demo.go"), []byte("package demo\nfunc Keep() int { return 1 }\n"), 0o644); err != nil {
		t.Fatalf("write source file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(project, "dist", "generated.js"), []byte("export const dup = 1;\n"), 0o644); err != nil {
		t.Fatalf("write generated file: %v", err)
	}

	if err := os.MkdirAll(filepath.Join(project, ".engine"), 0o755); err != nil {
		t.Fatalf("mkdir .engine: %v", err)
	}
	incompleteMap := `{"updatedAt":"","paths":[]}`
	if err := os.WriteFile(filepath.Join(project, ".engine", "generated-files-cache.json"), []byte(incompleteMap), 0o644); err != nil {
		t.Fatalf("write incomplete generated map: %v", err)
	}

	if err := RefreshProjectIndex(project); err != nil {
		t.Fatalf("refresh index with incomplete generated map: %v", err)
	}

	mapBytes, err := os.ReadFile(filepath.Join(project, ".engine", "generated-files-cache.json"))
	if err != nil {
		t.Fatalf("read generated map: %v", err)
	}
	var gmap generatedCache
	if err := json.Unmarshal(mapBytes, &gmap); err != nil {
		t.Fatalf("unmarshal generated map: %v", err)
	}
	if strings.TrimSpace(gmap.UpdatedAt) == "" {
		t.Fatalf("expected generated map to be completed with updatedAt timestamp")
	}

	idx := loadProjectIndex(project)
	if _, ok := idx.Files["dist/generated.js"]; ok {
		t.Fatalf("expected generated file to be excluded from quality index")
	}
	if _, ok := idx.Files["packages/demo/demo.go"]; !ok {
		t.Fatalf("expected source file to remain in quality index")
	}
}

func TestRefreshProjectIndex_DoesNotExcludeAncestorDirsForNestedGeneratedPaths(t *testing.T) {
	project := t.TempDir()
	if err := os.MkdirAll(filepath.Join(project, ".github"), 0o755); err != nil {
		t.Fatalf("mkdir behaviors: %v", err)
	}
	if err := os.WriteFile(filepath.Join(project, ".github", "WORKING_BEHAVIORS.md"), []byte("# Behaviors\n"), 0o644); err != nil {
		t.Fatalf("write behaviors: %v", err)
	}

	sourceDir := filepath.Join(project, "packages", "demo")
	generatedDir := filepath.Join(sourceDir, "dist")
	if err := os.MkdirAll(generatedDir, 0o755); err != nil {
		t.Fatalf("mkdir generated dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "demo.go"), []byte("package demo\nfunc Keep() int { return 1 }\n"), 0o644); err != nil {
		t.Fatalf("write source file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(generatedDir, "bundle.js"), []byte("export const generated = true;\n"), 0o644); err != nil {
		t.Fatalf("write generated bundle: %v", err)
	}

	if err := RefreshProjectIndex(project); err != nil {
		t.Fatalf("refresh index: %v", err)
	}

	idx := loadProjectIndex(project)
	if _, ok := idx.Files["packages/demo/demo.go"]; !ok {
		t.Fatalf("expected nested generated dir to not suppress ancestor source file")
	}
	if _, ok := idx.Files["packages/demo/dist/bundle.js"]; ok {
		t.Fatalf("expected nested generated file to be excluded from quality index")
	}
}
