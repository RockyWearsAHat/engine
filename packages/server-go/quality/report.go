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
	Rule       string `json:"rule,omitempty"`
	Severity   string `json:"severity"`
	Category   string `json:"category"`
	Message    string `json:"message"`
	File       string `json:"file"`
	Line       int    `json:"line"`
	Suggestion string `json:"suggestion,omitempty"`
	DocURL     string `json:"docUrl,omitempty"`
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
	Name   string
	File   string
	Line   int
	Public bool
}

type functionProfile struct {
	Name        string   `json:"name"`
	File        string   `json:"file"`
	Line        int      `json:"line"`
	Public      bool     `json:"public"`
	Calls       []string `json:"calls,omitempty"`
	DirectRisk  bool     `json:"directRisk,omitempty"`
	RiskyCallee string   `json:"riskyCallee,omitempty"`
}

type chunkLoc struct {
	File string
	Line int
}

type chunkLocExt struct {
	File            string
	Line            int
	DeclarationLike bool
}

type duplicatePairKey struct {
	LeftFile  string
	RightFile string
}

type duplicateMatch struct {
	LeftLine        int
	RightLine       int
	DeclarationLike bool
}

type duplicateRun struct {
	LeftFile        string
	RightFile       string
	LeftLine        int
	RightLine       int
	NormalizedLines int
	DeclarationLike bool
}

type chunkRecord struct {
	Hash            string `json:"hash"`
	Line            int    `json:"line"`
	Size            int    `json:"size"`
	DeclarationLike bool   `json:"declarationLike,omitempty"`
}

