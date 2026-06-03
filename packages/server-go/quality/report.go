package quality

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

type Issue struct {
	ID         string `json:"id"`
	Severity   string `json:"severity"`
	Category   string `json:"category"`
	Message    string `json:"message"`
	File       string `json:"file"`
	Line       int    `json:"line"`
	Suggestion string `json:"suggestion,omitempty"`
}

type Report struct {
	ProjectPath string  `json:"projectPath"`
	GeneratedAt string  `json:"generatedAt"`
	IssueCount  int     `json:"issueCount"`
	HighCount   int     `json:"highCount"`
	MediumCount int     `json:"mediumCount"`
	LowCount    int     `json:"lowCount"`
	Issues      []Issue `json:"issues"`
}

type ScanProgress struct {
	ProjectPath     string  `json:"projectPath"`
	Phase           string  `json:"phase"`
	Current         int     `json:"current"`
	Total           int     `json:"total"`
	Percent         float64 `json:"percent"`
	CurrentFile     string  `json:"currentFile,omitempty"`
	CurrentFunction string  `json:"currentFunction,omitempty"`
	Section         string  `json:"section,omitempty"`
	Message         string  `json:"message,omitempty"`
}

type ProgressCallback func(ScanProgress)

type symbolDef struct {
	Name string
	File string
	Line int
}

type chunkLoc struct {
	File string
	Line int
}

type chunkRecord struct {
	Hash string `json:"hash"`
	Line int    `json:"line"`
}

type fileIndexEntry struct {
	Path             string         `json:"path"`
	ModTimeUnixNano  int64          `json:"modTimeUnixNano"`
	Size             int64          `json:"size"`
	BaseName         string         `json:"baseName"`
	IsTest           bool           `json:"isTest"`
	Symbols          []symbolDef    `json:"symbols"`
	IdentifierCounts map[string]int `json:"identifierCounts"`
	Chunks           []chunkRecord  `json:"chunks"`
	BaseIssues       []Issue        `json:"baseIssues"`
}

type projectIndex struct {
	Version     int                       `json:"version"`
	ProjectPath string                    `json:"projectPath"`
	UpdatedAt   string                    `json:"updatedAt"`
	Files       map[string]fileIndexEntry `json:"files"`
}

type sourceFileInfo struct {
	AbsPath         string
	RelPath         string
	ModTimeUnixNano int64
	Size            int64
}

const qualityIndexVersion = 1

var (
	goFuncPattern       = regexp.MustCompile(`^\s*func\s+(?:\([^)]*\)\s*)?([A-Za-z_][A-Za-z0-9_]*)\s*\(`)
	jsFuncPattern       = regexp.MustCompile(`^\s*(?:export\s+)?(?:async\s+)?function\s+([A-Za-z_][A-Za-z0-9_]*)\s*\(`)
	jsConstFuncPattern  = regexp.MustCompile(`^\s*(?:export\s+)?const\s+([A-Za-z_][A-Za-z0-9_]*)\s*=\s*(?:async\s*)?\(`)
	goIgnoredErrPattern = regexp.MustCompile(`_\s*=\s*[A-Za-z0-9_\.]+\([^\n]*\)`)
	emptyCatchPattern   = regexp.MustCompile(`catch\s*\([^)]*\)\s*\{\s*\}`)
	identifierPattern   = regexp.MustCompile(`\b[A-Za-z_][A-Za-z0-9_]*\b`)
)

func ScanProject(projectPath string, maxIssues int) (Report, error) {
	return ScanProjectWithProgress(projectPath, maxIssues, nil)
}

func ScanProjectWithProgress(projectPath string, maxIssues int, onProgress ProgressCallback) (Report, error) {
	if strings.TrimSpace(projectPath) == "" {
		return Report{}, fmt.Errorf("project path required")
	}
	if maxIssues <= 0 {
		maxIssues = 120
	}
	if onProgress != nil {
		onProgress(ScanProgress{
			ProjectPath:     projectPath,
			Phase:           "generated-map",
			Current:         0,
			Total:           1,
			Percent:         0,
			CurrentFunction: "ScanProjectWithProgress",
			Section:         "start",
			Message:         "Preparing generated file map",
		})
	}

	index, docText, err := buildProjectIndex(projectPath, onProgress)
	if err != nil {
		return Report{}, err
	}
	if onProgress != nil {
		onProgress(ScanProgress{
			ProjectPath:     projectPath,
			Phase:           "report",
			Current:         1,
			Total:           1,
			Percent:         100,
			CurrentFunction: "buildReportFromIndex",
			Section:         "report",
			Message:         "Finalizing quality report",
		})
	}
	return buildReportFromIndex(projectPath, docText, index, maxIssues), nil
}

