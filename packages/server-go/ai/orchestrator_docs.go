package ai

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// DocLayer identifies one of Engine's per-project documentation files. Each
// layer plays a specific role in Pocock's "grill → distill → plan → execute"
// pipeline; routing every read/write through this type guarantees the layers
// stay consistent across roles.
type DocLayer string

const (
	// DocDesign captures the design concept from the grill phase — the
	// shared understanding the AI reached by walking the decision tree.
	// Output is prose with explicit "decided" and "open" sections.
	DocDesign DocLayer = "design.md"

	// DocVocabulary holds the project's ubiquitous-language terms table.
	// Distilled from design.md by RolePRDWriter so vocabulary lives in one
	// canonical place and never duplicates the design narrative.
	DocVocabulary DocLayer = "vocabulary.md"

	// DocPRD is the module-aware product requirements document. Lists the
	// modules touched, the public interface each one exposes, and how it
	// changes. Bridge between vocabulary and plan.
	DocPRD DocLayer = "prd.md"

	// DocModules is the auto-maintained module index: path → purpose →
	// public interface → critical/non-critical. Rewritten after every plan
	// step so the next step starts with a fresh map of the codebase.
	DocModules DocLayer = "modules.md"

	// DocPlan is the TDD-shaped checkbox plan. Written by RolePlanner.
	DocPlan DocLayer = "plan.md"

	// DocContext is the legacy single-file context document. Kept for
	// backward-compatibility with sessions started under the prior single-
	// doc layout; new code should prefer DocDesign + DocVocabulary.
	DocContext DocLayer = "context.md"
)

// docDir is the on-disk parent for all doc layers, relative to project root.
const docDir = ".engine"

// DocPath returns the absolute path on disk for a layer in projectPath.
// Centralized so changing the layout is a single-file edit.
func DocPath(projectPath string, layer DocLayer) string {
	return filepath.Join(projectPath, docDir, string(layer))
}

// ReadDoc returns the layer content, or "" when missing or unreadable.
// Errors are swallowed because the caller's policy is always the same:
// missing doc → empty string → the consuming prompt omits that section.
func ReadDoc(projectPath string, layer DocLayer) string {
	data, err := os.ReadFile(DocPath(projectPath, layer))
	if err != nil {
		return ""
	}
	return string(data)
}

// WriteDoc persists content to the layer's path, creating the .engine
// directory when absent.
func WriteDoc(projectPath string, layer DocLayer, content string) error {
	dir := filepath.Join(projectPath, docDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("ensure docs dir: %w", err)
	}
	return os.WriteFile(DocPath(projectPath, layer), []byte(content), 0o644)
}

// HasDoc reports whether the layer file exists with non-empty content.
// Used to short-circuit phases on resume so we don't regenerate canonical
// docs unnecessarily — the design concept once reached is stable.
func HasDoc(projectPath string, layer DocLayer) bool {
	return strings.TrimSpace(ReadDoc(projectPath, layer)) != ""
}

// ComposeDocContext assembles a prompt prefix from the requested layers,
// each prefaced with a clear header. Empty layers are skipped so the prefix
// never carries dead weight. The order of `layers` is preserved in output.
//
// Used by every role-aware prompt builder so each role sees exactly the
// documentation slices it needs — no more, no less.
func ComposeDocContext(projectPath string, layers ...DocLayer) string {
	var b strings.Builder
	for _, layer := range layers {
		body := strings.TrimSpace(ReadDoc(projectPath, layer))
		if body == "" {
			continue
		}
		fmt.Fprintf(&b, "=== %s ===\n%s\n\n", docDisplayName(layer), body)
	}
	return strings.TrimSpace(b.String())
}

// docDisplayName returns the short human label used in prompt headers.
func docDisplayName(layer DocLayer) string {
	switch layer {
	case DocDesign:
		return "DESIGN CONCEPT"
	case DocVocabulary:
		return "UBIQUITOUS LANGUAGE"
	case DocPRD:
		return "PRD (module-aware)"
	case DocModules:
		return "MODULE INDEX"
	case DocPlan:
		return "PLAN"
	case DocContext:
		return "CONTEXT (legacy)"
	default:
		return string(layer)
	}
}