type fileIndexEntry struct {
	Path             string            `json:"path"`
	ModTimeUnixNano  int64             `json:"modTimeUnixNano"`
	Size             int64             `json:"size"`
	RawLines         int               `json:"rawLines"`
	BaseName         string            `json:"baseName"`
	IsTest           bool              `json:"isTest"`
	NormalizedLines  int               `json:"normalizedLines"`
	Symbols          []symbolDef       `json:"symbols"`
	FunctionProfiles []functionProfile `json:"functionProfiles,omitempty"`
	InterfaceNames   []string          `json:"interfaceNames,omitempty"`
	IdentifierCounts map[string]int    `json:"identifierCounts"`
	CSSClassDefs     []string          `json:"cssClassDefs,omitempty"`
	ClassReferences  map[string]int    `json:"classReferences,omitempty"`
	Chunks           []chunkRecord     `json:"chunks"`
	ShapeChunks      []chunkRecord     `json:"shapeChunks,omitempty"`
	BaseIssues       []Issue           `json:"baseIssues"`
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

type functionSignature struct {
	Name        string
	Public      bool
	SpanMode    string
	StartIndent int
}

type ruleDefinition struct {
	ID              string
	Category        string
	DefaultSeverity string
	DocURL          string
}

const qualityIndexVersion = 21
const duplicateChunkMinLines = 1
const structuralDuplicateChunkMinLines = 3
const duplicateIssuesPerFileCap = 12
const behavioralIssuesPerFileCap = 3
const sourceLongFileThresholdLines = 700
const testLongFileThresholdLines = 900
const qualityRuleDocsBase = "https://github.com/fallow-rs/fallow"

const (
	ruleDuplicateLargest       = "duplicate.largest-overlap"
	ruleDuplicateStructural    = "duplicate.structural-overlap"
	ruleDuplicateCompaction    = "duplicate.compaction"
	ruleDocumentationFile      = "documentation.file-reference-gap"
	ruleDocumentationInterface = "documentation.interface-reference-gap"
	ruleDocumentationPublicFn  = "documentation.public-function-comment-gap"
	ruleDocumentationGroupFile = "documentation.grouped-file-reference-gap"
	ruleDocumentationGroupFn   = "documentation.grouped-public-function-comment-gap"
	ruleDocumentationGroupIntf = "documentation.grouped-interface-reference-gap"
	ruleCSSUnused              = "css.unused-selector"
	ruleDeadCodeSymbol         = "dead-code.unreferenced-symbol"
	ruleLargeUncommentedBlock  = "maintainability.large-uncommented-block"
	ruleLongFile               = "maintainability.long-file"
	ruleTestSourceColocation   = "maintainability.test-source-colocation"
	ruleLongFunction           = "cs.single-responsibility.long-function"
	ruleEmptyCatch             = "cs.error-handling.empty-catch"
	ruleIgnoredError           = "cs.error-handling.ignored-error-assignment"
	ruleDiscardedResult        = "cs.error-handling.discarded-result"
	rulePythonEmptyExcept      = "cs.error-handling.python-empty-except"
	ruleBehavioralShiftChain   = "behavioral-shift.side-effect-chain"
	ruleBehavioralShiftCompact = "behavioral-shift.compaction"
)

var ruleCatalog = map[string]ruleDefinition{
	ruleDuplicateLargest:       {ID: ruleDuplicateLargest, Category: "duplicate-content", DefaultSeverity: "medium", DocURL: qualityRuleDocsBase},
	ruleDuplicateStructural:    {ID: ruleDuplicateStructural, Category: "duplicate-content", DefaultSeverity: "medium", DocURL: qualityRuleDocsBase},
	ruleDuplicateCompaction:    {ID: ruleDuplicateCompaction, Category: "duplicate-content", DefaultSeverity: "low", DocURL: qualityRuleDocsBase},
	ruleDocumentationFile:      {ID: ruleDocumentationFile, Category: "documentation-gap", DefaultSeverity: "low", DocURL: qualityRuleDocsBase},
	ruleDocumentationInterface: {ID: ruleDocumentationInterface, Category: "documentation-gap", DefaultSeverity: "medium", DocURL: qualityRuleDocsBase},
	ruleDocumentationPublicFn:  {ID: ruleDocumentationPublicFn, Category: "documentation-gap", DefaultSeverity: "medium", DocURL: qualityRuleDocsBase},
	ruleDocumentationGroupFile: {ID: ruleDocumentationGroupFile, Category: "documentation-gap", DefaultSeverity: "low", DocURL: qualityRuleDocsBase},
	ruleDocumentationGroupFn:   {ID: ruleDocumentationGroupFn, Category: "documentation-gap", DefaultSeverity: "medium", DocURL: qualityRuleDocsBase},
	ruleDocumentationGroupIntf: {ID: ruleDocumentationGroupIntf, Category: "documentation-gap", DefaultSeverity: "medium", DocURL: qualityRuleDocsBase},
	ruleCSSUnused:              {ID: ruleCSSUnused, Category: "css-usage", DefaultSeverity: "low", DocURL: qualityRuleDocsBase},
	ruleDeadCodeSymbol:         {ID: ruleDeadCodeSymbol, Category: "dead-code", DefaultSeverity: "low", DocURL: qualityRuleDocsBase},
	ruleLargeUncommentedBlock:  {ID: ruleLargeUncommentedBlock, Category: "large-block-without-comment", DefaultSeverity: "medium", DocURL: qualityRuleDocsBase},
	ruleLongFile:               {ID: ruleLongFile, Category: "maintainability", DefaultSeverity: "low", DocURL: qualityRuleDocsBase},
	ruleTestSourceColocation:   {ID: ruleTestSourceColocation, Category: "maintainability", DefaultSeverity: "low", DocURL: qualityRuleDocsBase},
	ruleLongFunction:           {ID: ruleLongFunction, Category: "cs-principle", DefaultSeverity: "medium", DocURL: qualityRuleDocsBase},
	ruleEmptyCatch:             {ID: ruleEmptyCatch, Category: "cs-principle", DefaultSeverity: "high", DocURL: qualityRuleDocsBase},
	ruleIgnoredError:           {ID: ruleIgnoredError, Category: "cs-principle", DefaultSeverity: "medium", DocURL: qualityRuleDocsBase},
	ruleDiscardedResult:        {ID: ruleDiscardedResult, Category: "cs-principle", DefaultSeverity: "medium", DocURL: qualityRuleDocsBase},
	rulePythonEmptyExcept:      {ID: rulePythonEmptyExcept, Category: "cs-principle", DefaultSeverity: "high", DocURL: qualityRuleDocsBase},
	ruleBehavioralShiftChain:   {ID: ruleBehavioralShiftChain, Category: "behavioral-shift", DefaultSeverity: "medium", DocURL: qualityRuleDocsBase},
	ruleBehavioralShiftCompact: {ID: ruleBehavioralShiftCompact, Category: "behavioral-shift", DefaultSeverity: "low", DocURL: qualityRuleDocsBase},
}

var (
	goFuncPattern            = regexp.MustCompile(`^\s*func\s+(?:\([^)]*\)\s*)?([A-Za-z_][A-Za-z0-9_]*)\s*\(`)
	jsFuncPattern            = regexp.MustCompile(`^\s*(?:export\s+(?:default\s+)?)?(?:async\s+)?function\s+([A-Za-z_][A-Za-z0-9_]*)\s*\(`)
	jsConstFuncPattern       = regexp.MustCompile(`^\s*(?:export\s+(?:default\s+)?)?const\s+([A-Za-z_][A-Za-z0-9_]*)\s*=\s*(?:async\s*)?\(`)
	rustFuncPattern          = regexp.MustCompile(`^\s*(?:pub(?:\([^)]*\))?\s+)?(?:async\s+)?fn\s+([A-Za-z_][A-Za-z0-9_]*)\s*\(`)
	pythonFuncPattern        = regexp.MustCompile(`^\s*(?:async\s+)?def\s+([A-Za-z_][A-Za-z0-9_]*)\s*\(`)
	javaLikeFuncPattern      = regexp.MustCompile(`^\s*(?:(?:public|private|protected|internal|static|final|virtual|override|abstract|synchronized|native|unsafe|async|sealed|partial|readonly)\s+)+[A-Za-z_][A-Za-z0-9_<>,\[\]\.\?]*\s+([A-Za-z_][A-Za-z0-9_]*)\s*\([^;]*\)\s*(?:\{|=>)`)
	kotlinFuncPattern        = regexp.MustCompile(`^\s*(?:(?:public|private|protected|internal|open|override|suspend|inline|tailrec|operator|infix|external|abstract|final|sealed|data|expect|actual)\s+)*fun\s+([A-Za-z_][A-Za-z0-9_]*)\s*\(`)
	swiftFuncPattern         = regexp.MustCompile(`^\s*(?:(?:public|private|fileprivate|internal|open|final|override|static|class|mutating|nonmutating|convenience|required)\s+)*func\s+([A-Za-z_][A-Za-z0-9_]*)\s*\(`)
	goIgnoredErrPattern      = regexp.MustCompile(`(?mi)^\s*_\s*=\s*(?:err|[A-Za-z_][A-Za-z0-9_]*err)\s*$`)
	discardAssignmentPattern = regexp.MustCompile(`^\s*(?:(?:const|let|var)\s+)?([A-Za-z_][A-Za-z0-9_]*)\s*[:=]\s*(?:await\s+)?([A-Za-z_][A-Za-z0-9_\.]*)\s*\(`)
	emptyCatchPattern        = regexp.MustCompile(`catch\s*\([^)]*\)\s*\{\s*\}`)
	identifierPattern        = regexp.MustCompile(`\b[A-Za-z_][A-Za-z0-9_]*\b`)
	goInterfacePattern       = regexp.MustCompile(`^\s*type\s+([A-Za-z_][A-Za-z0-9_]*)\s+interface\s*\{`)
	tsInterfacePattern       = regexp.MustCompile(`^\s*(?:export\s+)?interface\s+([A-Za-z_][A-Za-z0-9_]*)\b`)
	cssClassSelectorPattern  = regexp.MustCompile(`(?:^|[\s,{>+~])\.([A-Za-z_-][A-Za-z0-9_-]*)`)
	classAttrPattern         = regexp.MustCompile(`(?i)\bclass(?:Name)?\s*=\s*(?:"([^"]*)"|'([^']*)')`)
	classTemplateAttrPattern = regexp.MustCompile("(?i)\\bclass(?:Name)?\\s*=\\s*\\{\\s*`([^`]*)`\\s*\\}")
	templateExprPattern      = regexp.MustCompile(`\$\{[^}]*\}`)
	cssModuleRefPattern      = regexp.MustCompile(`\b(?:styles|style)\.([A-Za-z_][A-Za-z0-9_-]*)\b`)
	pythonExceptLinePattern  = regexp.MustCompile(`^\s*except(?:\s+[^:]+)?\s*:\s*$`)
	pythonCommentLinePattern = regexp.MustCompile(`^\s*#`)
	genericCallPattern       = regexp.MustCompile(`\b([A-Za-z_][A-Za-z0-9_]*)\s*\(`)
	memberCallPattern        = regexp.MustCompile(`\.\s*([A-Za-z_][A-Za-z0-9_]*)\s*\(`)
)

func ScanProject(projectPath string, maxIssues int) (Report, error) {
	return ScanProjectWithProgress(projectPath, maxIssues, nil)
}

func ScanProjectWithProgress(projectPath string, maxIssues int, onProgress ProgressCallback) (Report, error) {
	if strings.TrimSpace(projectPath) == "" {
		return Report{}, fmt.Errorf("project path required")
	}
	// maxIssues <= 0 means uncapped scan.
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
	classReferenceCounts := make(map[string]int, 512)
	duplicateIssueCountByFile := make(map[string]int, 64)
	duplicateSuppressedByFile := make(map[string]int, 64)
	symbols := make([]symbolDef, 0, 256)
	chunks := make(map[string][]chunkLocExt)
	matchesByPair := make(map[duplicatePairKey][]duplicateMatch)

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
		for className, count := range entry.ClassReferences {
			if className == "" {
				continue
			}
			classReferenceCounts[className] += count
		}
		symbols = append(symbols, entry.Symbols...)
		hasDocGapIssue := fileHasCategory(entry.BaseIssues, "documentation-gap")
		if !hasDocGapIssue && fileNeedsDocReference(entry) && !documentationMentionsFile(docText, entry) {
			issues = append(issues, issueWithRule(ruleDocumentationFile, relPath, 1, "doc-gap", "File is not referenced anywhere in workspace documentation (.md, .mdx, .txt, or .dx).", "Add a short note in behavior, architecture, or module docs if this file is user-visible."))
		}
		missingInterfaces := undocumentedInterfaces(docText, entry.InterfaceNames)
		if len(missingInterfaces) > 0 {
			preview := strings.Join(missingInterfaces, ", ")
			if len(preview) > 180 {
				preview = preview[:177] + "..."
			}
			issues = append(issues, issueWithRule(ruleDocumentationInterface, relPath, 1, "interface-doc-gap", fmt.Sprintf("%d public interfaces in this file are missing workspace documentation references: %s", len(missingInterfaces), preview), "Document these interfaces in module-level docs (.md/.mdx/.txt/.dx) with responsibilities and usage expectations."))
		}
		for _, chunk := range entry.Chunks {
			for _, first := range chunks[chunk.Hash] {
				if first.File == relPath {
					continue
				}
				pair := duplicatePairKey{LeftFile: first.File, RightFile: relPath}
				matchesByPair[pair] = append(matchesByPair[pair], duplicateMatch{
					LeftLine:        first.Line,
					RightLine:       chunk.Line,
					DeclarationLike: first.DeclarationLike || chunk.DeclarationLike,
				})
			}
			chunks[chunk.Hash] = append(chunks[chunk.Hash], chunkLocExt{File: relPath, Line: chunk.Line, DeclarationLike: chunk.DeclarationLike})
		}
	}

	issues = append(issues, behavioralShiftIssues(index, paths)...)
	issues = append(issues, longFileIssues(index, paths)...)
	issues = append(issues, testSourceColocationIssues(index, paths)...)

	for _, run := range collectLargestDuplicateRuns(matchesByPair) {
		leftNormalized := index.Files[run.LeftFile].NormalizedLines
		rightNormalized := index.Files[run.RightFile].NormalizedLines
		overlapBase := minInt(leftNormalized, rightNormalized)
		overlapPct := 0.0
		if overlapBase > 0 {
			overlapPct = (float64(run.NormalizedLines) / float64(overlapBase)) * 100.0
		}

		// One-line and tiny two-line overlaps are usually incidental boilerplate,
		// not actionable duplication.
		if run.NormalizedLines <= 1 {
			continue
		}
		if run.NormalizedLines == 2 && overlapPct < 30.0 {
			continue
		}

		severity := classifyDuplicateSeverity(run.NormalizedLines, overlapPct, run.DeclarationLike)
		message := fmt.Sprintf("Duplicate code detected: this file repeats %d normalized line(s) from %s:%d (%.1f%% overlap of the smaller file). Consolidate the shared block into one reusable helper/module.", run.NormalizedLines, run.LeftFile, run.LeftLine, overlapPct)
		suggestion := "Extract the shared logic into a helper/module to avoid divergence."
		if severity == "high" {
			severity = "high"
			message = fmt.Sprintf("High-risk duplicate code: this file repeats %d normalized line(s) from %s:%d (%.1f%% overlap of the smaller file). Fix by centralizing the repeated block into one source of truth.", run.NormalizedLines, run.LeftFile, run.LeftLine, overlapPct)
			suggestion = "Move the shared block into one reusable definition and reference it from both files."
		} else if severity == "low" {
			message = fmt.Sprintf("Low-confidence duplicate snippet: this file repeats %d normalized line(s) from %s:%d (%.1f%% overlap of the smaller file). Verify whether this is shared intent or incidental similarity.", run.NormalizedLines, run.LeftFile, run.LeftLine, overlapPct)
			suggestion = "If intentional, keep it. If accidental duplication is growing, extract a helper before divergence starts."
		}
		issue := issueWithRuleSeverity(ruleDuplicateLargest, severity, run.RightFile, run.RightLine, "duplicate-largest|"+run.LeftFile, message, suggestion)
		if duplicateIssueCountByFile[run.RightFile] < duplicateIssuesPerFileCap {
			issues = append(issues, issue)
			duplicateIssueCountByFile[run.RightFile]++
		} else {
			duplicateSuppressedByFile[run.RightFile]++
		}
	}

	for file, suppressed := range duplicateSuppressedByFile {
		if suppressed <= 0 {
			continue
		}
		severity := "low"
		if suppressed > 100 {
			severity = "medium"
		}
		issues = append(issues, issueWithRuleSeverity(ruleDuplicateCompaction, severity, file, 1, "duplicate-compaction", fmt.Sprintf("%d additional duplicate matches were compacted for this file to keep the report actionable.", suppressed), "Review top duplicate findings first; resolve repeated root blocks and re-scan to reduce this compacted count."))
	}

	for _, relPath := range paths {
		entry := index.Files[relPath]
		if len(entry.CSSClassDefs) == 0 {
			continue
		}
		for _, className := range entry.CSSClassDefs {
			if className == "" || classReferenceCounts[className] > 0 {
				continue
			}
			issues = append(issues, issueWithRule(ruleCSSUnused, relPath, 1, "css-unused|"+className, fmt.Sprintf("CSS selector .%s was not matched by any class/className usage across indexed source files.", className), "If this selector is intentionally dynamic, keep it; otherwise remove it or align JSX/HTML class names."))
		}
	}

	issues = append(issues, deadCodeHeuristics(symbols, identifierCounts)...)
	issues = applyRuleEnginePolicy(projectPath, issues)
	issues = dedupeIssues(issues)
	issues = compressDocumentationGapIssues(issues)
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
		if maxIssues > 0 {
			issues = issues[:maxIssues]
		}
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
		case ".go", ".ts", ".tsx", ".js", ".jsx", ".rs", ".py", ".sh", ".css", ".scss", ".sass", ".less", ".java", ".kt", ".kts", ".swift", ".cs", ".cpp", ".c", ".hpp", ".h", ".php":
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
	ext := strings.ToLower(filepath.Ext(source.RelPath))
	baseIssues := make([]Issue, 0, 8)
	baseIssues = append(baseIssues, largeUncommentedBlocks(source.RelPath, lines)...)
	funcIssues, defs := functionComplexityIssues(source.RelPath, lines)
	baseIssues = append(baseIssues, funcIssues...)
	baseIssues = append(baseIssues, principleViolations(source.RelPath, lines, content)...)
	normalizedLines, _ := normalizedLinesOnly(lines)
	profiles := functionCallProfiles(source.RelPath, lines)

	return fileIndexEntry{
		Path:             source.RelPath,
		ModTimeUnixNano:  source.ModTimeUnixNano,
		Size:             source.Size,
		RawLines:         len(lines),
		BaseName:         strings.TrimSuffix(strings.ToLower(filepath.Base(source.RelPath)), strings.ToLower(filepath.Ext(source.RelPath))),
		IsTest:           isTestSourcePath(source.RelPath),
		NormalizedLines:  len(normalizedLines),
		Symbols:          defs,
		FunctionProfiles: profiles,
		InterfaceNames:   collectPublicInterfaceNames(lines),
		IdentifierCounts: countIdentifiers(content),
		CSSClassDefs:     collectCSSClassDefinitions(content, ext),
		ClassReferences:  collectClassReferences(content),
		Chunks:           collectChunkRecords(lines),
		BaseIssues:       baseIssues,
	}, nil
}

