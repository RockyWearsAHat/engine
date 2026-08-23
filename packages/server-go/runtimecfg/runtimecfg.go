package runtimecfg

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Settings holds runtime preferences that should not require process env mutation.
// Values are persisted per project in .engine/runtime-config.json.
type Settings struct {
	GitHubToken           string `json:"githubToken,omitempty"`
	GitHubOwner           string `json:"githubOwner,omitempty"`
	GitHubRepo            string `json:"githubRepo,omitempty"`
	AnthropicKey          string `json:"anthropicKey,omitempty"`
	OpenAIKey             string `json:"openaiKey,omitempty"`
	ModelProvider         string `json:"modelProvider,omitempty"`
	ActiveTeam            string `json:"activeTeam,omitempty"`
	OllamaBaseURL         string `json:"ollamaBaseUrl,omitempty"`
	OllamaNumCtx          string `json:"ollamaNumCtx,omitempty"`
	LlamacppBaseURL       string `json:"llamacppBaseUrl,omitempty"`
	Model                 string `json:"model,omitempty"`
	ClonesDir             string `json:"clonesDir,omitempty"`
	ContextMaxTokens      string `json:"contextMaxTokens,omitempty"`
	ContextRecentWindow   string `json:"contextRecentWindow,omitempty"`
	ListDirectoryMaxChars string `json:"listDirectoryMaxChars,omitempty"`
	LocalFirst            string `json:"localFirst,omitempty"`
	Hybrid                string `json:"hybrid,omitempty"`
	LlamacppModel         string `json:"llamacppModel,omitempty"`
	OllamaModel           string `json:"ollamaModel,omitempty"`
	PlannerProvider       string `json:"plannerProvider,omitempty"`
	PlannerModel          string `json:"plannerModel,omitempty"`
	ReviewerProvider      string `json:"reviewerProvider,omitempty"`
	ReviewerModel         string `json:"reviewerModel,omitempty"`
}

// Patch is a partial update where nil means "leave unchanged".
type Patch struct {
	GitHubToken           *string
	GitHubOwner           *string
	GitHubRepo            *string
	AnthropicKey          *string
	OpenAIKey             *string
	ModelProvider         *string
	ActiveTeam            *string
	OllamaBaseURL         *string
	OllamaNumCtx          *string
	LlamacppBaseURL       *string
	Model                 *string
	ClonesDir             *string
	ContextMaxTokens      *string
	ContextRecentWindow   *string
	ListDirectoryMaxChars *string
	LocalFirst            *string
	Hybrid                *string
	LlamacppModel         *string
	OllamaModel           *string
	PlannerProvider       *string
	PlannerModel          *string
	ReviewerProvider      *string
	ReviewerModel         *string
}

var mu sync.Mutex

func configPath(projectPath string) string {
	root := strings.TrimSpace(projectPath)
	if root == "" {
		root = "."
	}
	return filepath.Join(root, ".engine", "runtime-config.json")
}

func trimSettings(s *Settings) {
	s.GitHubToken = strings.TrimSpace(s.GitHubToken)
	s.GitHubOwner = strings.TrimSpace(s.GitHubOwner)
	s.GitHubRepo = strings.TrimSpace(s.GitHubRepo)
	s.AnthropicKey = strings.TrimSpace(s.AnthropicKey)
	s.OpenAIKey = strings.TrimSpace(s.OpenAIKey)
	s.ModelProvider = strings.TrimSpace(s.ModelProvider)
	s.ActiveTeam = strings.TrimSpace(s.ActiveTeam)
	s.OllamaBaseURL = strings.TrimSpace(s.OllamaBaseURL)
	s.OllamaNumCtx = strings.TrimSpace(s.OllamaNumCtx)
	s.LlamacppBaseURL = strings.TrimSpace(s.LlamacppBaseURL)
	s.Model = strings.TrimSpace(s.Model)
	s.ClonesDir = strings.TrimSpace(s.ClonesDir)
	s.ContextMaxTokens = strings.TrimSpace(s.ContextMaxTokens)
	s.ContextRecentWindow = strings.TrimSpace(s.ContextRecentWindow)
	s.ListDirectoryMaxChars = strings.TrimSpace(s.ListDirectoryMaxChars)
	s.LocalFirst = strings.TrimSpace(s.LocalFirst)
	s.Hybrid = strings.TrimSpace(s.Hybrid)
	s.LlamacppModel = strings.TrimSpace(s.LlamacppModel)
	s.OllamaModel = strings.TrimSpace(s.OllamaModel)
	s.PlannerProvider = strings.TrimSpace(s.PlannerProvider)
	s.PlannerModel = strings.TrimSpace(s.PlannerModel)
	s.ReviewerProvider = strings.TrimSpace(s.ReviewerProvider)
	s.ReviewerModel = strings.TrimSpace(s.ReviewerModel)
}

