package workspace

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadRegistry_Missing(t *testing.T) {
	dir := t.TempDir()
	entries, err := LoadRegistry(dir)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected empty slice, got %d entries", len(entries))
	}
}

func TestLoadRegistry_Invalid(t *testing.T) {
	dir := t.TempDir()
	rp := registryPath(dir)
	if err := os.MkdirAll(filepath.Dir(rp), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rp, []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := LoadRegistry(dir)
	if err == nil {
		t.Fatal("expected parse error")
	}
}

func TestAddToRegistry_LocalPath(t *testing.T) {
	dir := t.TempDir()
	repoDir := filepath.Join(dir, "myrepo")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}

	entry, err := AddToRegistry(dir, repoDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if entry.Name != "myrepo" {
		t.Errorf("name = %q, want %q", entry.Name, "myrepo")
	}
	if entry.URL != "" {
		t.Errorf("url should be empty for local path, got %q", entry.URL)
	}

	// Idempotent: adding twice returns the same entry without duplication.
	entry2, err := AddToRegistry(dir, repoDir)
	if err != nil {
		t.Fatalf("unexpected error on duplicate add: %v", err)
	}
	if entry2.LocalPath != entry.LocalPath {
		t.Errorf("duplicate add returned different path")
	}
	entries, _ := LoadRegistry(dir)
	if len(entries) != 1 {
		t.Errorf("expected 1 entry after duplicate add, got %d", len(entries))
	}
}

func TestAddToRegistry_NonExistentPath(t *testing.T) {
	dir := t.TempDir()
	_, err := AddToRegistry(dir, filepath.Join(dir, "nonexistent"))
	if err == nil {
		t.Fatal("expected error for non-existent path")
	}
}

func TestAddToRegistry_EmptyPath(t *testing.T) {
	dir := t.TempDir()
	_, err := AddToRegistry(dir, "")
	if err == nil {
		t.Fatal("expected error for empty path")
	}
}

func TestAddToRegistry_URL(t *testing.T) {
	dir := t.TempDir()

	// Inject a clone stub that creates a directory.
        origClone := cloneRepoFn
        cloneRepoFn = func(url, dest string) error {
                return os.MkdirAll(dest, 0o755)
        }
        t.Cleanup(func() { cloneRepoFn = origClone })

        entry, err := AddToRegistry(dir, "https://github.com/owner/testrepo.git")
        if err != nil {
                t.Fatalf("unexpected error: %v", err)
        }
        if entry.URL != "https://github.com/owner/testrepo.git" {
                t.Errorf("url = %q", entry.URL)
        }
        if entry.Name != "testrepo" {
                t.Errorf("name = %q, want %q", entry.Name, "testrepo")
        }
}

func TestAddToRegistry_URLCloneFails(t *testing.T) {
        dir := t.TempDir()
        origClone := cloneRepoFn
        cloneRepoFn = func(url, dest string) error {
                return os.ErrPermission
        }
        t.Cleanup(func() { cloneRepoFn = origClone })

        _, err := AddToRegistry(dir, "https://github.com/owner/fail.git")
        if err == nil {
                t.Fatal("expected clone error")
        }
}

func TestRemoveFromRegistry(t *testing.T) {
	dir := t.TempDir()
	repoDir := filepath.Join(dir, "alpha")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := AddToRegistry(dir, repoDir); err != nil {
		t.Fatal(err)
	}

	if err := RemoveFromRegistry(dir, "alpha"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	entries, _ := LoadRegistry(dir)
	if len(entries) != 0 {
		t.Errorf("expected 0 entries after remove, got %d", len(entries))
	}
}

func TestRemoveFromRegistry_NotFound(t *testing.T) {
	dir := t.TempDir()
	err := RemoveFromRegistry(dir, "nonexistent")
	if err == nil {
		t.Fatal("expected error for missing entry")
	}
}

func TestRemoveFromRegistry_EmptyName(t *testing.T) {
	dir := t.TempDir()
	err := RemoveFromRegistry(dir, "")
	if err == nil {
		t.Fatal("expected error for empty name")
	}
}

func TestLoadRegistry_ReadError(t *testing.T) {
	dir := t.TempDir()
	rp := registryPath(dir)
	if err := os.MkdirAll(rp, 0o755); err != nil {
		t.Fatal(err)
	}
	// rp is now a directory, not a file — ReadFile should fail.
	_, err := LoadRegistry(dir)
	if err == nil {
		t.Fatal("expected read error when registry path is a directory")
	}
}

func TestSaveRegistry_MkdirAllError(t *testing.T) {
	// Make the parent of the registry path unwritable.
	dir := t.TempDir()
	// registryPath returns something like <dir>/.engine/registry.json
	// Create the .engine dir as a file so MkdirAll fails.
	engineDir := filepath.Join(dir, ".engine")
	if err := os.WriteFile(engineDir, []byte("not a dir"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := saveRegistry(dir, []RegistryEntry{})
	if err == nil {
		t.Fatal("expected saveRegistry to fail when parent dir cannot be created")
	}
}

func TestCloneRepoFn_DefaultFunctionExecutes(t *testing.T) {
	// Test failure path.
	dir := t.TempDir()
	err := cloneRepoFn("not-a-real-repo-url", filepath.Join(dir, "dest"))
	if err == nil {
		t.Fatal("expected cloneRepoFn to fail for invalid source")
	}
}

func TestCloneRepoFn_SuccessPath(t *testing.T) {
	// Create a local bare git repo to clone from, exercising the success path.
	src := t.TempDir()
	dst := t.TempDir()
	dest := filepath.Join(dst, "cloned")

	// Init a bare repo in src.
	if err := exec.Command("git", "init", "--bare", src).Run(); err != nil {
		t.Skip("git not available:", err)
	}
	orig := cloneRepoFn
	t.Cleanup(func() { cloneRepoFn = orig })
	if err := cloneRepoFn(src, dest); err != nil {
		t.Fatalf("expected cloneRepoFn to succeed cloning a bare repo: %v", err)
	}
}

func TestAddToRegistry_LoadRegistryAndSaveErrors(t *testing.T) {
	dir := t.TempDir()
	repoDir := filepath.Join(dir, "repo")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Write bad JSON to registry file so LoadRegistry fails.
	rp := registryPath(dir)
	if err := os.MkdirAll(filepath.Dir(rp), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rp, []byte("bad json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := AddToRegistry(dir, repoDir); err == nil {
		t.Fatal("expected AddToRegistry to fail when registry file is invalid JSON")
	}

	// Remove bad JSON, force save failure by making the project root read-only
	// so MkdirAll in saveRegistry cannot create .engine.
	if err := os.Remove(rp); err != nil {
		t.Fatal(err)
	}
	engineDir := filepath.Join(dir, ".engine")
	if err := os.RemoveAll(engineDir); err != nil {
		t.Fatal(err)
	}
	// Make dir read-only so os.MkdirAll(.engine) fails.
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o755) })
	if _, err := AddToRegistry(dir, repoDir); err == nil {
		t.Fatal("expected AddToRegistry to fail when dir is read-only (saveRegistry should fail)")
	}
}

func TestRemoveFromRegistry_LoadError(t *testing.T) {
	dir := t.TempDir()
	rp := registryPath(dir)
	if err := os.MkdirAll(rp, 0o755); err != nil {
		t.Fatal(err)
	}
	// rp is now a directory, not a file — LoadRegistry should fail.
	if err := RemoveFromRegistry(dir, "anything"); err == nil {
		t.Fatal("expected remove error when registry is invalid")
	}
}

func TestRemoveFromRegistry_NotFoundAppendsUnmatchedEntries(t *testing.T) {
	dir := t.TempDir()
	alpha := filepath.Join(dir, "alpha")
	beta := filepath.Join(dir, "beta")
	if err := os.MkdirAll(alpha, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(beta, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := AddToRegistry(dir, alpha); err != nil {
		t.Fatalf("AddToRegistry alpha: %v", err)
	}
	if _, err := AddToRegistry(dir, beta); err != nil {
		t.Fatalf("AddToRegistry beta: %v", err)
	}

	err := RemoveFromRegistry(dir, "missing")
	if err == nil || !strings.Contains(err.Error(), "repository not found") {
		t.Fatalf("expected not found error with existing entries, got %v", err)
	}
}

func TestRemoveFromRegistry_SaveError(t *testing.T) {
	dir := t.TempDir()
	repoDir := filepath.Join(dir, "myrepo")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := AddToRegistry(dir, repoDir); err != nil {
		t.Fatalf("AddToRegistry: %v", err)
	}
	// Now make the registry path a directory so save fails.
	rp := registryPath(dir)
	if err := os.Remove(rp); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(rp, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := RemoveFromRegistry(dir, "myrepo"); err == nil {
		t.Fatal("expected RemoveFromRegistry to fail when save fails")
	}
}

func TestSaveRegistry_MarshalError(t *testing.T) {
	orig := jsonMarshalFn
	t.Cleanup(func() { jsonMarshalFn = orig })
	jsonMarshalFn = func(_ any) ([]byte, error) {
		return nil, fmt.Errorf("mock marshal error")
	}
	dir := t.TempDir()
	if err := saveRegistry(dir, []RegistryEntry{}); err == nil {
		t.Fatal("expected saveRegistry to fail when marshal fails")
	}
}

func TestAddToRegistry_ResolvePathError(t *testing.T) {
	orig := filepathAbsFn
	t.Cleanup(func() { filepathAbsFn = orig })
	filepathAbsFn = func(_ string) (string, error) {
		return "", fmt.Errorf("mock abs error")
	}
	dir := t.TempDir()
	repoDir := t.TempDir()
	if _, err := AddToRegistry(dir, repoDir); err == nil {
		t.Fatal("expected AddToRegistry to fail when filepath.Abs fails")
	}
}