func collectCSSClassDefinitions(content, ext string) []string {
	if ext != ".css" && ext != ".scss" && ext != ".sass" && ext != ".less" {
		return nil
	}
	defs := make([]string, 0, 32)
	seen := make(map[string]bool)
	for _, match := range cssClassSelectorPattern.FindAllStringSubmatch(content, -1) {
		if len(match) < 2 {
			continue
		}
		normalized := normalizeClassToken(match[1])
		if normalized == "" || seen[normalized] {
			continue
		}
		seen[normalized] = true
		defs = append(defs, normalized)
	}
	sort.Strings(defs)
	return defs
}

func collectClassReferences(content string) map[string]int {
	refs := make(map[string]int)
	for _, match := range classAttrPattern.FindAllStringSubmatch(content, -1) {
		for i := 1; i < len(match); i++ {
			addClassTokens(match[i], refs)
		}
	}
	for _, match := range classTemplateAttrPattern.FindAllStringSubmatch(content, -1) {
		if len(match) < 2 {
			continue
		}
		staticPortion := templateExprPattern.ReplaceAllString(match[1], " ")
		addClassTokens(staticPortion, refs)
	}
	for _, match := range cssModuleRefPattern.FindAllStringSubmatch(content, -1) {
		if len(match) < 2 {
			continue
		}
		token := normalizeClassToken(match[1])
		if token == "" {
			continue
		}
		refs[token]++
	}
	return refs
}

func addClassTokens(raw string, refs map[string]int) {
	for _, token := range strings.Fields(raw) {
		normalized := normalizeClassToken(token)
		if normalized == "" {
			continue
		}
		refs[normalized]++
	}
}

