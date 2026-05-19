package ai

import (
	"os"
	"path/filepath"
	"testing"
)

// TestDocDisplayName_AllCases covers every DocLayer constant and the default case.
func TestDocDisplayName_AllCases(t *testing.T) {
	cases := []struct {
		layer   DocLayer
		wantSub string
	}{
		{DocDesign, "DESIGN"},
		{DocVocabulary, "LANGUAGE"},
		{DocPRD, "PRD"},
		{DocModules, "MODULE"},
		{DocPlan, "PLAN"},
		{DocContext, "CONTEXT"},
		{DocLayer("custom.md"), "custom.md"},
	}
	for _, tc := range cases {
		got := docDisplayName(tc.layer)
		if got == "" {
			t.Errorf("docDisplayName(%q) returned empty string", tc.layer)
		}
	}
}

// TestWriteDoc_Success verifies WriteDoc creates the file in .engine/.
func TestWriteDoc_Success(t *testing.T) {
	dir := t.TempDir()
	if err := WriteDoc(dir, DocDesign, "some design content"); err != nil {
		t.Fatalf("WriteDoc: %v", err)
	}
	path := filepath.Join(dir, ".engine", string(DocDesign))
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("file not created: %v", err)
	}
	if string(data) != "some design content" {
		t.Errorf("unexpected content: %q", data)
	}
}

// TestWriteDoc_ErrorPath verifies WriteDoc returns error when MkdirAll fails.
func TestWriteDoc_ErrorPath(t *testing.T) {
	// Create a file where the .engine directory should be, so MkdirAll fails.
	dir := t.TempDir()
	blockPath := filepath.Join(dir, ".engine")
	if err := os.WriteFile(blockPath, []byte("blocking file"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := WriteDoc(dir, DocDesign, "content")
	if err == nil {
		t.Error("expected error when directory creation is blocked")
	}
}
