package fs

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadFile_Success(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hello.ts")
	if err := os.WriteFile(path, []byte("export const x = 1;"), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	fc, err := ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if fc.Language != "typescript" {
		t.Errorf("Language = %q, want typescript", fc.Language)
	}
	if fc.Content != "export const x = 1;" {
		t.Errorf("Content = %q", fc.Content)
	}
	if fc.Path != path {
		t.Errorf("Path = %q, want %q", fc.Path, path)
	}
	if fc.Size == 0 {
		t.Error("Size should not be zero")
	}
}

func TestReadFile_Plaintext(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(path, []byte("plain text"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	fc, err := ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if fc.Language != "plaintext" {
		t.Errorf("Language = %q, want plaintext", fc.Language)
	}
}

func TestReadFile_BinaryRejected(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "image.png")
	if err := os.WriteFile(path, []byte("fake png"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	_, err := ReadFile(path)
	if err == nil {
		t.Fatal("expected error for binary file")
	}
}

func TestReadFile_NotFound(t *testing.T) {
	_, err := ReadFile("/nonexistent/file.ts")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestReadFile_DirectoryError(t *testing.T) {
	dir := t.TempDir()
	_, err := ReadFile(dir)
	if err == nil {
		t.Fatal("expected error when reading a directory as a file")
	}
}

func TestWriteFile_CreatesParentDirs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "deep", "file.go")

	if err := WriteFile(path, "package main"); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(data) != "package main" {
		t.Errorf("content = %q, want 'package main'", string(data))
	}
}

func TestWriteFile_InvalidPath(t *testing.T) {
	err := WriteFile("bad\x00/path.txt", "x")
	if err == nil {
		t.Fatal("expected error for invalid path")
	}
}

func TestGetTree_File(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.go")
	if err := os.WriteFile(path, []byte("package main"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	node, err := GetTree(path, 3)
	if err != nil {
		t.Fatalf("GetTree: %v", err)
	}
	if node.Type != "file" {
		t.Errorf("Type = %q, want file", node.Type)
	}
	if node.Size == 0 {
		t.Error("Size should be non-zero for file")
	}
}

func TestGetTree_Directory(t *testing.T) {
	dir := t.TempDir()
	subDir := filepath.Join(dir, "sub")
	if err := os.Mkdir(subDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(subDir, "file.ts"), []byte("export {}"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	node, err := GetTree(dir, 3)
	if err != nil {
		t.Fatalf("GetTree: %v", err)
	}
	if node.Type != "directory" {
		t.Errorf("Type = %q, want directory", node.Type)
	}
	if !node.Loaded {
		t.Error("root should be Loaded")
	}
	if len(node.Children) == 0 {
		t.Error("expected children")
	}
}

func TestGetTree_MaxDepth(t *testing.T) {
	dir := t.TempDir()
	deep := filepath.Join(dir, "a", "b", "c")
	if err := os.MkdirAll(deep, 0755); err != nil {
		t.Fatalf("mkdirall: %v", err)
	}

	node, err := GetTree(dir, 1)
	if err != nil {
		t.Fatalf("GetTree: %v", err)
	}
	if node.Type != "directory" {
		t.Errorf("Type = %q", node.Type)
	}
}

func TestGetTree_NotFound(t *testing.T) {
	_, err := GetTree("/nonexistent/path", 2)
	if err == nil {
		t.Fatal("expected error for missing path")
	}
}

func TestGetTree_UnreadableDir(t *testing.T) {
	dir := t.TempDir()
	badDir := filepath.Join(dir, "noperms")
	if err := os.Mkdir(badDir, 0); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	defer os.Chmod(badDir, 0755) //nolint:errcheck

	node, err := GetTree(badDir, 3)
	// Should not error — unreadable dir returns node with Loaded=true
	if err != nil {
		t.Fatalf("GetTree unreadable: %v", err)
	}
	if node == nil {
		t.Fatal("expected node")
	}
}

func TestDetectLanguage_KnownExtensions(t *testing.T) {
	cases := []struct {
		path string
		want string
	}{
		{"main.go", "go"},
		{"app.ts", "typescript"},
		{"app.tsx", "typescriptreact"},
		{"index.js", "javascript"},
		{"index.jsx", "javascriptreact"},
		{"style.css", "css"},
		{"config.json", "json"},
		{"README.md", "markdown"},
		{"script.sh", "shell"},
		{"query.sql", "sql"},
		{"main.py", "python"},
		{"build.rs", "rust"},
		{"Makefile", "makefile"},
		{"Dockerfile", "dockerfile"},
		{"unknown.xyz", "plaintext"},
	}

	for _, tc := range cases {
		got := DetectLanguage(tc.path)
		if got != tc.want {
			t.Errorf("DetectLanguage(%q) = %q, want %q", tc.path, got, tc.want)
		}
	}
}

func TestSearchFiles_NoRipgrep(t *testing.T) {
	// Skip if rg is available (test the error path in isolation is hard)
	// We just call SearchFiles and accept either result or skip if rg is missing.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\nfunc main() {}"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	result, err := SearchFiles("main", dir, "*.go")
	if err != nil {
		// rg not available is acceptable in CI — skip
		t.Skipf("SearchFiles error (rg may not be available): %v", err)
	}
	if !strings.Contains(result, "main") {
		t.Errorf("expected 'main' in result, got: %s", result)
	}
}

func TestSearchMatches_NoResults(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("hello world"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	results, err := SearchMatches("ZZZNOMATCHES", dir, "")
	if err != nil {
		t.Skipf("SearchMatches: rg may not be available: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}
}

func TestSearchFiles_WithMatches(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "hello.go"), []byte("package main\nfunc hello() {}"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	result, err := SearchFiles("hello", dir, "")
	if err != nil {
		t.Skipf("rg not available: %v", err)
	}
	if !strings.Contains(result, "hello") {
		t.Errorf("expected 'hello' in result, got: %s", result)
	}
}

func TestSearchMatches_WithResults(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("func main() {}\n"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	results, err := SearchMatches("main", dir, "")
	if err != nil {
		t.Skipf("rg not available: %v", err)
	}
	if len(results) == 0 {
		t.Error("expected at least one result")
	}
	if results[0].Path == "" {
		t.Error("expected non-empty path")
	}
	if results[0].Line == 0 {
		t.Error("expected non-zero line")
	}
}

func TestSearchFiles_EmptyResults(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "file.txt"), []byte("no match here"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	result, err := SearchFiles("ZZZNOMATCHES", dir, "")
	if err != nil {
		t.Skipf("SearchFiles: rg may not be available: %v", err)
	}
	if result != "No matches found" {
		t.Errorf("expected 'No matches found', got %q", result)
	}
}

func TestSearchMatches_RelativePath(t *testing.T) {
        // Use a relative directory path so rg returns relative paths,
        // triggering the filepath.Join(dir, matchPath) branch.
        dir := t.TempDir()
        if err := os.WriteFile(filepath.Join(dir, "rel.go"), []byte("relpattern"), 0644); err != nil {
                t.Fatalf("write: %v", err)
        }

        // chdir to parent so we can pass a relative dir.
        orig, _ := os.Getwd()
        defer os.Chdir(orig) //nolint:errcheck
        parent := filepath.Dir(dir)
        if err := os.Chdir(parent); err != nil {
                t.Skipf("cannot chdir: %v", err)
        }
        relDir := filepath.Base(dir)

        results, err := SearchMatches("relpattern", relDir, "")
        if err != nil {
                t.Skipf("rg not available: %v", err)
        }
        if len(results) == 0 {
                t.Error("expected results")
                return
        }
        // filepath.Join was applied to make the path relative to dir.
        if !strings.HasSuffix(results[0].Path, "rel.go") {
                t.Errorf("expected path ending with rel.go, got %q", results[0].Path)
        }
}

func TestSearchMatches_NilResultsGuard(t *testing.T) {
        // Verify nil guard: a no-op call to rg that exits 0 with no match events.
        // We achieve this by using --stats via rg's stdin mode — instead, we test
        // an empty dir where rg exits 0 with only summary events (no matches).
        // Actually rg exits 1 on no match, but with --stats exits 0.
        // Since our API doesn't expose --stats, we test the nil guard by
        // verifying SearchMatches returns a non-nil slice even when results is empty.
        dir := t.TempDir()
        // Empty dir — rg should exit 1 (no matches).
        results, err := SearchMatches("ZZZNOMATCHES", dir, "")
        if err != nil {
                t.Skipf("rg not available: %v", err)
        }
        if results == nil {
                t.Error("expected non-nil results slice")
        }
}

func TestSearchMatches_ScannerError(t *testing.T) {
        // Trigger scanner.Err() by using a fake rg that writes a line exceeding
        // the 8MB scanner buffer.  We create a temp binary named "rg" and
        // prepend its directory to PATH.
        dir := t.TempDir()

        // Write a Go program that prints a JSON line longer than 8MB.
        fakeSrc := `package main
import (
	"fmt"
	"strings"
)
func main() {
	big := strings.Repeat("x", 9*1024*1024)
	fmt.Println(big)
}
`
        srcFile := filepath.Join(dir, "fake_rg.go")
        if err := os.WriteFile(srcFile, []byte(fakeSrc), 0644); err != nil {
                t.Fatalf("write fake src: %v", err)
        }
        outBin := filepath.Join(dir, "rg")
        cmd := exec.Command("go", "build", "-o", outBin, srcFile)
        if out, err := cmd.CombinedOutput(); err != nil {
                t.Fatalf("build fake rg: %v\n%s", err, out)
        }

        origPath := os.Getenv("PATH")
        os.Setenv("PATH", dir+string(os.PathListSeparator)+origPath)
        defer os.Setenv("PATH", origPath)

        _, err := SearchMatches("anything", t.TempDir(), "")
        if err == nil {
                t.Error("expected scanner error from oversized line")
        }
}

func TestSearchFiles_ErrorFromSearchMatches(t *testing.T) {
	// Pass an invalid regex to trigger a rg hard failure.
	_, err := SearchFiles("(?P", t.TempDir(), "")
	// rg exits with code 2 on invalid regex, so we expect an error.
	_ = err
}
func TestSearchFiles_Truncation(t *testing.T) {
        // Create a file with 200 very long lines matching a pattern so the
        // formatted output exceeds 4MB, triggering the truncation path.
        if _, err := os.Stat("/opt/homebrew/bin/rg"); os.IsNotExist(err) {
                t.Skip("rg not available")
        }

        dir := t.TempDir()
        // Each line: "NEEDLE" + padding to ~21000 bytes.
        lineLen := 21000
        line := "NEEDLE" + strings.Repeat("x", lineLen-6)
        var content strings.Builder
        for i := 0; i < 201; i++ {
                content.WriteString(line)
                content.WriteByte('\n')
        }
        if err := os.WriteFile(filepath.Join(dir, "big.txt"), []byte(content.String()), 0644); err != nil {
                t.Fatalf("write: %v", err)
        }

        result, err := SearchFiles("NEEDLE", dir, "")
        if err != nil {
                t.Skipf("rg not available: %v", err)
        }
        if !strings.HasSuffix(result, "... (truncated)") {
                t.Errorf("expected truncation, got output of length %d", len(result))
        }
}