func buildProjectIndex(projectPath string, onProgress ProgressCallback) (projectIndex, string, error) {
	docText := strings.ToLower(readDocs(projectPath))
	generatedIndex, mapTotal, mapChanged, err := prepareGeneratedIndex(projectPath, onProgress)
	if err != nil {
		return projectIndex{}, docText, err
	}
	if onProgress != nil && mapTotal > 0 {
		mapStatus := "cached"
		if mapChanged {
			mapStatus = "completed"
		}
		onProgress(ScanProgress{
			ProjectPath:     projectPath,
			Phase:           "generated-map",
			Current:         mapTotal,
			Total:           mapTotal,
			Percent:         100,
			CurrentFunction: "prepareGeneratedIndex",
			Section:         "generated-map",
			Message:         fmt.Sprintf("Generated file map %s", mapStatus),
		})
	}

	sources, err := gatherSourceFiles(projectPath, generatedIndex)
	if err != nil {
		return projectIndex{}, docText, err
	}

	index := loadProjectIndex(projectPath)
	if index.Version != qualityIndexVersion || index.Files == nil {
		index = projectIndex{
			Version:     qualityIndexVersion,
			ProjectPath: projectPath,
			Files:       make(map[string]fileIndexEntry),
		}
	}

	seen := make(map[string]bool, len(sources))
	changed := false
	total := len(sources)
	for _, source := range sources {
		seen[source.RelPath] = true
		cached, ok := index.Files[source.RelPath]
		processed := len(seen)
		if onProgress != nil && total > 0 {
			onProgress(ScanProgress{
				ProjectPath:     projectPath,
				Phase:           "scan",
				Current:         processed,
				Total:           total,
				Percent:         (float64(processed) / float64(total)) * 100,
				CurrentFile:     source.RelPath,
				CurrentFunction: "analyzeFile",
				Section:         "scan",
				Message:         "Scanning source file",
			})
		}
		if ok && cached.ModTimeUnixNano == source.ModTimeUnixNano && cached.Size == source.Size {
			continue
		}
		entry, analyzeErr := analyzeFile(source)
		if analyzeErr != nil {
			delete(index.Files, source.RelPath)
			changed = true
			continue
		}
		index.Files[source.RelPath] = entry
		changed = true
	}

	for relPath := range index.Files {
		if !seen[relPath] {
			delete(index.Files, relPath)
			changed = true
		}
	}

	index.ProjectPath = projectPath
	if changed {
		index.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
		saveProjectIndex(projectPath, index)
	}
	return index, docText, nil
}