func normalizeClassToken(token string) string {
	token = strings.TrimSpace(token)
	if token == "" {
		return ""
	}
	var b strings.Builder
	b.Grow(len(token))
	for _, r := range token {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r + ('a' - 'A'))
		}
	}
	return b.String()
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
	if err := os.WriteFile(qualityIndexPath(projectPath), content, 0o644); err != nil {
		return
	}
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

func countIdentifiers(content string) map[string]int {
	counts := make(map[string]int, 256)
	for _, token := range identifierPattern.FindAllString(content, -1) {
		counts[token]++
	}
	return counts
}

func collectChunkRecords(lines []string) []chunkRecord {
	normalized, lineMap := normalizedLinesOnly(lines)
	if len(normalized) < duplicateChunkMinLines {
		return nil
	}
	records := make([]chunkRecord, 0, len(normalized)-duplicateChunkMinLines+1)
	for i := 0; i+duplicateChunkMinLines <= len(normalized); i++ {
		if chunkLowSignal(normalized[i : i+duplicateChunkMinLines]) {
			continue
		}
		chunk := strings.Join(normalized[i:i+duplicateChunkMinLines], "\n")
		hash := sha1.Sum([]byte(chunk))
		records = append(records, chunkRecord{Hash: hex.EncodeToString(hash[:]), Line: lineMap[i], Size: duplicateChunkMinLines, DeclarationLike: chunkContainsDeclarationSignal(normalized[i : i+duplicateChunkMinLines])})
	}
	return records
}

func chunkLowSignal(lines []string) bool {
	for _, line := range lines {
		if !lineLowSignal(line) {
			return false
		}
	}
	return true
}

func lineLowSignal(line string) bool {
	lower := strings.ToLower(strings.TrimSpace(line))
	if lower == "" {
		return true
	}
	if strings.HasPrefix(lower, "package ") || strings.HasPrefix(lower, "import ") || strings.HasPrefix(lower, "export ") {
		return true
	}
	tokens := identifierPattern.FindAllString(lower, -1)
	if len(tokens) == 0 {
		return true
	}
	nonNoise := 0
	for _, token := range tokens {
		if duplicateNoiseToken(token) {
			continue
		}
		nonNoise++
	}
	if nonNoise == 0 {
		return true
	}
	return len(lower) < 10 && nonNoise < 2
}

func duplicateNoiseToken(token string) bool {
	switch token {
	case "const", "let", "var", "return", "true", "false", "nil", "null", "if", "for", "while", "switch", "case", "default", "break", "continue", "func", "function", "class", "type", "interface", "struct", "public", "private", "protected", "async", "await", "new":
		return true
	default:
		return false
	}
}

func classifyDuplicateSeverity(normalizedLines int, overlapPct float64, declarationLike bool) string {
	if normalizedLines >= 20 {
		return "high"
	}
	if normalizedLines >= 10 && overlapPct >= 65.0 {
		return "high"
	}
	if declarationLike && normalizedLines >= 6 && overlapPct >= 35.0 {
		return "high"
	}
	if (normalizedLines >= 8 && overlapPct >= 12.0) || (normalizedLines >= 5 && overlapPct >= 20.0) || overlapPct >= 35.0 {
		return "medium"
	}
	return "low"
}

func normalizedLinesOnly(lines []string) ([]string, []int) {
	return normalizeLinesForDuplicateDetection(lines)
}

func normalizeLinesForDuplicateDetection(lines []string) ([]string, []int) {
	normalized := make([]string, 0, len(lines))
	lineMap := make([]int, 0, len(lines))
	inBlockComment := false

	for idx, raw := range lines {
		line := raw
		if inBlockComment {
			end := strings.Index(line, "*/")
			if end == -1 {
				continue
			}
			line = line[end+2:]
			inBlockComment = false
		}

		for {
			start := strings.Index(line, "/*")
			if start == -1 {
				break
			}
			end := strings.Index(line[start+2:], "*/")
			if end == -1 {
				line = line[:start]
				inBlockComment = true
				break
			}
			line = line[:start] + " " + line[start+2+end+2:]
		}

		if comment := strings.Index(line, "//"); comment >= 0 {
			line = line[:comment]
		}

		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "*") {
			continue
		}

		collapsed := strings.Join(strings.Fields(trimmed), " ")
		if !containsAlphaNum(collapsed) {
			continue
		}

		normalized = append(normalized, collapsed)
		lineMap = append(lineMap, idx+1)
	}

	return normalized, lineMap
}

func readDocs(projectPath string) string {
	paths, err := gatherDocumentationFiles(projectPath)
	if err != nil {
		return ""
	}
	parts := make([]string, 0, len(paths))
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err == nil {
			parts = append(parts, strings.ToLower(string(b)))
		}
	}
	return strings.Join(parts, "\n")
}

func gatherDocumentationFiles(projectPath string) ([]string, error) {
	files := make([]string, 0, 128)
	projectRoot := filepath.Clean(projectPath)
	err := filepath.WalkDir(projectPath, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "target", "dist", "build", "coverage", "llvm-cov-target", "rust-analyzer", ".cache", ".venv", ".engine":
				return filepath.SkipDir
			}
			return nil
		}
		switch strings.ToLower(filepath.Ext(d.Name())) {
		case ".md", ".mdx", ".txt", ".dx":
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	_ = projectRoot
	return files, nil
}

func documentationMentionsFile(docText string, entry fileIndexEntry) bool {
	if docText == "" {
		return false
	}
	relPath := strings.ToLower(filepath.ToSlash(entry.Path))
	baseName := strings.ToLower(filepath.Base(entry.Path))
	stem := strings.TrimSuffix(baseName, filepath.Ext(baseName))
	if strings.Contains(docText, relPath) || strings.Contains(docText, baseName) || strings.Contains(docText, stem) || strings.Contains(docText, entry.BaseName) {
		return true
	}
	for _, sym := range entry.Symbols {
		if sym.Public && containsWord(docText, strings.ToLower(sym.Name)) {
			return true
		}
	}
	for _, iface := range entry.InterfaceNames {
		if containsWord(docText, strings.ToLower(iface)) {
			return true
		}
	}
	return false
}

func undocumentedInterfaces(docText string, names []string) []string {
	if len(names) == 0 {
		return nil
	}
	missing := make([]string, 0, len(names))
	for _, name := range names {
		if !containsWord(docText, strings.ToLower(name)) {
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)
	return missing
}

func collectPublicInterfaceNames(lines []string) []string {
	names := make([]string, 0, 8)
	seen := make(map[string]bool)
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if m := goInterfacePattern.FindStringSubmatch(trimmed); len(m) == 2 {
			name := m[1]
			if len(name) > 0 && strings.ToUpper(name[:1]) == name[:1] && !seen[name] {
				seen[name] = true
				names = append(names, name)
			}
			continue
		}
		if m := tsInterfacePattern.FindStringSubmatch(trimmed); len(m) == 2 {
			name := m[1]
			if (strings.HasPrefix(trimmed, "export ") || (len(name) > 0 && strings.ToUpper(name[:1]) == name[:1])) && !seen[name] {
				seen[name] = true
				names = append(names, name)
			}
		}
	}
	sort.Strings(names)
	return names
}

func fileNeedsDocReference(entry fileIndexEntry) bool {
	if len(entry.BaseName) <= 2 {
		return false
	}
	if len(entry.InterfaceNames) > 0 {
		return true
	}
	for _, sym := range entry.Symbols {
		if sym.Public {
			return true
		}
	}
	return false
}

func fileHasCategory(issues []Issue, category string) bool {
	for _, issue := range issues {
		if issue.Category == category {
			return true
		}
	}
	return false
}

func containsWord(text, word string) bool {
	if word == "" {
		return false
	}
	pattern := regexp.MustCompile(`\b` + regexp.QuoteMeta(word) + `\b`)
	return pattern.MatchString(text)
}

