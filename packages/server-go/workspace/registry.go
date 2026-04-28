package workspace

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const registryFileName = "registry.json"

// RegistryEntry describes one repository Engine is responsible for.
type RegistryEntry struct {
	Name      string `json:"name"`
	LocalPath string `json:"localPath"`
	URL       string `json:"url"`
}

// registryPath returns the path to the registry file inside the project's .engine dir.
func registryPath(projectRoot string) string {
	return filepath.Join(projectRoot, ".engine", registryFileName)
}

// LoadRegistry reads all entries from the registry file.
// Returns an empty slice when the file does not exist.
func LoadRegistry(projectRoot string) ([]RegistryEntry, error) {
	data, err := os.ReadFile(registryPath(projectRoot))
	if os.IsNotExist(err) {
		return []RegistryEntry{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read registry: %w", err)
	}
	var entries []RegistryEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, fmt.Errorf("parse registry: %w", err)
	}
	return entries, nil
}

// saveRegistry writes entries to disk, creating parent directories as needed.
func saveRegistry(projectRoot string, entries []RegistryEntry) error {
	rp := registryPath(projectRoot)
	if err := os.MkdirAll(filepath.Dir(rp), 0o755); err != nil {
		return fmt.Errorf("create registry dir: %w", err)
	}
	data, err := jsonMarshalFn(entries)
	if err != nil {
		return fmt.Errorf("marshal registry: %w", err)
	}
	return os.WriteFile(rp, data, 0o644)
}

// cloneRepoFn is injectable for tests.
var cloneRepoFn = func(url, dest string) error {
	cmd := exec.Command("git", "clone", url, dest) //nolint:gosec
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// jsonMarshalFn is injectable for tests.
var jsonMarshalFn = func(v any) ([]byte, error) {
	return json.MarshalIndent(v, "", "  ")
}

// filepathAbsFn is injectable for tests.
var filepathAbsFn = filepath.Abs

// AddToRegistry adds a repository by local path or remote URL.
// For URLs the repo is cloned under <projectRoot>/.engine/projects/<name> by default.
// Returns the new entry. Duplicate entries (same resolved local path) are skipped.
func AddToRegistry(projectRoot, urlOrPath string) (*RegistryEntry, error) {
	clean := strings.TrimSpace(urlOrPath)
	if clean == "" {
		return nil, fmt.Errorf("path or URL is required")
	}

	isURL := strings.HasPrefix(clean, "https://") ||
		strings.HasPrefix(clean, "http://") ||
		strings.HasPrefix(clean, "git@")

	localPath := clean
	repoURL := ""
	if isURL {
		repoURL = clean
		name := strings.TrimSuffix(filepath.Base(clean), ".git")
		clonesDir := strings.TrimSpace(os.Getenv("ENGINE_CLONES_DIR"))
		if clonesDir == "" {
			clonesDir = filepath.Join(projectRoot, ".engine", "projects")
		}
		dest := filepath.Join(clonesDir, name)
		if _, err := os.Stat(dest); os.IsNotExist(err) {
			if err := cloneRepoFn(clean, dest); err != nil {
				return nil, fmt.Errorf("clone %s: %w", clean, err)
			}
		}
		localPath = dest
	}

	abs, err := filepathAbsFn(localPath)
	if err != nil {
		return nil, fmt.Errorf("resolve path: %w", err)
	}
	if st, statErr := os.Stat(abs); statErr != nil || !st.IsDir() {
		return nil, fmt.Errorf("path must be an existing directory: %s", abs)
	}

	entries, err := LoadRegistry(projectRoot)
	if err != nil {
		return nil, err
	}
	// Skip duplicate.
	for _, e := range entries {
		if e.LocalPath == abs {
			return &e, nil
		}
	}

	entry := RegistryEntry{
		Name:      filepath.Base(abs),
		LocalPath: abs,
		URL:       repoURL,
	}
	entries = append(entries, entry)
	if err := saveRegistry(projectRoot, entries); err != nil {
		return nil, err
	}
	return &entry, nil
}

// RemoveFromRegistry removes the entry with the given name.
// Returns an error if the name does not match any entry.
func RemoveFromRegistry(projectRoot, name string) error {
	ref := strings.TrimSpace(name)
	if ref == "" {
		return fmt.Errorf("name is required")
	}

	entries, err := LoadRegistry(projectRoot)
	if err != nil {
		return err
	}

	next := entries[:0]
	found := false
	for _, e := range entries {
		if e.Name == ref {
			found = true
			continue
		}
		next = append(next, e)
	}
	if !found {
		return fmt.Errorf("repository not found: %s", ref)
	}
	return saveRegistry(projectRoot, next)
}
