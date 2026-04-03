package fs

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

var ignoreDirs = map[string]bool{
	".git": true, "node_modules": true, "dist": true, "out": true,
	"build": true, ".myeditor": true, ".DS_Store": true,
	"target": true, "__pycache__": true, ".next": true, ".nuxt": true,
}

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
	Name     string      `json:"name"`
	Path     string      `json:"path"`
	Type     string      `json:"type"` // "file" | "directory"
	Children []*FileNode `json:"children,omitempty"`
	Size     int64       `json:"size,omitempty"`
	Modified string      `json:"modified,omitempty"`
}

// FileContent holds the result of reading a file.
type FileContent struct {
	Path     string `json:"path"`
	Content  string `json:"content"`
	Language string `json:"language"`
	Size     int64  `json:"size"`
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
		if depth >= maxDepth {
			return node, nil
		}
		entries, err := os.ReadDir(path)
		if err != nil {
			return node, nil
		}
		for _, e := range entries {
			if ignoreDirs[e.Name()] || strings.HasPrefix(e.Name(), ".") && e.Name() != ".env" {
				continue
			}
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

// SearchFiles runs ripgrep to search for a pattern in files.
func SearchFiles(pattern, dir, fileGlob string) (string, error) {
	args := []string{"--line-number", "--with-filename", "--color=never", pattern}
	if fileGlob != "" {
		args = append(args, "--glob", fileGlob)
	}
	args = append(args, dir)

	out, err := exec.Command("rg", args...).CombinedOutput()
	if err != nil {
		// exit code 1 means no matches — not a real error
		if len(out) == 0 {
			return "No matches found", nil
		}
	}
	result := strings.TrimSpace(string(out))
	if result == "" {
		return "No matches found", nil
	}
	// Truncate to 4MB
	if len(result) > 4*1024*1024 {
		result = result[:4*1024*1024] + "\n... (truncated)"
	}
	return result, nil
}

// DetectLanguage returns the Monaco language ID for a file path.
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