func buildReportFromIndex(projectPath, docText string, index projectIndex, maxIssues int) Report {
	issues := make([]Issue, 0, 128)
	identifierCounts := make(map[string]int, 512)
	symbols := make([]symbolDef, 0, 256)
	chunks := make(map[string]chunkLoc)
	duplicateHashes := make(map[string]bool)

	paths := make([]string, 0, len(index.Files))
	for path := range index.Files {
		paths = append(paths, path)
	}
	sort.Strings(paths)

	for _, relPath := range paths {
		entry := index.Files[relPath]
		issues = append(issues, entry.BaseIssues...)
		for token, count := range entry.IdentifierCounts {
			identifierCounts[token] += count
		}
		symbols = append(symbols, entry.Symbols...)
		if !entry.IsTest && len(entry.BaseName) > 2 && !strings.Contains(docText, entry.BaseName) {
			issues = append(issues, Issue{
				ID:         stableID(relPath, 1, "doc-gap"),
				Severity:   "low",
				Category:   "documentation-gap",
				Message:    "File appears missing from user-facing documentation and architecture notes.",
				File:       relPath,
				Line:       1,
				Suggestion: "Reference this module in WORKING_BEHAVIORS or architecture notes if it represents shipped behavior.",
			})
		}
		for _, chunk := range entry.Chunks {
			if first, ok := chunks[chunk.Hash]; ok {
				if first.File == relPath || duplicateHashes[chunk.Hash] {
					continue
				}
				duplicateHashes[chunk.Hash] = true
				issues = append(issues, Issue{
					ID:         stableID(relPath, chunk.Line, "duplicate"),
					Severity:   "medium",
					Category:   "duplicate-content",
					Message:    fmt.Sprintf("Repeated code chunk matches %s:%d; candidate for DRY extraction.", first.File, first.Line),
					File:       relPath,
					Line:       chunk.Line,
					Suggestion: "Extract shared logic into a helper/module to reduce divergence risk.",
				})
				continue
			}
			chunks[chunk.Hash] = chunkLoc{File: relPath, Line: chunk.Line}
		}
	}

	issues = append(issues, deadCodeHeuristics(symbols, identifierCounts)...)
	issues = dedupeIssues(issues)
	sort.SliceStable(issues, func(i, j int) bool {
		if severityRank(issues[i].Severity) != severityRank(issues[j].Severity) {
			return severityRank(issues[i].Severity) < severityRank(issues[j].Severity)
		}
		if issues[i].File != issues[j].File {
			return issues[i].File < issues[j].File
		}
		return issues[i].Line < issues[j].Line
	})
	if len(issues) > maxIssues {
		issues = issues[:maxIssues]
	}

	report := Report{
		ProjectPath: projectPath,
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		IssueCount:  len(issues),
		Issues:      issues,
	}
	for _, issue := range issues {
		switch issue.Severity {
		case "high":
			report.HighCount++
		case "medium":
			report.MediumCount++
		default:
			report.LowCount++
		}
	}
	return report
}