func applyField(dst *string, src *string) {
	if src == nil {
		return
	}
	*dst = strings.TrimSpace(*src)
}

func applyPatch(dst *Settings, patch Patch) {
	applyField(&dst.GitHubToken, patch.GitHubToken)
	applyField(&dst.GitHubOwner, patch.GitHubOwner)
	applyField(&dst.GitHubRepo, patch.GitHubRepo)
	applyField(&dst.AnthropicKey, patch.AnthropicKey)
	applyField(&dst.OpenAIKey, patch.OpenAIKey)
	applyField(&dst.ModelProvider, patch.ModelProvider)
	applyField(&dst.ActiveTeam, patch.ActiveTeam)
	applyField(&dst.OllamaBaseURL, patch.OllamaBaseURL)
	applyField(&dst.OllamaNumCtx, patch.OllamaNumCtx)
	applyField(&dst.LlamacppBaseURL, patch.LlamacppBaseURL)
	applyField(&dst.Model, patch.Model)
	applyField(&dst.ClonesDir, patch.ClonesDir)
	applyField(&dst.ContextMaxTokens, patch.ContextMaxTokens)
	applyField(&dst.ContextRecentWindow, patch.ContextRecentWindow)
	applyField(&dst.ListDirectoryMaxChars, patch.ListDirectoryMaxChars)
	applyField(&dst.LocalFirst, patch.LocalFirst)
	applyField(&dst.Hybrid, patch.Hybrid)
	applyField(&dst.LlamacppModel, patch.LlamacppModel)
	applyField(&dst.OllamaModel, patch.OllamaModel)
	applyField(&dst.PlannerProvider, patch.PlannerProvider)
	applyField(&dst.PlannerModel, patch.PlannerModel)
	applyField(&dst.ReviewerProvider, patch.ReviewerProvider)
	applyField(&dst.ReviewerModel, patch.ReviewerModel)
}

// Load reads per-project runtime settings from disk.
func Load(projectPath string) (Settings, error) {
	mu.Lock()
	defer mu.Unlock()

	path := configPath(projectPath)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Settings{}, nil
		}
		return Settings{}, err
	}
	if strings.TrimSpace(string(data)) == "" {
		return Settings{}, nil
	}
	var cfg Settings
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Settings{}, err
	}
	trimSettings(&cfg)
	return cfg, nil
}

// Apply merges a patch into per-project runtime settings and persists it.
func Apply(projectPath string, patch Patch) (Settings, error) {
	mu.Lock()
	defer mu.Unlock()

	path := configPath(projectPath)
	cfg := Settings{}
	if data, err := os.ReadFile(path); err == nil && strings.TrimSpace(string(data)) != "" {
		if err := json.Unmarshal(data, &cfg); err != nil {
			return Settings{}, err
		}
	} else if err != nil && !os.IsNotExist(err) {
		return Settings{}, err
	}

	applyPatch(&cfg, patch)
	trimSettings(&cfg)

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return Settings{}, err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return Settings{}, err
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return Settings{}, err
	}
	return cfg, nil
}