func chunkContainsDeclarationSignal(lines []string) bool {
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "func ") || strings.HasPrefix(trimmed, "type ") || strings.HasPrefix(trimmed, "class ") || strings.HasPrefix(trimmed, "export ") || strings.HasPrefix(trimmed, "interface ") || strings.Contains(trimmed, " struct {") {
			return true
		}
	}
	return false
}

func hasLeadingDocumentationComment(lines []string, start int) bool {
	if start <= 0 {
		return false
	}
	i := start - 1
	for i >= 0 && strings.TrimSpace(lines[i]) == "" {
		return false
	}
	seenComment := false
	for i >= 0 {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed == "" {
			break
		}
		if strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "/*") || strings.HasPrefix(trimmed, "*") {
			seenComment = true
			i--
			continue
		}
		break
	}
	return seenComment
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
					issues = append(issues, issueWithRule(ruleLargeUncommentedBlock, rel, start+1, "long-block", fmt.Sprintf("Large code block (%d lines) has no guiding comments.", length), "Split into smaller helpers and annotate non-obvious intent."))
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
			issues = append(issues, issueWithRule(ruleLargeUncommentedBlock, rel, start+1, "long-block", fmt.Sprintf("Large code block (%d lines) has no guiding comments.", length), "Split into smaller helpers and annotate non-obvious intent."))
		}
	}
	return issues
}

func functionComplexityIssues(rel string, lines []string) ([]Issue, []symbolDef) {
	issues := make([]Issue, 0, 8)
	symbols := make([]symbolDef, 0, 16)
	missingPublicDocs := make([]string, 0, 8)
	ext := strings.ToLower(filepath.Ext(rel))
	for idx := 0; idx < len(lines); idx++ {
		sig := parseFunctionSignature(rel, lines[idx], ext)
		if sig.Name == "" {
			continue
		}
		symbols = append(symbols, symbolDef{Name: sig.Name, File: rel, Line: idx + 1, Public: sig.Public})
		if sig.Public && !hasLeadingDocumentationComment(lines, idx) {
			missingPublicDocs = append(missingPublicDocs, sig.Name)
		}

		span := functionSpan(lines, idx)
		if sig.SpanMode == "indent" {
			span = functionSpanByIndent(lines, idx, sig.StartIndent)
		}
		decisionCount := functionDecisionCount(lines, idx, span)
		if shouldFlagLongFunction(rel, sig.Name, span, decisionCount) {
			issues = append(issues, issueWithRule(ruleLongFunction, rel, idx+1, "long-func", fmt.Sprintf("Function %s spans %d lines with %d decision points; likely violating single responsibility.", sig.Name, span, decisionCount), "Extract focused helpers so each method has one clear responsibility."))
		}
	}
	if len(missingPublicDocs) > 0 {
		sort.Strings(missingPublicDocs)
		preview := strings.Join(missingPublicDocs, ", ")
		if len(preview) > 180 {
			preview = preview[:177] + "..."
		}
		issues = append(issues, issueWithRule(ruleDocumentationPublicFn, rel, 1, "public-doc", fmt.Sprintf("%d public functions in this file lack adjacent doc comments: %s", len(missingPublicDocs), preview), "Add concise contract comments for each exported/public function in this file."))
	}
	return issues, symbols
}

func parseFunctionSignature(rel, line, ext string) functionSignature {
	if m := goFuncPattern.FindStringSubmatch(line); len(m) == 2 {
		name := m[1]
		return functionSignature{Name: name, Public: isPublicFunctionSignature(line, name, ext), SpanMode: "brace"}
	}
	if m := rustFuncPattern.FindStringSubmatch(line); len(m) == 2 {
		name := m[1]
		return functionSignature{Name: name, Public: isPublicFunctionSignature(line, name, ext), SpanMode: "brace"}
	}
	if m := jsFuncPattern.FindStringSubmatch(line); len(m) == 2 {
		name := m[1]
		return functionSignature{Name: name, Public: isPublicFunctionSignature(line, name, ext), SpanMode: "brace"}
	}
	if m := jsConstFuncPattern.FindStringSubmatch(line); len(m) == 2 {
		name := m[1]
		return functionSignature{Name: name, Public: isPublicFunctionSignature(line, name, ext), SpanMode: "brace"}
	}
	if m := pythonFuncPattern.FindStringSubmatch(line); len(m) == 2 {
		name := m[1]
		return functionSignature{Name: name, Public: isPublicFunctionSignature(line, name, ext), SpanMode: "indent", StartIndent: leadingIndentWidth(line)}
	}
	if m := kotlinFuncPattern.FindStringSubmatch(line); len(m) == 2 {
		name := m[1]
		return functionSignature{Name: name, Public: isPublicFunctionSignature(line, name, ext), SpanMode: "brace"}
	}
	if m := swiftFuncPattern.FindStringSubmatch(line); len(m) == 2 {
		name := m[1]
		return functionSignature{Name: name, Public: isPublicFunctionSignature(line, name, ext), SpanMode: "brace"}
	}
	if m := javaLikeFuncPattern.FindStringSubmatch(line); len(m) == 2 {
		name := m[1]
		if isControlKeyword(name) {
			return functionSignature{}
		}
		return functionSignature{Name: name, Public: isPublicFunctionSignature(line, name, ext), SpanMode: "brace"}
	}
	return functionSignature{}
}

func isControlKeyword(name string) bool {
	switch strings.ToLower(name) {
	case "if", "for", "while", "switch", "catch", "else", "do", "return", "func", "def", "async", "await", "function":
		return true
	default:
		return false
	}
}

func functionCallProfiles(rel string, lines []string) []functionProfile {
	out := make([]functionProfile, 0, 16)
	ext := strings.ToLower(filepath.Ext(rel))
	for idx := 0; idx < len(lines); idx++ {
		sig := parseFunctionSignature(rel, lines[idx], ext)
		if sig.Name == "" {
			continue
		}
		span := functionSpan(lines, idx)
		if sig.SpanMode == "indent" {
			span = functionSpanByIndent(lines, idx, sig.StartIndent)
		}
		if span <= 0 {
			continue
		}
		end := minInt(len(lines), idx+span)
		calls := extractCalls(lines[idx:end], sig.Name)
		riskyCallee, directRisk := firstBehavioralRiskyCall(calls)
		out = append(out, functionProfile{
			Name:        sig.Name,
			File:        rel,
			Line:        idx + 1,
			Public:      sig.Public,
			Calls:       calls,
			DirectRisk:  directRisk,
			RiskyCallee: riskyCallee,
		})
	}
	return out
}

