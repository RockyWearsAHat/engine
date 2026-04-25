package fs

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

var binaryExts = map[string]bool{
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true,
	".svg": true, ".ico": true, ".bmp": true, ".tiff": true,
	".mp4": true, ".mov": true, ".avi": true, ".mkv": true, ".webm": true,
	".mp3": true, ".wav": true, ".ogg": true, ".flac": true,
	".zip": true, ".tar": true, ".gz": true, ".bz2": true, ".7z": true, ".rar": true,
	".ttf": true, ".otf": true, ".woff": true, ".woff2": true, ".eot": true,
	".exe": true, ".dll": true, ".so": true, ".dylib": true, ".a": true, ".o": true,
	".pdf": true, ".doc": true, ".docx": true, ".xls": true, ".xlsx": true,
	".db": true, ".sqlite": true, ".sqlite3": true,
	".pyc": true, ".pyo": true, ".class": true,
}

var langMap = map[string]string{
	".ts": "typescript", ".tsx": "typescriptreact", ".js": "javascript",
	".jsx": "javascriptreact", ".go": "go", ".rs": "rust", ".py": "python",
	".java": "java", ".c": "c", ".cpp": "cpp", ".h": "c", ".hpp": "cpp",
	".cs": "csharp", ".rb": "ruby", ".php": "php", ".swift": "swift",
	".kt": "kotlin", ".html": "html", ".css": "css", ".scss": "scss",
	".less": "less", ".json": "json", ".yaml": "yaml", ".yml": "yaml",
	".toml": "toml", ".md": "markdown", ".mdx": "markdown",
	".sh": "shell", ".bash": "shell", ".zsh": "shell",
	".sql": "sql", ".graphql": "graphql", ".proto": "protobuf",
	".xml": "xml", ".dockerfile": "dockerfile", ".tf": "hcl",
	".mod": "go", ".sum": "go",
}

// FileNode represents a file or directory in the tree.
type FileNode struct {
	Name        string      `json:"name"`
	Path        string      `json:"path"`
	Type        string      `json:"type"` // "file" | "directory"
	Children    []*FileNode `json:"children,omitempty"`
	Loaded      bool        `json:"loaded,omitempty"`
	HasChildren bool        `json:"hasChildren,omitempty"`
	Size        int64       `json:"size,omitempty"`
	Modified    string      `json:"modified,omitempty"`
}

// FileContent holds the result of reading a file.
type FileContent struct {
	Path     string `json:"path"`
	Content  string `json:"content"`
	Language string `json:"language"`
	Size     int64  `json:"size"`
}

// SearchResult holds a single text search match.
type SearchResult struct {
	Path    string `json:"path"`
	Line    int    `json:"line"`
	Column  int    `json:"column,omitempty"`
	Preview string `json:"preview"`
}

// ReadFile reads a text file and returns its content with language detection.
func ReadFile(path string) (*FileContent, error) {
	ext := strings.ToLower(filepath.Ext(path))
	if binaryExts[ext] {
		return nil, fmt.Errorf("binary file not supported: %s", path)
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("stat %s: %w", path, err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	lang := langMap[ext]
	if lang == "" {
		lang = "plaintext"
	}
	return &FileContent{
		Path:     path,
		Content:  string(data),
		Language: lang,
		Size:     info.Size(),
	}, nil
}

// WriteFile writes content to a file, creating parent directories as needed.
func WriteFile(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(path), err)
	}
	return os.WriteFile(path, []byte(content), 0644)
}

// GetTree returns a directory tree up to maxDepth levels deep.
func GetTree(root string, maxDepth int) (*FileNode, error) {
	return buildTree(root, 0, maxDepth)
}

func buildTree(path string, depth, maxDepth int) (*FileNode, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	node := &FileNode{
		Name:     info.Name(),
		Path:     path,
		Modified: info.ModTime().UTC().Format("2006-01-02T15:04:05Z"),
	}
	if info.IsDir() {
		node.Type = "directory"
		entries, err := os.ReadDir(path)
		if err != nil {
			node.Loaded = true
			return node, nil
		}
		node.HasChildren = len(entries) > 0
		if depth >= maxDepth {
			return node, nil
		}
		node.Loaded = true
		for _, e := range entries {
			child, err := buildTree(filepath.Join(path, e.Name()), depth+1, maxDepth)
			if err == nil {
				node.Children = append(node.Children, child)
			}
		}
	} else {
		node.Type = "file"
		node.Size = info.Size()
	}
	return node, nil
}

// SearchFiles runs ripgrep and formats matches as plain text for tool responses.
func SearchFiles(pattern, dir, fileGlob string) (string, error) {
	results, err := SearchMatches(pattern, dir, fileGlob)
	if err != nil {
		return "", err
	}
	if len(results) == 0 {
		return "No matches found", nil
	}

	var b strings.Builder
	for _, result := range results {
		fmt.Fprintf(&b, "%s:%d:%s\n", result.Path, result.Line, result.Preview)
	}

	formatted := strings.TrimSpace(b.String())
	if len(formatted) > 4*1024*1024 {
		formatted = formatted[:4*1024*1024] + "\n... (truncated)"
	}
	return formatted, nil
}

// SearchMatches runs ripgrep and returns structured search results.
func SearchMatches(pattern, dir, fileGlob string) ([]SearchResult, error) {
	args := []string{"--json", "--line-number", "--color=never", pattern}
	if fileGlob != "" {
		args = append(args, "--glob", fileGlob)
	}
	args = append(args, dir)

	out, err := exec.Command("rg", args...).CombinedOutput()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			return []SearchResult{}, nil
		}
		return nil, fmt.Errorf("search files: %w", err)
	}

	type ripgrepEvent struct {
		Type string `json:"type"`
		Data struct {
			Path struct {
				Text string `json:"text"`
			} `json:"path"`
			Lines struct {
				Text string `json:"text"`
			} `json:"lines"`
			LineNumber int `json:"line_number"`
			Submatches []struct {
				Start int `json:"start"`
			} `json:"submatches"`
		} `json:"data"`
	}

	results := make([]SearchResult, 0, 32)
	scanner := bufio.NewScanner(bytes.NewReader(out))
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)

	for scanner.Scan() {
		var event ripgrepEvent
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil || event.Type != "match" {
			continue
		}

		matchPath := event.Data.Path.Text
		if !filepath.IsAbs(matchPath) {
			matchPath = filepath.Join(dir, matchPath)
		}

		result := SearchResult{
			Path:    matchPath,
			Line:    event.Data.LineNumber,
			Preview: strings.TrimRight(event.Data.Lines.Text, "\r\n"),
		}
		if len(event.Data.Submatches) > 0 {
			result.Column = event.Data.Submatches[0].Start + 1
		}
		results = append(results, result)
		if len(results) >= 200 {
			break
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("parse search results: %w", err)
	}
	return results, nil
}

// DetectLanguage returns the language identifier for a file path.
func DetectLanguage(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	if lang, ok := langMap[ext]; ok {
		return lang
	}
	base := strings.ToLower(filepath.Base(path))
	switch base {
	case "dockerfile":
		return "dockerfile"
	case "makefile", "gnumakefile":
		return "makefile"
	}
	return "plaintext"
}
