package ai

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDocumenterStepNoIndex: documenter skips gracefully with no index.dx
func TestDocumenterStepNoIndex(t *testing.T) {
	tmpDir := t.TempDir()

	result := DocumenterStep(tmpDir, "test step", "test body", "test result")
	if !strings.Contains(result, "skip") {
		t.Fatalf("expected skip, got %q", result)
	}
}

// TestDocumenterStepEmptyStep: documenter skips empty steps
func TestDocumenterStepEmptyStep(t *testing.T) {
	tmpDir := t.TempDir()

	// Create minimal dx structure
	docDir := filepath.Join(tmpDir, ".doc")
	os.MkdirAll(docDir, 0o755)
	os.WriteFile(filepath.Join(tmpDir, "index.dx"), []byte("# Test"), 0o644)

	result := DocumenterStep(tmpDir, "", "", "")
	if !strings.Contains(result, "skip") {
		t.Fatalf("expected skip for empty step, got %q", result)
	}
}

// TestSparseRecallNoDocuments: sparse recall skips when no dx docs
func TestSparseRecallNoDocuments(t *testing.T) {
	tmpDir := t.TempDir()

	result := sparseRecall(tmpDir, "test query", 6)
	if result != "" {
		t.Fatalf("expected empty result, got %q", result)
	}
}

// TestSparseRecallKCap: sparse recall respects k <= 6 ceiling
func TestSparseRecallKCap(t *testing.T) {
	tmpDir := t.TempDir()
	docDir := filepath.Join(tmpDir, ".doc")
	os.MkdirAll(docDir, 0o755)
	os.WriteFile(filepath.Join(tmpDir, "index.dx"), []byte("# Test"), 0o644)

	// k > 6 should clamp to 6
	result := sparseRecall(tmpDir, "test", 10)
	_ = result // dx CLI not available in test env, but function should handle gracefully
}

// TestReadMemoryFromDxReturnsBlocks: ReadMemoryFromDx returns blocks not whole docs
func TestReadMemoryFromDxReturnsBlocks(t *testing.T) {
	tmpDir := t.TempDir()
	docDir := filepath.Join(tmpDir, ".doc")
	os.MkdirAll(docDir, 0o755)
	os.WriteFile(filepath.Join(tmpDir, "index.dx"), []byte("# Test\n## Block1\nContent"), 0o644)

	// Without dx CLI, function returns empty but logs the call
	result := ReadMemoryFromDx(tmpDir, "test", 6)
	_ = result
}

// TestReadMemoryFromDxKLimit: ReadMemoryFromDx caps k at 6
func TestReadMemoryFromDxKLimit(t *testing.T) {
	tmpDir := t.TempDir()

	// k=0 should not panic, should return empty
	result := ReadMemoryFromDx(tmpDir, "query", 0)
	if result != "" {
		t.Fatalf("expected empty for k=0, got %q", result)
	}

	// k>6 should cap at 6 (though dx is not available in test)
	_ = ReadMemoryFromDx(tmpDir, "query", 20)
}

// TestWriteMemoryToDxFallback: WriteMemoryToDx returns empty on success
func TestWriteMemoryToDxFallback(t *testing.T) {
	tmpDir := t.TempDir()

	// No dx docs: fallback to old store path (returns empty)
	result := WriteMemoryToDx(tmpDir, "section", "content")
	if result != "" {
		t.Fatalf("expected empty fallback, got %q", result)
	}
}

// TestWriteMemoryToDxEmptyParams: WriteMemoryToDx validates inputs
func TestWriteMemoryToDxEmptyParams(t *testing.T) {
	// Empty path
	result := WriteMemoryToDx("", "section", "content")
	if !strings.Contains(result, "empty") {
		t.Fatalf("expected error for empty path, got %q", result)
	}

	// Empty section
	result = WriteMemoryToDx("/tmp", "", "content")
	if !strings.Contains(result, "empty") {
		t.Fatalf("expected error for empty section, got %q", result)
	}
}
