package ai

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDocPath_ResolvesUnderEngine(t *testing.T) {
	got := DocPath("/proj", DocDesign)
	want := filepath.Join("/proj", ".engine", "design.md")
	if got != want {
		t.Errorf("DocPath: got %q want %q", got, want)
	}
}

func TestReadDoc_EmptyOnMissing(t *testing.T) {
	dir := t.TempDir()
	if got := ReadDoc(dir, DocPRD); got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

func TestWriteReadDoc_Roundtrip(t *testing.T) {
	dir := t.TempDir()
	if err := WriteDoc(dir, DocVocabulary, "| Term | Def |\n| Todo | a task |"); err != nil {
		t.Fatalf("write: %v", err)
	}
	if got := ReadDoc(dir, DocVocabulary); !strings.Contains(got, "Todo") {
		t.Errorf("roundtrip mismatch: %q", got)
	}
	if _, err := os.Stat(filepath.Join(dir, ".engine", "vocabulary.md")); err != nil {
		t.Errorf("file not created: %v", err)
	}
}

func TestHasDoc_TrueOnlyWhenNonEmpty(t *testing.T) {
	dir := t.TempDir()
	if HasDoc(dir, DocDesign) {
		t.Error("expected false for missing doc")
	}
	if err := WriteDoc(dir, DocDesign, ""); err != nil {
		t.Fatalf("write empty design doc: %v", err)
	}
	if HasDoc(dir, DocDesign) {
		t.Error("expected false for empty doc")
	}
	if err := WriteDoc(dir, DocDesign, "real content"); err != nil {
		t.Fatalf("write real design doc: %v", err)
	}
	if !HasDoc(dir, DocDesign) {
		t.Error("expected true for non-empty doc")
	}
}

func TestComposeDocContext_PreservesOrderAndSkipsEmpty(t *testing.T) {
	dir := t.TempDir()
	if err := WriteDoc(dir, DocDesign, "DESIGN BODY"); err != nil {
		t.Fatalf("write design doc: %v", err)
	}
	if err := WriteDoc(dir, DocVocabulary, "VOCAB BODY"); err != nil {
		t.Fatalf("write vocabulary doc: %v", err)
	}
	// DocPRD intentionally not written — must be skipped silently.

	composed := ComposeDocContext(dir, DocDesign, DocPRD, DocVocabulary)
	if !strings.Contains(composed, "DESIGN CONCEPT") || !strings.Contains(composed, "DESIGN BODY") {
		t.Errorf("design layer missing: %s", composed)
	}
	if !strings.Contains(composed, "UBIQUITOUS LANGUAGE") || !strings.Contains(composed, "VOCAB BODY") {
		t.Errorf("vocab layer missing: %s", composed)
	}
	if strings.Contains(composed, "PRD") {
		t.Errorf("PRD section should have been skipped: %s", composed)
	}
	// Order: design must appear before vocab in the composed text.
	if strings.Index(composed, "DESIGN BODY") > strings.Index(composed, "VOCAB BODY") {
		t.Error("compose order not preserved")
	}
}

func TestComposeDocContext_EmptyWhenAllMissing(t *testing.T) {
	dir := t.TempDir()
	if got := ComposeDocContext(dir, DocDesign, DocVocabulary, DocPRD, DocModules); got != "" {
		t.Errorf("expected empty composition, got %q", got)
	}
}

func TestSplitPRDOutput_Happy(t *testing.T) {
	raw := "# Ubiquitous Language\n| Term | Def |\n| X | y |\n\n---SPLIT---\n\n# Product Requirements\n## Overview\nthe overview"
	vocab, prd, ok := splitPRDOutput(raw)
	if !ok {
		t.Fatal("expected ok split")
	}
	if !strings.Contains(vocab, "Ubiquitous Language") {
		t.Errorf("vocab missing header: %q", vocab)
	}
	if !strings.Contains(prd, "Product Requirements") {
		t.Errorf("prd missing header: %q", prd)
	}
}

func TestSplitPRDOutput_MissingSeparator(t *testing.T) {
	if _, _, ok := splitPRDOutput("just one section, no separator"); ok {
		t.Error("expected ok=false when separator missing")
	}
}

func TestSplitPRDOutput_EmptyHalf(t *testing.T) {
	raw := "---SPLIT---\n# PRD"
	if _, _, ok := splitPRDOutput(raw); ok {
		t.Error("expected ok=false when one half is empty")
	}
}

// orchestratorIntakePhase should pick up an existing legacy context.md and
// migrate it forward to design.md, so projects scaffolded before the layered-
// docs rollout don't re-run the grill.
func TestOrchestratorIntakePhase_MigratesLegacyContext(t *testing.T) {
	dir := t.TempDir()
	legacy := "## Legacy context body"
	if err := writeContextDoc(dir, legacy); err != nil {
		t.Fatalf("seed legacy: %v", err)
	}
	// Remove the new layered design.md if a write_legacy wrote it as DocContext.
	if err := os.Remove(DocPath(dir, DocDesign)); err != nil && !os.IsNotExist(err) {
		t.Fatalf("remove layered design doc: %v", err)
	}

	cfg := OrchestratorConfig{
		ProjectPath: dir,
		ChatFn: func(_ *ChatContext, _ string) {
			t.Fatal("ChatFn should not be invoked when legacy context.md exists")
		},
	}
	state := &OrchestrationState{Brief: "brief"}

	got, err := orchestratorIntakePhase(cfg, state, nil)
	if err != nil {
		t.Fatalf("intake: %v", err)
	}
	if !strings.Contains(got, "Legacy context body") {
		t.Errorf("expected legacy content carried forward: %q", got)
	}
	// After migration, design.md should now exist with the legacy body.
	if !HasDoc(dir, DocDesign) {
		t.Error("expected design.md to be created from legacy content")
	}
}

func TestOrchestratorPRDPhase_NoOpWhenBothPresent(t *testing.T) {
	dir := t.TempDir()
	if err := WriteDoc(dir, DocDesign, "design"); err != nil {
		t.Fatalf("write design doc: %v", err)
	}
	if err := WriteDoc(dir, DocVocabulary, "vocab"); err != nil {
		t.Fatalf("write vocabulary doc: %v", err)
	}
	if err := WriteDoc(dir, DocPRD, "prd"); err != nil {
		t.Fatalf("write prd doc: %v", err)
	}

	cfg := OrchestratorConfig{
		ProjectPath: dir,
		ChatFn: func(_ *ChatContext, _ string) {
			t.Fatal("ChatFn should not be invoked when both layered docs exist")
		},
	}
	if err := orchestratorPRDPhase(cfg, nil); err != nil {
		t.Fatalf("PRD phase: %v", err)
	}
}

func TestOrchestratorPRDPhase_MissingDesignFails(t *testing.T) {
	dir := t.TempDir()
	cfg := OrchestratorConfig{
		ProjectPath: dir,
		ChatFn:      func(_ *ChatContext, _ string) {},
	}
	if err := orchestratorPRDPhase(cfg, nil); err == nil {
		t.Error("expected error when design.md is missing")
	}
}