func gatherSourceFiles(projectPath string, generatedIndex map[string]bool) ([]sourceFileInfo, error) {
	files := make([]sourceFileInfo, 0, 512)
	projectRoot := filepath.Clean(projectPath)
	err := filepath.WalkDir(projectPath, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		cleanPath := filepath.Clean(path)
		if generatedIndex[cleanPath] {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			name := d.Name()
			switch name {
			case ".git", "node_modules", "target", "dist", "build", "coverage", "llvm-cov-target", "rust-analyzer", ".cache", ".venv":
				return filepath.SkipDir
			}
			return nil
		}
		ext := strings.ToLower(filepath.Ext(d.Name()))
		switch ext {
		case ".go", ".ts", ".tsx", ".js", ".jsx", ".rs", ".py", ".sh":
			info, statErr := d.Info()
			if statErr != nil {
				return nil
			}
			rel := filepath.ToSlash(strings.TrimPrefix(path, projectRoot+string(filepath.Separator)))
			files = append(files, sourceFileInfo{AbsPath: path, RelPath: rel, ModTimeUnixNano: info.ModTime().UnixNano(), Size: info.Size()})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return files, nil
}

func analyzeFile(source sourceFileInfo) (fileIndexEntry, error) {
	contentBytes, err := os.ReadFile(source.AbsPath)
	if err != nil {
		return fileIndexEntry{}, err
	}
	content := string(contentBytes)
	lines := strings.Split(content, "\n")
	baseIssues := make([]Issue, 0, 8)
	baseIssues = append(baseIssues, largeUncommentedBlocks(source.RelPath, lines)...)
	funcIssues, defs := functionComplexityIssues(source.RelPath, lines)
	baseIssues = append(baseIssues, funcIssues...)
	baseIssues = append(baseIssues, principleViolations(source.RelPath, content)...)

	return fileIndexEntry{
		Path:             source.RelPath,
		ModTimeUnixNano:  source.ModTimeUnixNano,
		Size:             source.Size,
		BaseName:         strings.TrimSuffix(strings.ToLower(filepath.Base(source.RelPath)), strings.ToLower(filepath.Ext(source.RelPath))),
		IsTest:           strings.Contains(source.RelPath, "/test") || strings.Contains(source.RelPath, "_test."),
		Symbols:          defs,
		IdentifierCounts: countIdentifiers(content),
		Chunks:           collectChunkRecords(lines),
		BaseIssues:       baseIssues,
	}, nil
}

func loadProjectIndex(projectPath string) projectIndex {
	path := qualityIndexPath(projectPath)
	content, err := os.ReadFile(path)
	if err != nil {
		return projectIndex{Version: qualityIndexVersion, ProjectPath: projectPath, Files: make(map[string]fileIndexEntry)}
	}
	var index projectIndex
	if json.Unmarshal(content, &index) != nil || index.Files == nil {
		return projectIndex{Version: qualityIndexVersion, ProjectPath: projectPath, Files: make(map[string]fileIndexEntry)}
	}
	return index
}

func saveProjectIndex(projectPath string, index projectIndex) {
	engineDir := filepath.Join(projectPath, ".engine")
	if os.MkdirAll(engineDir, 0o755) != nil {
		return
	}
	content, err := json.MarshalIndent(index, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(qualityIndexPath(projectPath), content, 0o644)
}

func qualityIndexPath(projectPath string) string {
	return filepath.Join(projectPath, ".engine", "quality-index.json")
}

type generatedCache struct {
	UpdatedAt string   `json:"updatedAt"`
	ProcessID int      `json:"processId,omitempty"`
	Paths     []string `json:"paths"`
}

func generatedFilesCachePath(projectPath string) string {
	return filepath.Join(projectPath, ".engine", "generated-files-cache.json")
}

func loadGeneratedIndex(projectPath string) (map[string]bool, bool, bool) {
	idx := make(map[string]bool)
	data, err := os.ReadFile(generatedFilesCachePath(projectPath))
	if err != nil {
		return idx, false, false
	}
	var cache generatedCache
	if err := json.Unmarshal(data, &cache); err != nil {
		return idx, false, false
	}
	for _, p := range cache.Paths {
		clean := filepath.Clean(strings.TrimSpace(p))
		if clean != "" && looksGeneratedPath(clean) {
			idx[clean] = true
		}
	}
	complete := strings.TrimSpace(cache.UpdatedAt) != "" && cache.ProcessID == 0
	return idx, true, complete
}

func saveGeneratedIndex(projectPath string, idx map[string]bool) error {
	paths := make([]string, 0, len(idx))
	for p := range idx {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	cache := generatedCache{
		UpdatedAt: time.Now().UTC().Format(time.RFC3339),
		Paths:     paths,
	}
	data, err := json.MarshalIndent(cache, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(projectPath, ".engine"), 0o755); err != nil {
		return err
	}
	return os.WriteFile(generatedFilesCachePath(projectPath), data, 0o644)
}

func prepareGeneratedIndex(projectPath string, onProgress ProgressCallback) (map[string]bool, int, bool, error) {
	idx, _, complete := loadGeneratedIndex(projectPath)
	if complete {
		return idx, 1, false, nil
	}

	allPaths, err := collectProjectPaths(projectPath)
	if err != nil {
		return idx, 0, false, err
	}
	total := len(allPaths)
	if total == 0 {
		total = 1
	}

	projectRoot := filepath.Clean(projectPath)
	lastPercent := -1
	for i, absPath := range allPaths {
		if looksGeneratedPath(absPath) {
			idx[absPath] = true
			addGeneratedParents(idx, projectRoot, absPath)
		}
		if onProgress != nil {
			percent := int((float64(i+1) / float64(total)) * 100)
			if percent != lastPercent {
				lastPercent = percent
				onProgress(ScanProgress{
					ProjectPath:     projectPath,
					Phase:           "generated-map",
					Current:         i + 1,
					Total:           total,
					Percent:         float64(percent),
					CurrentFile:     filepath.ToSlash(strings.TrimPrefix(absPath, projectRoot+string(filepath.Separator))),
					CurrentFunction: "prepareGeneratedIndex",
					Section:         "generated-map",
					Message:         "Completing generated files map",
				})
			}
		}
	}

	if err := saveGeneratedIndex(projectPath, idx); err != nil {
		return idx, total, false, err
	}
	return idx, total, true, nil
}

func collectProjectPaths(projectPath string) ([]string, error) {
	projectRoot := filepath.Clean(projectPath)
	all := make([]string, 0, 1024)
	err := filepath.WalkDir(projectRoot, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			if isGeneratedDirName(d.Name()) {
				all = append(all, filepath.Clean(path))
				return filepath.SkipDir
			}
		}
		all = append(all, filepath.Clean(path))
		return nil
	})
	if err != nil {
		return nil, err
	}
	return all, nil
}

func isGeneratedDirName(name string) bool {
	switch name {
	case "node_modules", "dist", "build", "target", "coverage", ".cache", "out", "tmp", "bin", "vendor", "storybook-static", "llvm-cov-target", "rust-analyzer", ".engine", ".venv":
		return true
	default:
		return false
	}
}

func looksGeneratedPath(absPath string) bool {
	generatedNames := map[string]bool{
		"node_modules":     true,
		"dist":             true,
		"build":            true,
		"target":           true,
		"coverage":         true,
		".cache":           true,
		"out":              true,
		"tmp":              true,
		"bin":              true,
		"vendor":           true,
		"storybook-static": true,
		"llvm-cov-target":  true,
		"rust-analyzer":    true,
		".engine":          true,
	}
	for _, part := range strings.Split(filepath.Clean(absPath), string(filepath.Separator)) {
		if generatedNames[part] {
			return true
		}
	}
	lower := strings.ToLower(absPath)
	return strings.HasSuffix(lower, ".log") || strings.HasSuffix(lower, ".tmp") || strings.HasSuffix(lower, ".profraw") || strings.HasSuffix(lower, ".profdata")
}

func addGeneratedParents(idx map[string]bool, projectRoot, absPath string) {
	for parent := filepath.Clean(absPath); parent != projectRoot && parent != "." && parent != string(filepath.Separator); parent = filepath.Dir(parent) {
		idx[parent] = true
	}
}

func countIdentifiers(content string) map[string]int {
	counts := make(map[string]int, 256)
	for _, token := range identifierPattern.FindAllString(content, -1) {
		counts[token]++
	}
	return counts
}

func collectChunkRecords(lines []string) []chunkRecord {
	normalized := make([]string, 0, len(lines))
	lineMap := make([]int, 0, len(lines))
	for idx, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "/*") || strings.HasPrefix(trimmed, "*") {
			continue
		}
		normalized = append(normalized, strings.Join(strings.Fields(trimmed), " "))
		lineMap = append(lineMap, idx+1)
	}
	if len(normalized) < 8 {
		return nil
	}
	records := make([]chunkRecord, 0, len(normalized)-7)
	for i := 0; i+8 <= len(normalized); i++ {
		chunk := strings.Join(normalized[i:i+8], "\n")
		hash := sha1.Sum([]byte(chunk))
		records = append(records, chunkRecord{Hash: hex.EncodeToString(hash[:]), Line: lineMap[i]})
	}
	return records
}

