package ai

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/engine/server/db"
)

// contextFile is the legacy single-file location for the grill phase output.
// Preserved so projects scaffolded before the layered-docs rollout still
// resolve their context document. New work writes to DocDesign + DocVocabulary
// instead — see orchestrator_docs.go.
const contextFile = "context.md"

// orchestratorIntakePhase walks the design tree (RoleGriller) and persists
// the design concept to .engine/design.md. It does NOT produce the vocabulary
// table or PRD — those are RolePRDWriter's job, run as a separate phase
// because they consume the design concept verbatim and benefit from a fresh
// context window.
//
// Returns the design document for inline use; resume case re-reads existing
// design.md (the design concept once reached is stable across attempts).
//
// Why this is its own phase: shared understanding has to exist BEFORE the
// planner decomposes the work. Otherwise the planner invents vocabulary the
// later steps don't share and the reviewer rejects perfectly fine code for
// using the wrong name.
func orchestratorIntakePhase(cfg OrchestratorConfig, state *OrchestrationState, cancel <-chan struct{}) (string, error) {
	if existing := ReadDoc(cfg.ProjectPath, DocDesign); strings.TrimSpace(existing) != "" {
		return existing, nil
	}
	// Backward compat: if the legacy context.md exists, treat it as design.md
	// so old sessions resume gracefully.
	if legacy := readContextDoc(cfg.ProjectPath); strings.TrimSpace(legacy) != "" {
		if err := WriteDoc(cfg.ProjectPath, DocDesign, legacy); err != nil {
			return "", fmt.Errorf("persist migrated design.md: %w", err)
		}
		return legacy, nil
	}

	sessionID := fmt.Sprintf("%s-intake-%d", chooseSessionPrefix(cfg), time.Now().UnixNano())
	if err := db.CreateSession(sessionID, cfg.ProjectPath, ""); err != nil {
		return "", fmt.Errorf("create intake session: %w", err)
	}

	var (
		outMu sync.Mutex
		out   strings.Builder
	)
	ctx := &ChatContext{
		ProjectPath: cfg.ProjectPath,
		SessionID:   sessionID,
		Role:        RoleGriller,
		Cancel:      cancel,
		OnChunk: func(content string, _ bool) {
			if content == "" {
				return
			}
			outMu.Lock()
			out.WriteString(content)
			outMu.Unlock()
		},
		OnError:      func(string) {},
		OnToolCall:   func(string, any) {},
		OnToolResult: func(string, any, bool) {},
	}

	prompt := buildGrillerPrompt(state.Brief)
	cfg.chatFnFor()(ctx, prompt)

	design := strings.TrimSpace(out.String())
	if design == "" {
		return "", fmt.Errorf("grill phase produced no design document")
	}
	if err := WriteDoc(cfg.ProjectPath, DocDesign, design); err != nil {
		return design, fmt.Errorf("persist design.md: %w", err)
	}
	return design, nil
}

// orchestratorPRDPhase reads the design concept and emits two layered docs:
// vocabulary.md (terms table) and prd.md (module-aware PRD). Split here because
// the next phases — planner, builder, reviewer — all read these layers and
// benefit from them being pre-built rather than regenerated mid-loop.
func orchestratorPRDPhase(cfg OrchestratorConfig, cancel <-chan struct{}) error {
	if HasDoc(cfg.ProjectPath, DocVocabulary) && HasDoc(cfg.ProjectPath, DocPRD) {
		return nil // resume: both already exist, trust them
	}
	design := strings.TrimSpace(ReadDoc(cfg.ProjectPath, DocDesign))
	if design == "" {
		return fmt.Errorf("PRD phase: design.md missing — intake must run first")
	}

	sessionID := fmt.Sprintf("%s-prd-%d", chooseSessionPrefix(cfg), time.Now().UnixNano())
	if err := db.CreateSession(sessionID, cfg.ProjectPath, ""); err != nil {
		return fmt.Errorf("create PRD session: %w", err)
	}

	var (
		outMu sync.Mutex
		out   strings.Builder
	)
	ctx := &ChatContext{
		ProjectPath: cfg.ProjectPath,
		SessionID:   sessionID,
		Role:        RolePRDWriter,
		Cancel:      cancel,
		OnChunk: func(content string, _ bool) {
			if content == "" {
				return
			}
			outMu.Lock()
			out.WriteString(content)
			outMu.Unlock()
		},
		OnError:      func(string) {},
		OnToolCall:   func(string, any) {},
		OnToolResult: func(string, any, bool) {},
	}

	prompt := fmt.Sprintf("Design concept to distill (.engine/design.md):\n\n%s\n\nProduce the two sections separated by ---SPLIT--- as instructed.", design)
	cfg.chatFnFor()(ctx, prompt)

	vocab, prd, ok := splitPRDOutput(out.String())
	if !ok {
		return fmt.Errorf("PRD phase: output did not contain ---SPLIT--- separator")
	}
	if err := WriteDoc(cfg.ProjectPath, DocVocabulary, vocab); err != nil {
		return fmt.Errorf("persist vocabulary.md: %w", err)
	}
	if err := WriteDoc(cfg.ProjectPath, DocPRD, prd); err != nil {
		return fmt.Errorf("persist prd.md: %w", err)
	}
	return nil
}

