package quality

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeQualityTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func issueCategories(report Report) map[string][]Issue {
	categories := make(map[string][]Issue)
	for _, issue := range report.Issues {
		categories[issue.Category] = append(categories[issue.Category], issue)
	}
	return categories
}

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

func TestScanProject_ReadsWorkspaceDocsAcrossSupportedFormats(t *testing.T) {
	project := t.TempDir()
	writeQualityTestFile(t, filepath.Join(project, ".github", "WORKING_BEHAVIORS.md"), "# Behaviors\n")
	writeQualityTestFile(t, filepath.Join(project, "docs", "guide.md"), "This note references packages/demo/mentioned-md.go.\n")
	writeQualityTestFile(t, filepath.Join(project, "docs", "guide.mdx"), "This note references packages/demo/mentioned-mdx.go.\n")
	writeQualityTestFile(t, filepath.Join(project, "docs", "guide.txt"), "This note references packages/demo/mentioned-txt.go.\n")
	writeQualityTestFile(t, filepath.Join(project, "docs", "guide.dx"), "This note references packages/demo/mentioned-dx.go.\n")

	for _, name := range []string{"mentioned-md.go", "mentioned-mdx.go", "mentioned-txt.go", "mentioned-dx.go"} {
		writeQualityTestFile(t, filepath.Join(project, "packages", "demo", name), "package demo\n\n// ExportedSymbol keeps this file eligible for docs-coverage checks.\nfunc ExportedSymbol() int {\n\treturn 1\n}\n")
	}
	writeQualityTestFile(t, filepath.Join(project, "packages", "demo", "orphan.go"), "package demo\n\n// MissingDocsRef should be documented in workspace docs but is intentionally omitted.\nfunc MissingDocsRef() int {\n\treturn 1\n}\n")

	report, err := ScanProject(project, 200)
	if err != nil {
		t.Fatalf("scan project: %v", err)
	}

	categories := issueCategories(report)
	if len(categories["documentation-gap"]) == 0 {
		t.Fatalf("expected at least one documentation-gap issue")
	}
	for _, issue := range categories["documentation-gap"] {
		if issue.File == "packages/demo/mentioned-md.go" || issue.File == "packages/demo/mentioned-mdx.go" || issue.File == "packages/demo/mentioned-txt.go" || issue.File == "packages/demo/mentioned-dx.go" {
			t.Fatalf("expected supported docs formats to mark files as documented, got unexpected issue: %+v", issue)
		}
	}
	foundOrphan := false
	for _, issue := range categories["documentation-gap"] {
		if strings.Contains(issue.Message, "orphan.go") {
			foundOrphan = true
			break
		}
	}
	if !foundOrphan {
		t.Fatalf("expected orphan.go to be flagged as missing from documentation")
	}
}

func TestScanProject_FlagsPublicFunctionsWithoutAdjacentDocComments(t *testing.T) {
	project := t.TempDir()
	writeQualityTestFile(t, filepath.Join(project, ".github", "WORKING_BEHAVIORS.md"), "# Behaviors\npackages/demo/public.go\n")
	writeQualityTestFile(t, filepath.Join(project, "packages", "demo", "public.go"), "package demo\n\nfunc Run() int {\n\treturn 1\n}\n")

	report, err := ScanProject(project, 200)
	if err != nil {
		t.Fatalf("scan project: %v", err)
	}

	categories := issueCategories(report)
	issues := categories["documentation-gap"]
	if len(issues) == 0 {
		t.Fatalf("expected documentation-gap issue for public function")
	}
	found := false
	for _, issue := range issues {
		if issue.File == "packages/demo" && strings.Contains(issue.Message, "public functions across") && strings.Contains(issue.Message, "public.go") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected public function doc comment issue, got %+v", issues)
	}
}