func readDocs(projectPath string) string {
	paths := []string{
		filepath.Join(projectPath, ".github", "WORKING_BEHAVIORS.md"),
		filepath.Join(projectPath, "obsidian-vault", "Engine", "Architecture.md"),
		filepath.Join(projectPath, "obsidian-vault", "Engine", "Knowledge.md"),
	}
	parts := make([]string, 0, len(paths))
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err == nil {
			parts = append(parts, string(b))
		}
	}
	return strings.Join(parts, "\n")
}

func largeUncommentedBlocks(rel string, lines []string) []Issue {
	issues := make([]Issue, 0, 4)
	start := -1
	hasComment := false
	for idx, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			if start != -1 {
				length := idx - start
				if length >= 55 && !hasComment {
					issues = append(issues, Issue{
						ID:         stableID(rel, start+1, "long-block"),
						Severity:   "medium",
						Category:   "large-block-without-comment",
						Message:    fmt.Sprintf("Large code block (%d lines) has no guiding comments.", length),
						File:       rel,
						Line:       start + 1,
						Suggestion: "Split into smaller helpers and annotate non-obvious intent.",
					})
				}
				start = -1
				hasComment = false
			}
			continue
		}
		if start == -1 {
			start = idx
			hasComment = false
		}
		if strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "/*") || strings.HasPrefix(trimmed, "*") || strings.HasPrefix(trimmed, "#") {
			hasComment = true
		}
	}
	if start != -1 {
		length := len(lines) - start
		if length >= 55 && !hasComment {
			issues = append(issues, Issue{
				ID:         stableID(rel, start+1, "long-block"),
				Severity:   "medium",
				Category:   "large-block-without-comment",
				Message:    fmt.Sprintf("Large code block (%d lines) has no guiding comments.", length),
				File:       rel,
				Line:       start + 1,
				Suggestion: "Split into smaller helpers and annotate non-obvious intent.",
			})
		}
	}
	return issues
}