// splitPRDOutput parses the PRDWriter's response into vocabulary + PRD halves.
// Returns ok=false when the separator is absent so the caller can surface a
// clear error rather than silently writing a mangled doc.
func splitPRDOutput(raw string) (vocab, prd string, ok bool) {
	const sep = "---SPLIT---"
	idx := strings.Index(raw, sep)
	if idx < 0 {
		return "", "", false
	}
	vocab = strings.TrimSpace(raw[:idx])
	prd = strings.TrimSpace(raw[idx+len(sep):])
	if vocab == "" || prd == "" {
		return "", "", false
	}
	return vocab, prd, true
}

// orchestratorModuleIndexPhase regenerates .engine/modules.md. Called after
// every plan step so the planner and reviewer of the NEXT step start with an
// up-to-date map. Failures are non-fatal: the index goes stale for one step
// but the orchestrator continues.
func orchestratorModuleIndexPhase(cfg OrchestratorConfig, cancel <-chan struct{}) error {
	sessionID := fmt.Sprintf("%s-modidx-%d", chooseSessionPrefix(cfg), time.Now().UnixNano())
	if err := db.CreateSession(sessionID, cfg.ProjectPath, ""); err != nil {
		return fmt.Errorf("create module-index session: %w", err)
	}

	var (
		outMu sync.Mutex
		out   strings.Builder
	)
	ctx := &ChatContext{
		ProjectPath: cfg.ProjectPath,
		SessionID:   sessionID,
		Role:        RoleModuleIndexer,
		Cancel:      cancel,
		MaxTurns:    6,
		OnChunk: func(content string, _ bool) {
			if content == "" {
				return
			}
			outMu.Lock()
			out.WriteString(content)
			outMu.Unlock()
		},
		OnError:      func(string) {},
		OnToolCall:   func(string, any) {},
		OnToolResult: func(string, any, bool) {},
	}

	cfg.chatFnFor()(ctx, "Rescan the project and emit the updated module index table per your instructions.")

	body := strings.TrimSpace(out.String())
	if body == "" {
		return fmt.Errorf("module-index phase produced no output")
	}
	return WriteDoc(cfg.ProjectPath, DocModules, body)
}

func buildGrillerPrompt(brief string) string {
	return strings.Join([]string{
		"Project brief:",
		strings.TrimSpace(brief),
		"",
		"Walk the design tree and emit the design concept document per your instructions. Output ONLY the synthesized markdown, no preamble.",
	}, "\n")
}

// readContextDoc reads from the legacy single-file path. New callers should
// use ReadDoc(projectPath, DocDesign) or ComposeDocContext(...) directly.
func readContextDoc(projectPath string) string {
	if v := ReadDoc(projectPath, DocContext); strings.TrimSpace(v) != "" {
		return v
	}
	// Fall through to legacy on-disk path used by older sessions.
	data, err := os.ReadFile(filepath.Join(projectPath, ".engine", contextFile))
	if err != nil {
		return ""
	}
	return string(data)
}

// writeContextDoc preserves legacy single-file output so old test fixtures
// keep working; production code should call WriteDoc(DocDesign|DocVocabulary)
// to write into the layered layout.
func writeContextDoc(projectPath, content string) error {
	return WriteDoc(projectPath, DocContext, content)
}

// orchestratorArchitectReview runs RoleArchitect over the latest diff. Treated
// as advisory by default: rejection is recorded as feedback for the next
// builder pass but does NOT block step completion unless the reviewer's own
// gate rejected too. This keeps early scaffolding from being repeatedly held
// up by a "module too shallow" verdict on intentionally small new files.
func orchestratorArchitectReview(cfg OrchestratorConfig, state *OrchestrationState, step *PlanStep, cancel <-chan struct{}) (ReviewVerdict, string) {
	sessionID := fmt.Sprintf("%s-arch-%d-%d", chooseSessionPrefix(cfg), step.Index, time.Now().UnixNano())
	if err := db.CreateSession(sessionID, cfg.ProjectPath, ""); err != nil {
		return ReviewInconclusive, "create architect session: " + err.Error()
	}
	policy := ResolveAutonomousPolicy(cfg.ProjectPath)
	var (
		outMu sync.Mutex
		out   strings.Builder
	)
	ctx := &ChatContext{
		ProjectPath:      cfg.ProjectPath,
		SessionID:        sessionID,
		Role:             RoleArchitect,
		AutonomousPolicy: &policy,
		Cancel:           cancel,
		MaxTurns:         8,
		OnChunk: func(content string, _ bool) {
			if content == "" {
				return
			}
			outMu.Lock()
			out.WriteString(content)
			outMu.Unlock()
		},
		OnError:      func(string) {},
		OnToolCall:   func(string, any) {},
		OnToolResult: func(string, any, bool) {},
	}
	prompt := fmt.Sprintf(
		"Review the latest change set for step %d (%q) in %s/%s. Apply the deep-modules rubric and respond per your instructions.",
		step.Index, step.Title, state.Owner, state.Repo,
	)
	cfg.chatFnFor()(ctx, prompt)
	verdict, feedback := parseReviewerVerdict(out.String())
	return verdict, feedback
}