func TestScanProject_FlagsPublicInterfacesMissingWorkspaceDocs(t *testing.T) {
	project := t.TempDir()
	writeQualityTestFile(t, filepath.Join(project, ".github", "WORKING_BEHAVIORS.md"), "# Behaviors\n")
	writeQualityTestFile(t, filepath.Join(project, "packages", "demo", "iface.ts"), "export interface BuildContract {\n  id: string\n}\n")

	report, err := ScanProject(project, 200)
	if err != nil {
		t.Fatalf("scan project: %v", err)
	}

	found := false
	for _, issue := range report.Issues {
		if issue.Category == "documentation-gap" && strings.Contains(issue.Message, "public interfaces") && strings.Contains(issue.Message, "iface.ts") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected interface documentation-gap issue, got %+v", report.Issues)
	}
}

func TestScanProject_DuplicateSeverity_PrioritizesSubstantialOverlap(t *testing.T) {
	project := t.TempDir()
	writeQualityTestFile(t, filepath.Join(project, ".github", "WORKING_BEHAVIORS.md"), "# Behaviors\npackages/demo/dup/\n")
	writeQualityTestFile(t, filepath.Join(project, "packages", "demo", "small-a.go"), "package demo\nvar repeated = 1\n")
	writeQualityTestFile(t, filepath.Join(project, "packages", "demo", "small-b.go"), "package demo\nvar repeated = 1\n")

	decl := strings.Join([]string{
		"package demo",
		"type Config struct {",
		"\tName string",
		"\tEnabled bool",
		"\tCount int",
		"\tMode string",
		"\tRetry int",
		"}",
	}, "\n")
	writeQualityTestFile(t, filepath.Join(project, "packages", "demo", "decl-a.go"), decl)
	writeQualityTestFile(t, filepath.Join(project, "packages", "demo", "decl-b.go"), decl)

	report, err := ScanProject(project, 200)
	if err != nil {
		t.Fatalf("scan project: %v", err)
	}

	hasDuplicate := false
	hasHighDeclDuplicate := false
	hasSmallDuplicate := false
	for _, issue := range report.Issues {
		if issue.Category != "duplicate-content" {
			continue
		}
		hasDuplicate = true
		if issue.File == "packages/demo/decl-b.go" && issue.Severity == "high" {
			hasHighDeclDuplicate = true
		}
		if issue.File == "packages/demo/small-b.go" {
			hasSmallDuplicate = true
		}
	}
	if !hasDuplicate {
		t.Fatalf("expected at least one duplicate-content issue")
	}
	if !hasHighDeclDuplicate {
		t.Fatalf("expected declaration-like duplicate block to be marked high severity")
	}
	if hasSmallDuplicate {
		t.Fatalf("expected short exact two-line duplicate to be filtered as low-signal noise")
	}
}

func TestScanProject_DuplicateDetection_IgnoresCommentsAndUsesLargestRunSeverity(t *testing.T) {
	project := t.TempDir()
	writeQualityTestFile(t, filepath.Join(project, ".github", "WORKING_BEHAVIORS.md"), "# Behaviors\npackages/demo/dup/\n")

	left := strings.Join([]string{
		"package demo",
		"func SharedLogic() int {",
		"  // comment should be ignored",
		"  a := 1",
		"  b := 2",
		"  c := a + b",
		"  d := c + 1",
		"  e := d + 1",
		"  f := e + 1",
		"  g := f + 1",
		"  h := g + 1",
		"  i := h + 1",
		"  return i",
		"}",
	}, "\n")

	right := strings.Join([]string{
		"package demo",
		"func AnotherSharedLogic() int {",
		"  a := 1 // inline comment should be ignored",
		"  b := 2",
		"  /* block comment should be ignored */",
		"  c := a + b",
		"  d := c + 1",
		"  e := d + 1",
		"  f := e + 1",
		"  g := f + 1",
		"  h := g + 1",
		"  i := h + 1",
		"  return i",
		"}",
	}, "\n")

	writeQualityTestFile(t, filepath.Join(project, "packages", "demo", "dup-left.go"), left)
	writeQualityTestFile(t, filepath.Join(project, "packages", "demo", "dup-right.go"), right)

	report, err := ScanProject(project, 200)
	if err != nil {
		t.Fatalf("scan project: %v", err)
	}

	dups := make([]Issue, 0)
	for _, issue := range report.Issues {
		if issue.Category == "duplicate-content" {
			dups = append(dups, issue)
		}
	}
	if len(dups) == 0 {
		t.Fatalf("expected duplicate-content issue, got %+v", report.Issues)
	}

	var target *Issue
	for i := range dups {
		if dups[i].File == "packages/demo/dup-right.go" {
			target = &dups[i]
			break
		}
	}
	if target == nil {
		t.Fatalf("expected duplicate issue on dup-right.go, got %+v", dups)
	}
	if target.Severity != "high" && target.Severity != "medium" {
		t.Fatalf("expected overlap-scored duplicate severity (medium/high), got %+v", target)
	}
	if !strings.Contains(target.Message, "Duplicate code") && !strings.Contains(target.Message, "High-risk duplicate code") {
		t.Fatalf("expected explicit duplicate-code wording in message, got %+v", target)
	}
	if !strings.Contains(target.Message, "overlap") {
		t.Fatalf("expected overlap percentage in duplicate message, got %+v", target)
	}
	if !strings.Contains(target.Message, "helper/module") && !strings.Contains(target.Message, "source of truth") {
		t.Fatalf("expected actionable fix guidance in duplicate message, got %+v", target)
	}
}