func extractCalls(lines []string, self string) []string {
	seen := make(map[string]bool)
	out := make([]string, 0, 16)
	for _, line := range lines {
		for _, m := range genericCallPattern.FindAllStringSubmatch(line, -1) {
			if len(m) != 2 {
				continue
			}
			name := m[1]
			if name == "" || name == self || isControlKeyword(name) {
				continue
			}
			if seen[name] {
				continue
			}
			seen[name] = true
			out = append(out, name)
		}
		for _, m := range memberCallPattern.FindAllStringSubmatch(line, -1) {
			if len(m) != 2 {
				continue
			}
			name := m[1]
			if name == "" || name == self || isControlKeyword(name) {
				continue
			}
			if seen[name] {
				continue
			}
			seen[name] = true
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

func firstBehavioralRiskyCall(calls []string) (string, bool) {
	for _, call := range calls {
		if behavioralCallNameLooksRisky(call) {
			return call, true
		}
	}
	return "", false
}

func behavioralCallNameLooksRisky(callee string) bool {
	parts := strings.Split(callee, ".")
	name := strings.ToLower(parts[len(parts)-1])
	if name == "setstate" || name == "setvalue" || name == "set" || name == "update" {
		return false
	}
	risky := []string{"write", "save", "persist", "commit", "rollback", "migrate", "upsert", "insert", "publish"}
	for _, token := range risky {
		if strings.HasPrefix(name, token) || strings.Contains(name, "_"+token) {
			return true
		}
	}
	return false
}

func behavioralShiftIssues(index projectIndex, paths []string) []Issue {
	issues := make([]Issue, 0, 16)
	for _, relPath := range paths {
		entry := index.Files[relPath]
		if len(entry.FunctionProfiles) == 0 {
			continue
		}
		local := make(map[string]functionProfile, len(entry.FunctionProfiles))
		emitted := 0
		suppressed := 0
		for _, fn := range entry.FunctionProfiles {
			local[fn.Name] = fn
		}
		for _, fn := range entry.FunctionProfiles {
			if !fn.Public {
				continue
			}
			if behavioralCallNameLooksRisky(fn.Name) {
				continue
			}
			chain, risky := resolveRiskyCallChain(local, fn.Name, nil, 0, 5)
			if !risky {
				continue
			}
			if emitted >= behavioralIssuesPerFileCap {
				suppressed++
				continue
			}
			msg := fmt.Sprintf("Public function %s reaches a side-effect call chain (%s). Potential behavioral shift should be explicit and guarded.", fn.Name, strings.Join(chain, " -> "))
			issues = append(issues, issueWithRule(ruleBehavioralShiftChain, relPath, fn.Line, "behavior-shift|"+fn.Name, msg, "Document invariants and expected side effects, and ensure failures are explicitly handled at this boundary."))
			emitted++
		}
		if suppressed > 0 {
			issues = append(issues, issueWithRule(ruleBehavioralShiftCompact, relPath, 1, "behavior-shift-compact", fmt.Sprintf("%d additional behavioral-shift findings were compacted in this file to keep results actionable.", suppressed), "Address the highest-risk behavioral shift chains first, then re-scan for additional candidates."))
		}
	}
	return issues
}

func longFileIssues(index projectIndex, paths []string) []Issue {
	issues := make([]Issue, 0, 16)
	for _, relPath := range paths {
		entry := index.Files[relPath]
		if entry.RawLines <= 0 {
			continue
		}

		threshold := sourceLongFileThresholdLines
		if entry.IsTest {
			threshold = testLongFileThresholdLines
		}
		if entry.RawLines < threshold {
			continue
		}

		severity := "low"
		if !entry.IsTest && entry.RawLines >= sourceLongFileThresholdLines+500 {
			severity = "medium"
		}

		message := fmt.Sprintf("Long file detected: %s has %d lines. Consider decomposition for maintainability.", relPath, entry.RawLines)
		suggestion := "Split this file into focused modules/components so responsibilities and tests are easier to navigate."
		issues = append(issues, issueWithRuleSeverity(ruleLongFile, severity, relPath, 1, "long-file", message, suggestion))
	}
	return issues
}

func testSourceColocationIssues(index projectIndex, paths []string) []Issue {
	type dirState struct {
		hasTests  bool
		hasSource bool
		testFiles []string
	}

	byDir := make(map[string]*dirState)
	for _, relPath := range paths {
		dir := filepath.ToSlash(filepath.Dir(relPath))
		if dir == "." {
			continue
		}
		state := byDir[dir]
		if state == nil {
			state = &dirState{}
			byDir[dir] = state
		}
		entry := index.Files[relPath]
		if entry.IsTest {
			state.hasTests = true
			state.testFiles = append(state.testFiles, filepath.Base(relPath))
		} else {
			state.hasSource = true
		}
	}

	dirs := make([]string, 0, len(byDir))
	for dir, state := range byDir {
		if state.hasTests && state.hasSource {
			dirs = append(dirs, dir)
		}
	}
	sort.Strings(dirs)

	issues := make([]Issue, 0, len(dirs))
	for _, dir := range dirs {
		state := byDir[dir]
		sort.Strings(state.testFiles)
		preview := strings.Join(state.testFiles, ", ")
		if len(preview) > 160 {
			preview = preview[:157] + "..."
		}
		message := fmt.Sprintf("Tests are colocated with source files in %s (examples: %s).", dir, preview)
		suggestion := "Prefer separating tests into a dedicated test directory, or document the colocation convention to keep structure intentional."
		issues = append(issues, issueWithRule(ruleTestSourceColocation, dir, 1, "test-source-colocation", message, suggestion))
	}
	return issues
}

func resolveRiskyCallChain(local map[string]functionProfile, name string, seen map[string]bool, depth int, maxDepth int) ([]string, bool) {
	if depth > maxDepth {
		return []string{name}, false
	}
	if seen == nil {
		seen = make(map[string]bool)
	}
	if seen[name] {
		return []string{name}, false
	}
	seen[name] = true
	fn, ok := local[name]
	if !ok {
		if behavioralCallNameLooksRisky(name) {
			return []string{name}, true
		}
		return []string{name}, false
	}
	if fn.DirectRisk {
		if fn.RiskyCallee != "" {
			return []string{name, fn.RiskyCallee}, true
		}
		return []string{name}, true
	}
	for _, callee := range fn.Calls {
		nextSeen := cloneSeenSet(seen)
		trail, risky := resolveRiskyCallChain(local, callee, nextSeen, depth+1, maxDepth)
		if risky {
			return append([]string{name}, trail...), true
		}
	}
	return []string{name}, false
}

func cloneSeenSet(in map[string]bool) map[string]bool {
	out := make(map[string]bool, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func isPublicFunctionSignature(line, name, ext string) bool {
	trimmed := strings.TrimSpace(line)
	if strings.HasPrefix(trimmed, "export ") || strings.Contains(trimmed, " export ") {
		return true
	}
	if strings.HasPrefix(strings.ToLower(ext), ".py") {
		return !strings.HasPrefix(name, "_")
	}
	if strings.HasPrefix(strings.ToLower(ext), ".rs") {
		return strings.Contains(trimmed, "pub ")
	}
	if strings.Contains(trimmed, "public ") || strings.HasPrefix(trimmed, "public ") {
		return true
	}
	if name == "main" || name == "init" {
		return false
	}
	return len(name) > 0 && strings.ToUpper(name[:1]) == name[:1]
}

func leadingIndentWidth(line string) int {
	width := 0
	for _, ch := range line {
		if ch == ' ' {
			width++
			continue
		}
		if ch == '\t' {
			width += 4
			continue
		}
		break
	}
	return width
}

func functionSpanByIndent(lines []string, start int, baseIndent int) int {
	if start < 0 || start >= len(lines) {
		return 0
	}
	for i := start + 1; i < len(lines); i++ {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed == "" || pythonCommentLinePattern.MatchString(trimmed) {
			continue
		}
		indent := leadingIndentWidth(lines[i])
		if indent <= baseIndent {
			return i - start
		}
	}
	return len(lines) - start
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

func functionDecisionCount(lines []string, start int, span int) int {
	if span <= 0 {
		return 0
	}
	end := minInt(len(lines), start+span)
	count := 0
	for i := start; i < end; i++ {
		line := strings.ToLower(lines[i])
		count += strings.Count(line, "if ")
		count += strings.Count(line, " if(")
		count += strings.Count(line, "for ")
		count += strings.Count(line, "for(")
		count += strings.Count(line, "switch ")
		count += strings.Count(line, "case ")
		count += strings.Count(line, "else if")
		count += strings.Count(line, "&&")
		count += strings.Count(line, "||")
		count += strings.Count(line, "catch")
	}
	return count
}

func shouldFlagLongFunction(rel string, name string, span int, decisionCount int) bool {
	lowerRel := strings.ToLower(rel)
	isUIComponent := strings.HasSuffix(lowerRel, ".tsx") || strings.HasSuffix(lowerRel, ".jsx") || strings.HasSuffix(name, "Panel") || strings.HasSuffix(name, "Screen") || strings.HasSuffix(name, "View")

	if span >= 320 {
		return true
	}

	if isUIComponent {
		return span >= 450 && decisionCount >= 40
	}

	return span >= 200 && decisionCount >= 20
}

func principleViolations(rel string, lines []string, content string) []Issue {
	issues := make([]Issue, 0, 4)
	for i, line := range lines {
		if emptyCatchPattern.MatchString(line) {
			issues = append(issues, issueWithRule(ruleEmptyCatch, rel, i+1, "empty-catch", "Empty catch block detected. Swallowing exceptions hides real failures.", "Handle the error or rethrow with context."))
		}
	}
	for i, line := range lines {
		if goIgnoredErrPattern.MatchString(line) {
			issues = append(issues, issueWithRule(ruleIgnoredError, rel, i+1, "ignored-error", "Ignored error assignment found. CS 3500 style requires explicit handling at boundaries.", "Handle returned errors explicitly or document why ignoring is safe."))
		}
	}
	for _, line := range riskyDiscardedResultLines(lines) {
		issues = append(issues, issueWithRule(ruleDiscardedResult, rel, line, "discarded-result", "Discarded function result detected. Hidden failures often come from ignored return values.", "Capture and validate the result, or document why dropping it is safe."))
	}
	for i := 0; i < len(lines); i++ {
		if !pythonExceptLinePattern.MatchString(lines[i]) {
			continue
		}
		for j := i + 1; j < len(lines); j++ {
			next := strings.TrimSpace(lines[j])
			if next == "" || pythonCommentLinePattern.MatchString(next) {
				continue
			}
			if next == "pass" {
				issues = append(issues, issueWithRule(rulePythonEmptyExcept, rel, i+1, "python-empty-except", "Empty exception handler detected (except: pass). Swallowing exceptions hides real failures.", "Handle the exception, log context, or re-raise explicitly."))
			}
			break
		}
	}
	return issues
}

func riskyDiscardedResultLines(lines []string) []int {
	result := make([]int, 0, 4)
	for i, line := range lines {
		match := discardAssignmentPattern.FindStringSubmatch(line)
		if len(match) != 3 {
			continue
		}
		lhs := strings.ToLower(strings.TrimSpace(match[1]))
		callee := strings.ToLower(strings.TrimSpace(match[2]))
		if shouldFlagDiscardedAssignment(lhs, callee) {
			result = append(result, i+1)
		}
	}
	return result
}

func shouldFlagDiscardedAssignment(lhs, callee string) bool {
	if lhs == "" || callee == "" {
		return false
	}
	if lhs != "_" {
		return strings.HasPrefix(lhs, "unused") || strings.HasPrefix(lhs, "ignored") || strings.HasPrefix(lhs, "_")
	}
	return callNameLooksRisky(callee)
}

func callNameLooksRisky(callee string) bool {
	parts := strings.Split(callee, ".")
	name := parts[len(parts)-1]
	risky := []string{
		"write", "save", "persist", "create", "delete", "remove", "update", "set", "commit", "rollback", "migrate", "close", "send", "dispatch", "encode", "decode", "upsert", "insert",
	}
	for _, token := range risky {
		if strings.Contains(name, token) {
			return true
		}
	}
	return false
}

func deadCodeHeuristics(symbols []symbolDef, identifierCounts map[string]int) []Issue {
	issues := make([]Issue, 0, 16)
	for _, sym := range symbols {
		if skipDeadCodeCandidate(sym.Name) {
			continue
		}
		count := identifierCounts[sym.Name]
		if count <= 1 {
			issues = append(issues, issueWithRule(ruleDeadCodeSymbol, sym.File, sym.Line, "dead-code", fmt.Sprintf("Symbol %s appears only once across the indexed project and may be dead code.", sym.Name), "Remove unused symbol or add usage/tests proving it is required."))
		}
	}
	return issues
}

func isTestSourcePath(rel string) bool {
	lower := strings.ToLower(rel)
	return strings.Contains(lower, "/test/") || strings.Contains(lower, "_test.") || strings.Contains(lower, ".test.")
}

func RefreshProjectIndex(projectPath string) error {
	if strings.TrimSpace(projectPath) == "" {
		return fmt.Errorf("project path required")
	}
	_, _, err := buildProjectIndex(projectPath, nil)
	return err
}

func issueWithRule(ruleID, file string, line int, idSalt, message, suggestion string) Issue {
	return issueWithRuleSeverity(ruleID, "", file, line, idSalt, message, suggestion)
}

func issueWithRuleSeverity(ruleID, severity, file string, line int, idSalt, message, suggestion string) Issue {
	rule := ruleCatalog[ruleID]
	if strings.TrimSpace(severity) == "" {
		severity = rule.DefaultSeverity
	}
	return Issue{
		ID:         stableID(file, line, idSalt),
		Rule:       ruleID,
		Severity:   severity,
		Category:   rule.Category,
		Message:    message,
		File:       file,
		Line:       line,
		Suggestion: suggestion,
		DocURL:     rule.DocURL,
	}
}

func applyRuleEnginePolicy(projectPath string, issues []Issue) []Issue {
	overrides := loadRuleSeverityOverrides(projectPath)
	out := make([]Issue, 0, len(issues))
	for _, issue := range issues {
		if def, ok := ruleCatalog[issue.Rule]; ok {
			if issue.Category == "" {
				issue.Category = def.Category
			}
			if strings.TrimSpace(issue.Severity) == "" {
				issue.Severity = def.DefaultSeverity
			}
			if issue.DocURL == "" {
				issue.DocURL = def.DocURL
			}
		}
		if !isRulePrincipleAligned(issue.Rule, issue.Category) {
			continue
		}
		if overrideSeverity, ok := overrides[issue.Rule]; ok {
			normalized := normalizeRuleSeverity(overrideSeverity)
			if normalized == "off" {
				if ruleCanBeSilenced(issue.Rule) {
					continue
				}
				normalized = ""
			}
			if normalized != "" {
				issue.Severity = normalized
			}
		}
		out = append(out, issue)
	}
	return out
}

func isRulePrincipleAligned(ruleID, category string) bool {
	if strings.HasPrefix(ruleID, "cs.") || strings.HasPrefix(ruleID, "behavioral-shift.") {
		return true
	}
	if strings.HasPrefix(ruleID, "duplicate.") || strings.HasPrefix(ruleID, "documentation.") || strings.HasPrefix(ruleID, "dead-code.") || strings.HasPrefix(ruleID, "maintainability.") || strings.HasPrefix(ruleID, "css.") {
		return true
	}
	if category == "cs-principle" || category == "behavioral-shift" || category == "duplicate-content" || category == "documentation-gap" || category == "dead-code" || category == "large-block-without-comment" || category == "css-usage" || category == "maintainability" {
		return true
	}
	return false
}

func ruleCanBeSilenced(ruleID string) bool {
	if strings.HasPrefix(ruleID, "cs.error-handling.") {
		return false
	}
	if strings.HasPrefix(ruleID, "behavioral-shift.") {
		return false
	}
	return true
}

func normalizeRuleSeverity(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "error", "high":
		return "high"
	case "warn", "warning", "medium":
		return "medium"
	case "info", "low":
		return "low"
	case "off", "disabled", "disable", "none":
		return "off"
	default:
		return ""
	}
}

func loadRuleSeverityOverrides(projectPath string) map[string]string {
	candidates := []string{
		filepath.Join(projectPath, ".engine", "quality-rules.json"),
		filepath.Join(projectPath, ".quality-rules.json"),
	}
	for _, path := range candidates {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		if parsed := parseRuleOverrideJSON(data); len(parsed) > 0 {
			return parsed
		}
	}
	return nil
}

func parseRuleOverrideJSON(data []byte) map[string]string {
	var direct map[string]string
	if err := json.Unmarshal(data, &direct); err == nil && len(direct) > 0 {
		return direct
	}

	var wrapped struct {
		Rules    map[string]string `json:"rules"`
		Severity map[string]string `json:"severity"`
	}
	if err := json.Unmarshal(data, &wrapped); err == nil {
		if len(wrapped.Rules) > 0 {
			return wrapped.Rules
		}
		if len(wrapped.Severity) > 0 {
			return wrapped.Severity
		}
	}

	var structured struct {
		Rules map[string]struct {
			Severity string `json:"severity"`
		} `json:"rules"`
	}
	if err := json.Unmarshal(data, &structured); err == nil && len(structured.Rules) > 0 {
		out := make(map[string]string, len(structured.Rules))
		for ruleID, rule := range structured.Rules {
			if strings.TrimSpace(rule.Severity) != "" {
				out[ruleID] = rule.Severity
			}
		}
		if len(out) > 0 {
			return out
		}
	}

	return nil
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

func compressDocumentationGapIssues(issues []Issue) []Issue {
	type group struct {
		Dir           string
		Severity      string
		Kind          string
		TotalCount    int
		AffectedFiles map[string]bool
		Samples       []string
	}

	const (
		fileGapPrefix      = "File is not referenced anywhere in workspace documentation"
		funcGapPrefix      = "public functions in this file lack adjacent doc comments"
		interfaceGapPrefix = "public interfaces in this file are missing workspace documentation references"
	)

	grouped := make(map[string]*group)
	kept := make([]Issue, 0, len(issues))

	for _, issue := range issues {
		if issue.Category != "documentation-gap" {
			kept = append(kept, issue)
			continue
		}

		kind := ""
		count := 1
		switch {
		case issue.Severity == "low" && strings.HasPrefix(issue.Message, fileGapPrefix):
			kind = "file-reference"
		case issue.Severity == "medium" && strings.Contains(issue.Message, funcGapPrefix):
			kind = "function-comment"
			count = parseLeadingCount(issue.Message)
		case issue.Severity == "medium" && strings.Contains(issue.Message, interfaceGapPrefix):
			kind = "interface-reference"
			count = parseLeadingCount(issue.Message)
		}

		if kind == "" {
			kept = append(kept, issue)
			continue
		}

		dir := filepath.ToSlash(filepath.Dir(issue.File))
		key := issue.Severity + "|" + kind + "|" + dir
		g := grouped[key]
		if g == nil {
			g = &group{
				Dir:           dir,
				Severity:      issue.Severity,
				Kind:          kind,
				AffectedFiles: make(map[string]bool),
				Samples:       make([]string, 0, 8),
			}
			grouped[key] = g
		}
		if count < 1 {
			count = 1
		}
		g.TotalCount += count
		g.AffectedFiles[issue.File] = true
		g.Samples = append(g.Samples, filepath.Base(issue.File))
	}

	keys := make([]string, 0, len(grouped))
	for key := range grouped {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	for _, key := range keys {
		g := grouped[key]
		preview := dedupeSortedPreview(g.Samples, 6)
		msg := ""
		suggestion := ""
		switch g.Kind {
		case "file-reference":
			msg = fmt.Sprintf("%d files in %s are missing workspace documentation references: %s", len(g.AffectedFiles), g.Dir, preview)
			suggestion = "Document exported behavior for these files at the module or directory level."
		case "function-comment":
			msg = fmt.Sprintf("%d public functions across %d files in %s are missing adjacent doc comments (files: %s)", g.TotalCount, len(g.AffectedFiles), g.Dir, preview)
			suggestion = "Add contract comments for exported/public functions in these files."
		case "interface-reference":
			msg = fmt.Sprintf("%d public interfaces across %d files in %s are missing workspace documentation references (files: %s)", g.TotalCount, len(g.AffectedFiles), g.Dir, preview)
			suggestion = "Add module docs for these interfaces in .md/.mdx/.txt/.dx with responsibilities and usage guidance."
		}
		ruleID := ruleDocumentationGroupFile
		switch g.Kind {
		case "function-comment":
			ruleID = ruleDocumentationGroupFn
		case "interface-reference":
			ruleID = ruleDocumentationGroupIntf
		}
		kept = append(kept, issueWithRuleSeverity(ruleID, g.Severity, g.Dir, 1, "doc-gap-group|"+g.Kind+"|"+g.Severity, msg, suggestion))
	}

	return kept
}

func dedupeSortedPreview(items []string, limit int) string {
	if len(items) == 0 {
		return ""
	}
	seen := make(map[string]bool)
	uniq := make([]string, 0, len(items))
	for _, item := range items {
		if item == "" || seen[item] {
			continue
		}
		seen[item] = true
		uniq = append(uniq, item)
	}
	sort.Strings(uniq)
	if limit < 1 {
		limit = 1
	}
	previewCount := len(uniq)
	if previewCount > limit {
		previewCount = limit
	}
	preview := strings.Join(uniq[:previewCount], ", ")
	if len(uniq) > previewCount {
		preview += fmt.Sprintf(" (+%d more)", len(uniq)-previewCount)
	}
	return preview
}

func parseLeadingCount(message string) int {
	count := 0
	if _, err := fmt.Sscanf(strings.TrimSpace(message), "%d", &count); err != nil {
		return 1
	}
	if count < 1 {
		return 1
	}
	return count
}

func collectLargestDuplicateRuns(matchesByPair map[duplicatePairKey][]duplicateMatch) []duplicateRun {
	runs := make([]duplicateRun, 0, len(matchesByPair))
	keys := make([]duplicatePairKey, 0, len(matchesByPair))
	for key := range matchesByPair {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].LeftFile != keys[j].LeftFile {
			return keys[i].LeftFile < keys[j].LeftFile
		}
		return keys[i].RightFile < keys[j].RightFile
	})

	for _, key := range keys {
		matches := matchesByPair[key]
		if len(matches) == 0 {
			continue
		}
		sort.Slice(matches, func(i, j int) bool {
			if matches[i].LeftLine != matches[j].LeftLine {
				return matches[i].LeftLine < matches[j].LeftLine
			}
			return matches[i].RightLine < matches[j].RightLine
		})

		best := duplicateRun{}
		runStart := matches[0]
		runLen := 1
		runDecl := matches[0].DeclarationLike
		prev := matches[0]

		flush := func() {
			lineCount := runLen + duplicateChunkMinLines - 1
			if lineCount > best.NormalizedLines || (lineCount == best.NormalizedLines && runStart.RightLine < best.RightLine) {
				best = duplicateRun{
					LeftFile:        key.LeftFile,
					RightFile:       key.RightFile,
					LeftLine:        runStart.LeftLine,
					RightLine:       runStart.RightLine,
					NormalizedLines: lineCount,
					DeclarationLike: runDecl,
				}
			}
		}

		for i := 1; i < len(matches); i++ {
			cur := matches[i]
			if cur.LeftLine == prev.LeftLine+1 && cur.RightLine == prev.RightLine+1 {
				runLen++
				runDecl = runDecl || cur.DeclarationLike
				prev = cur
				continue
			}
			flush()
			runStart = cur
			runLen = 1
			runDecl = cur.DeclarationLike
			prev = cur
		}
		flush()

		if best.NormalizedLines > 0 {
			runs = append(runs, best)
		}
	}

	return runs
}

func containsAlphaNum(line string) bool {
	for _, r := range line {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' {
			return true
		}
	}
	return false
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
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