func functionComplexityIssues(rel string, lines []string) ([]Issue, []symbolDef) {
	issues := make([]Issue, 0, 8)
	symbols := make([]symbolDef, 0, 16)
	for idx := 0; idx < len(lines); idx++ {
		line := lines[idx]
		name := ""
		if m := goFuncPattern.FindStringSubmatch(line); len(m) == 2 {
			name = m[1]
		} else if m := jsFuncPattern.FindStringSubmatch(line); len(m) == 2 {
			name = m[1]
		} else if m := jsConstFuncPattern.FindStringSubmatch(line); len(m) == 2 {
			name = m[1]
		}
		if name == "" {
			continue
		}
		symbols = append(symbols, symbolDef{Name: name, File: rel, Line: idx + 1})

		span := functionSpan(lines, idx)
		if span >= 85 {
			issues = append(issues, Issue{
				ID:         stableID(rel, idx+1, "long-func"),
				Severity:   "medium",
				Category:   "cs-principle",
				Message:    fmt.Sprintf("Function %s spans %d lines; likely violating single responsibility.", name, span),
				File:       rel,
				Line:       idx + 1,
				Suggestion: "Extract focused helpers so each method has one clear responsibility.",
			})
		}
	}
	return issues, symbols
}

func functionSpan(lines []string, start int) int {
	depth := 0
	seenBrace := false
	for i := start; i < len(lines); i++ {
		line := lines[i]
		for _, ch := range line {
			switch ch {
			case '{':
				depth++
				seenBrace = true
			case '}':
				if depth > 0 {
					depth--
				}
			}
		}
		if seenBrace && depth == 0 {
			return i - start + 1
		}
	}
	return 0
}

func principleViolations(rel, content string) []Issue {
	issues := make([]Issue, 0, 4)
	if emptyCatchPattern.MatchString(content) {
		issues = append(issues, Issue{
			ID:         stableID(rel, 1, "empty-catch"),
			Severity:   "high",
			Category:   "cs-principle",
			Message:    "Empty catch block detected. Swallowing exceptions hides real failures.",
			File:       rel,
			Line:       1,
			Suggestion: "Handle the error or rethrow with context.",
		})
	}
	if goIgnoredErrPattern.MatchString(content) {
		issues = append(issues, Issue{
			ID:         stableID(rel, 1, "ignored-error"),
			Severity:   "medium",
			Category:   "cs-principle",
			Message:    "Ignored error assignment found. CS 3500 style requires explicit handling at boundaries.",
			File:       rel,
			Line:       1,
			Suggestion: "Handle returned errors explicitly or document why ignoring is safe.",
		})
	}
	return issues
}

func deadCodeHeuristics(symbols []symbolDef, identifierCounts map[string]int) []Issue {
	issues := make([]Issue, 0, 16)
	for _, sym := range symbols {
		if skipDeadCodeCandidate(sym.Name) {
			continue
		}
		count := identifierCounts[sym.Name]
		if count <= 1 {
			issues = append(issues, Issue{
				ID:         stableID(sym.File, sym.Line, "dead-code"),
				Severity:   "low",
				Category:   "dead-code",
				Message:    fmt.Sprintf("Symbol %s appears only once across the indexed project and may be dead code.", sym.Name),
				File:       sym.File,
				Line:       sym.Line,
				Suggestion: "Remove unused symbol or add usage/tests proving it is required.",
			})
		}
	}
	return issues
}

func RefreshProjectIndex(projectPath string) error {
	if strings.TrimSpace(projectPath) == "" {
		return fmt.Errorf("project path required")
	}
	_, _, err := buildProjectIndex(projectPath, nil)
	return err
}

func skipDeadCodeCandidate(name string) bool {
	if name == "main" || name == "init" {
		return true
	}
	if strings.HasPrefix(name, "Test") {
		return true
	}
	if len(name) < 3 {
		return true
	}
	return false
}

func stableID(file string, line int, salt string) string {
	sum := sha1.Sum([]byte(fmt.Sprintf("%s:%d:%s", file, line, salt)))
	return hex.EncodeToString(sum[:8])
}

func dedupeIssues(issues []Issue) []Issue {
	seen := make(map[string]bool)
	out := make([]Issue, 0, len(issues))
	for _, issue := range issues {
		key := issue.Category + "|" + issue.File + "|" + fmt.Sprintf("%d", issue.Line) + "|" + issue.Message
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, issue)
	}
	return out
}

func severityRank(severity string) int {
	switch severity {
	case "high":
		return 0
	case "medium":
		return 1
	default:
		return 2
	}
}