func TestScanProject_DuplicateSeverity_OneLineExactDuplicateIsIgnored(t *testing.T) {
	project := t.TempDir()
	writeQualityTestFile(t, filepath.Join(project, ".github", "WORKING_BEHAVIORS.md"), "# Behaviors\npackages/demo/\n")
	writeQualityTestFile(t, filepath.Join(project, "packages", "demo", "single-a.go"), "package demo\nconst Mirror = 42\n")
	writeQualityTestFile(t, filepath.Join(project, "packages", "demo", "single-b.go"), "package demo\nconst Mirror = 42\n")

	report, err := ScanProject(project, 200)
	if err != nil {
		t.Fatalf("scan project: %v", err)
	}

	for _, issue := range report.Issues {
		if issue.Category == "duplicate-content" && issue.File == "packages/demo/single-b.go" {
			t.Fatalf("expected one-line exact duplicate to be ignored as low-signal noise, got %+v", issue)
		}
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

func TestScanProject_PrincipleScope_DoesNotEmitReactSpecificPitfalls(t *testing.T) {
	project := t.TempDir()
	writeQualityTestFile(t, filepath.Join(project, ".github", "WORKING_BEHAVIORS.md"), "# Behaviors\n")
	writeQualityTestFile(t, filepath.Join(project, "packages", "demo", "list.tsx"), strings.Join([]string{
		"import React from 'react'",
		"",
		"export function DemoList({ items }: { items: string[] }) {",
		"  return (",
		"    <ul>",
		"      {items.map((item, index) => (",
		"        <li key={index} onClick={() => console.log(item)}>{item}</li>",
		"      ))}",
		"    </ul>",
		"  )",
		"}",
	}, "\n"))

	report, err := ScanProject(project, 200)
	if err != nil {
		t.Fatalf("scan project: %v", err)
	}

	for _, issue := range report.Issues {
		if issue.Category == "react-pitfall" || strings.HasPrefix(issue.Rule, "react.") {
			t.Fatalf("expected principle-only findings and no react-specific pitfall flags, got %+v", issue)
		}
	}
}

func TestScanProject_CSSClassFuzzyMatching(t *testing.T) {
	project := t.TempDir()
	writeQualityTestFile(t, filepath.Join(project, ".github", "WORKING_BEHAVIORS.md"), "# Behaviors\n")
	writeQualityTestFile(t, filepath.Join(project, "packages", "demo", "panel.css"), strings.Join([]string{
		".quality-panel-root { display: block; }",
		".unused-token { color: red; }",
	}, "\n"))
	writeQualityTestFile(t, filepath.Join(project, "packages", "demo", "panel.tsx"), strings.Join([]string{
		"export function Panel() {",
		"  return <section className=\"quality_panel_root\">ok</section>",
		"}",
	}, "\n"))

	report, err := ScanProject(project, 200)
	if err != nil {
		t.Fatalf("scan project: %v", err)
	}

	flaggedUnusedToken := false
	flaggedUsedToken := false
	for _, issue := range report.Issues {
		if issue.Category != "css-usage" {
			continue
		}
		if strings.Contains(issue.Message, ".unusedtoken") {
			flaggedUnusedToken = true
		}
		if strings.Contains(issue.Message, ".qualitypanelroot") {
			flaggedUsedToken = true
		}
	}
	if !flaggedUnusedToken {
		t.Fatalf("expected unused css selector to be reported, got %+v", report.Issues)
	}
	if flaggedUsedToken {
		t.Fatalf("expected fuzzy class matching to keep used selector out of css-usage findings, got %+v", report.Issues)
	}
}

func TestScanProject_CSPrinciple_DoesNotFlagLargeLinearFunction(t *testing.T) {
	project := t.TempDir()
	writeQualityTestFile(t, filepath.Join(project, ".github", "WORKING_BEHAVIORS.md"), "# Behaviors\n")

	lines := []string{"export function LinearWorker() {", "  let total = 0"}
	for i := 0; i < 180; i++ {
		lines = append(lines, "  total += 1")
	}
	lines = append(lines, "  return total", "}")
	writeQualityTestFile(t, filepath.Join(project, "packages", "demo", "linear.ts"), strings.Join(lines, "\n"))

	report, err := ScanProject(project, 200)
	if err != nil {
		t.Fatalf("scan project: %v", err)
	}

	for _, issue := range report.Issues {
		if issue.Category == "cs-principle" && issue.File == "packages/demo/linear.ts" {
			t.Fatalf("expected linear function to be ignored by cs-principle long-function heuristic, got %+v", issue)
		}
	}
}

func TestScanProject_CSPrinciple_FlagsLargeDecisionHeavyFunction(t *testing.T) {
	project := t.TempDir()
	writeQualityTestFile(t, filepath.Join(project, ".github", "WORKING_BEHAVIORS.md"), "# Behaviors\n")

	lines := []string{"export function BranchyWorker(input: number) {", "  let score = 0"}
	for i := 0; i < 40; i++ {
		lines = append(lines,
			"  if (input > 0 && input < 100) {",
			"    score += 1",
			"  } else if (input === 0 || input === -1) {",
			"    score += 2",
			"  } else {",
			"    score += 3",
			"  }",
		)
	}
	lines = append(lines, "  return score", "}")
	writeQualityTestFile(t, filepath.Join(project, "packages", "demo", "branchy.ts"), strings.Join(lines, "\n"))

	report, err := ScanProject(project, 200)
	if err != nil {
		t.Fatalf("scan project: %v", err)
	}

	found := false
	for _, issue := range report.Issues {
		if issue.Category == "cs-principle" && issue.File == "packages/demo/branchy.ts" {
			found = true
			if !strings.Contains(issue.Message, "decision points") {
				t.Fatalf("expected decision-point context in cs-principle message, got %+v", issue)
			}
			break
		}
	}
	if !found {
		t.Fatalf("expected decision-heavy function to be flagged by cs-principle heuristic")
	}
}

func TestScanProject_CSPrinciple_IgnoredCallResultDoesNotTriggerIgnoredErrorIssue(t *testing.T) {
	project := t.TempDir()
	writeQualityTestFile(t, filepath.Join(project, ".github", "WORKING_BEHAVIORS.md"), "# Behaviors\n")
	writeQualityTestFile(t, filepath.Join(project, "packages", "demo", "noise.go"), strings.Join([]string{
		"package demo",
		"import \"fmt\"",
		"func Noisy() {",
		"  _ = fmt.Sprintf(\"%s\", \"ok\")",
		"}",
	}, "\n"))

	report, err := ScanProject(project, 200)
	if err != nil {
		t.Fatalf("scan project: %v", err)
	}

	for _, issue := range report.Issues {
		if issue.Category == "cs-principle" && strings.Contains(strings.ToLower(issue.Message), "ignored error assignment") {
			t.Fatalf("expected ignored call-result assignment to be ignored, got %+v", issue)
		}
	}
}

func TestScanProject_CSPrinciple_ExplicitIgnoredErrIsFlagged(t *testing.T) {
	project := t.TempDir()
	writeQualityTestFile(t, filepath.Join(project, ".github", "WORKING_BEHAVIORS.md"), "# Behaviors\n")
	writeQualityTestFile(t, filepath.Join(project, "packages", "demo", "ignored.go"), strings.Join([]string{
		"package demo",
		"func ExplicitIgnore() {",
		"  err := doThing()",
		"  _ = err",
		"}",
		"func doThing() error { return nil }",
	}, "\n"))

	report, err := ScanProject(project, 200)
	if err != nil {
		t.Fatalf("scan project: %v", err)
	}

	found := false
	for _, issue := range report.Issues {
		if issue.Category == "cs-principle" && strings.Contains(strings.ToLower(issue.Message), "ignored error assignment") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected explicit _ = err pattern to be flagged")
	}
}

func TestScanProject_CSPrinciple_ReportsAllExplicitIgnoredErrAssignments(t *testing.T) {
	project := t.TempDir()
	writeQualityTestFile(t, filepath.Join(project, ".github", "WORKING_BEHAVIORS.md"), "# Behaviors\n")
	writeQualityTestFile(t, filepath.Join(project, "packages", "demo", "ignored_many.go"), strings.Join([]string{
		"package demo",
		"func ExplicitIgnoreMany() {",
		"  err := doThing()",
		"  _ = err",
		"  otherErr := doThing()",
		"  _ = otherErr",
		"  thirdErr := doThing()",
		"  _ = thirdErr",
		"}",
		"func doThing() error { return nil }",
	}, "\n"))

	report, err := ScanProject(project, 200)
	if err != nil {
		t.Fatalf("scan project: %v", err)
	}

	count := 0
	for _, issue := range report.Issues {
		if issue.Rule == "cs.error-handling.ignored-error-assignment" && issue.File == "packages/demo/ignored_many.go" {
			count++
		}
	}
	if count != 3 {
		t.Fatalf("expected 3 ignored error assignment issues, got %d", count)
	}
}

func TestScanProject_CSPrinciple_FlagsPythonDecisionHeavyFunction(t *testing.T) {
	project := t.TempDir()
	writeQualityTestFile(t, filepath.Join(project, ".github", "WORKING_BEHAVIORS.md"), "# Behaviors\n")

	lines := []string{"def branchy_worker(value):", "    score = 0"}
	for i := 0; i < 40; i++ {
		lines = append(lines,
			"    if value > 0 and value < 100:",
			"        score += 1",
			"    elif value == 0 or value == -1:",
			"        score += 2",
			"    else:",
			"        score += 3",
		)
	}
	lines = append(lines, "    return score")
	writeQualityTestFile(t, filepath.Join(project, "packages", "demo", "branchy.py"), strings.Join(lines, "\n"))

	report, err := ScanProject(project, 200)
	if err != nil {
		t.Fatalf("scan project: %v", err)
	}

	found := false
	for _, issue := range report.Issues {
		if issue.Category == "cs-principle" && issue.File == "packages/demo/branchy.py" && strings.Contains(issue.Message, "decision points") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected python decision-heavy function to be flagged")
	}
}

func TestScanProject_DocumentationGap_FlagsPublicRustFunctionWithoutDocComment(t *testing.T) {
	project := t.TempDir()
	writeQualityTestFile(t, filepath.Join(project, ".github", "WORKING_BEHAVIORS.md"), "# Behaviors\n")
	writeQualityTestFile(t, filepath.Join(project, "packages", "demo", "lib.rs"), strings.Join([]string{
		"pub fn run() -> i32 {",
		"    1",
		"}",
	}, "\n"))

	report, err := ScanProject(project, 200)
	if err != nil {
		t.Fatalf("scan project: %v", err)
	}

	found := false
	for _, issue := range report.Issues {
		if issue.Category == "documentation-gap" && strings.Contains(issue.Message, "public functions") && strings.Contains(issue.Message, "lib.rs") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected documentation-gap issue for public rust function")
	}
}

func TestScanProject_EmitsRuleMetadata(t *testing.T) {
	project := t.TempDir()
	writeQualityTestFile(t, filepath.Join(project, ".github", "WORKING_BEHAVIORS.md"), "# Behaviors\n")
	writeQualityTestFile(t, filepath.Join(project, "packages", "demo", "demo.go"), strings.Join([]string{
		"package demo",
		"func onlyOnce() int {",
		"  return 1",
		"}",
	}, "\n"))

	report, err := ScanProject(project, 200)
	if err != nil {
		t.Fatalf("scan project: %v", err)
	}

	foundRule := false
	for _, issue := range report.Issues {
		if issue.Rule == "" {
			continue
		}
		foundRule = true
		if issue.DocURL == "" {
			t.Fatalf("expected doc URL for rule-backed issue, got %+v", issue)
		}
		break
	}
	if !foundRule {
		t.Fatalf("expected at least one issue with a rule id")
	}
}

func TestScanProject_RuleOverride_CoreSafetyRulesCannotBeSilenced(t *testing.T) {
	project := t.TempDir()
	writeQualityTestFile(t, filepath.Join(project, ".github", "WORKING_BEHAVIORS.md"), "# Behaviors\n")
	writeQualityTestFile(t, filepath.Join(project, ".engine", "quality-rules.json"), strings.Join([]string{
		"{",
		"  \"rules\": {",
		"    \"cs.error-handling.ignored-error-assignment\": \"off\",",
		"    \"cs.error-handling.discarded-result\": \"low\"",
		"  }",
		"}",
	}, "\n"))
	writeQualityTestFile(t, filepath.Join(project, "packages", "demo", "sample.go"), strings.Join([]string{
		"package demo",
		"func sample() {",
		"  err := doThing()",
		"  _ = err",
		"  _ = saveThing()",
		"}",
		"func doThing() error { return nil }",
		"func saveThing() error { return nil }",
	}, "\n"))

	report, err := ScanProject(project, 200)
	if err != nil {
		t.Fatalf("scan project: %v", err)
	}

	foundIgnored := false
	for _, issue := range report.Issues {
		if issue.Rule == "cs.error-handling.ignored-error-assignment" {
			foundIgnored = true
		}
	}
	if !foundIgnored {
		t.Fatalf("expected ignored-error rule to remain active even when override requests off")
	}

	foundDiscarded := false
	for _, issue := range report.Issues {
		if issue.Rule == "cs.error-handling.discarded-result" {
			foundDiscarded = true
			if issue.Severity != "low" {
				t.Fatalf("expected discarded-result severity override to low, got %+v", issue)
			}
		}
	}
	if !foundDiscarded {
		t.Fatalf("expected discarded-result rule to remain after override")
	}
}

func TestScanProject_BehavioralShift_FlagsPublicSideEffectCallChain(t *testing.T) {
	project := t.TempDir()
	writeQualityTestFile(t, filepath.Join(project, ".github", "WORKING_BEHAVIORS.md"), "# Behaviors\n")
	writeQualityTestFile(t, filepath.Join(project, "packages", "demo", "chain.ts"), strings.Join([]string{
		"function persistUser() {",
		"  saveUserProfile()",
		"}",
		"export function updateUserFlow() {",
		"  persistUser()",
		"}",
	}, "\n"))

	report, err := ScanProject(project, 200)
	if err != nil {
		t.Fatalf("scan project: %v", err)
	}

	found := false
	for _, issue := range report.Issues {
		if issue.Rule == "behavioral-shift.side-effect-chain" {
			found = true
			if !strings.Contains(issue.Message, "updateUserFlow") {
				t.Fatalf("expected public entry in behavioral shift message, got %+v", issue)
			}
		}
	}
	if !found {
		t.Fatalf("expected behavioral shift issue for public side-effect call chain")
	}
}

func TestScanProject_BehavioralShift_CannotBeSilencedByOffOverride(t *testing.T) {
	project := t.TempDir()
	writeQualityTestFile(t, filepath.Join(project, ".github", "WORKING_BEHAVIORS.md"), "# Behaviors\n")
	writeQualityTestFile(t, filepath.Join(project, ".engine", "quality-rules.json"), strings.Join([]string{
		"{",
		"  \"rules\": {",
		"    \"behavioral-shift.side-effect-chain\": \"off\"",
		"  }",
		"}",
	}, "\n"))
	writeQualityTestFile(t, filepath.Join(project, "packages", "demo", "chain.ts"), strings.Join([]string{
		"function persistUser() {",
		"  saveUserProfile()",
		"}",
		"export function updateUserFlow() {",
		"  persistUser()",
		"}",
	}, "\n"))

	report, err := ScanProject(project, 200)
	if err != nil {
		t.Fatalf("scan project: %v", err)
	}

	found := false
	for _, issue := range report.Issues {
		if issue.Rule == "behavioral-shift.side-effect-chain" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected behavioral-shift side-effect-chain to remain active when override requests off")
	}
}

func TestScanProject_BehavioralShift_CompactsPerFileFindings(t *testing.T) {
	project := t.TempDir()
	writeQualityTestFile(t, filepath.Join(project, ".github", "WORKING_BEHAVIORS.md"), "# Behaviors\n")
	writeQualityTestFile(t, filepath.Join(project, "packages", "demo", "many.ts"), strings.Join([]string{
		"function persistOne() { saveOne() }",
		"function persistTwo() { saveTwo() }",
		"function persistThree() { saveThree() }",
		"function persistFour() { saveFour() }",
		"export function runA() { persistOne() }",
		"export function runB() { persistTwo() }",
		"export function runC() { persistThree() }",
		"export function runD() { persistFour() }",
	}, "\n"))

	report, err := ScanProject(project, 200)
	if err != nil {
		t.Fatalf("scan project: %v", err)
	}

	chainCount := 0
	compactCount := 0
	for _, issue := range report.Issues {
		if issue.File != "packages/demo/many.ts" {
			continue
		}
		if issue.Rule == "behavioral-shift.side-effect-chain" {
			chainCount++
		}
		if issue.Rule == "behavioral-shift.compaction" {
			compactCount++
		}
	}
	if chainCount > 3 {
		t.Fatalf("expected behavioral-shift chain findings to be capped at 3 per file, got %d", chainCount)
	}
	if compactCount != 1 {
		t.Fatalf("expected one behavioral-shift compaction notice, got %d", compactCount)
	}
}

func TestScanProject_BehavioralShift_FindsRiskAcrossSiblingBranches(t *testing.T) {
	project := t.TempDir()
	writeQualityTestFile(t, filepath.Join(project, ".github", "WORKING_BEHAVIORS.md"), "# Behaviors\n")
	writeQualityTestFile(t, filepath.Join(project, "packages", "demo", "graph.ts"), strings.Join([]string{
		"function shared() {",
		"  if (Math.random() > 2) return",
		"}",
		"function left() {",
		"  shared()",
		"}",
		"function right() {",
		"  shared()",
		"  saveSnapshot()",
		"}",
		"export function rootFlow() {",
		"  left()",
		"  right()",
		"}",
	}, "\n"))

	report, err := ScanProject(project, 200)
	if err != nil {
		t.Fatalf("scan project: %v", err)
	}

	found := false
	for _, issue := range report.Issues {
		if issue.Rule == "behavioral-shift.side-effect-chain" && strings.Contains(issue.Message, "rootFlow") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected behavioral shift to be found across sibling branch traversal")
	}
}
