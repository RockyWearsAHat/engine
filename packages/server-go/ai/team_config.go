package ai

import (
	"os"
	"path/filepath"
	"strings"
)

// ResolveTeamOrchestratorModel reads .engine/config.yaml and resolves the
// selected team's orchestrator model/provider. When selectedTeam is empty,
// dev_loop.default_team is used as fallback.
func ResolveTeamOrchestratorModel(projectPath string, selectedTeam string) (resolvedTeam string, provider string, model string, ok bool) {
	if strings.TrimSpace(projectPath) == "" {
		return "", "", "", false
	}

	configPath := filepath.Join(projectPath, ".engine", "config.yaml")
	content, err := os.ReadFile(configPath)
	if err != nil {
		return "", "", "", false
	}

	teamModels, defaultTeam := parseTeamConfigYAML(string(content))
	teamName := strings.TrimSpace(selectedTeam)
	if teamName == "" {
		teamName = strings.TrimSpace(defaultTeam)
	}
	if teamName == "" {
		return "", "", "", false
	}

	fullModel := strings.TrimSpace(teamModels[teamName])
	if fullModel == "" {
		return "", "", "", false
	}

	teamProvider, teamModel := splitProviderModel(fullModel)
	if teamProvider == "" {
		teamProvider = inferredProviderForModel(teamModel)
	}

	return teamName, teamProvider, teamModel, true
}

func parseTeamConfigYAML(yaml string) (map[string]string, string) {
	modelsByTeam := make(map[string]string)
	if strings.TrimSpace(yaml) == "" {
		return modelsByTeam, ""
	}

	lines := strings.Split(yaml, "\n")
	inTeams := false
	inDevLoop := false
	currentTeam := ""
	currentRole := ""
	defaultTeam := ""

	for _, rawLine := range lines {
		trimmedRight := strings.TrimRight(rawLine, " \t")
		if strings.TrimSpace(trimmedRight) == "" {
			continue
		}

		trimmedLeft := strings.TrimLeft(trimmedRight, " \t")
		if strings.HasPrefix(trimmedLeft, "#") {
			continue
		}

		indent := len(trimmedRight) - len(trimmedLeft)
		if indent == 0 {
			inTeams = trimmedLeft == "teams:"
			inDevLoop = trimmedLeft == "dev_loop:"
			currentTeam = ""
			currentRole = ""
			continue
		}

		if inDevLoop && indent == 2 && strings.HasPrefix(trimmedLeft, "default_team:") {
			defaultTeam = parseYAMLValue(trimmedLeft, "default_team:")
			continue
		}

		if !inTeams {
			continue
		}

		if indent == 2 {
			currentTeam = strings.TrimSuffix(trimmedLeft, ":")
			currentRole = ""
			continue
		}

		if currentTeam == "" {
			continue
		}

		if indent == 4 {
			currentRole = strings.TrimSuffix(trimmedLeft, ":")
			continue
		}

		if indent == 6 && currentRole == "orchestrator" && strings.HasPrefix(trimmedLeft, "model:") {
			modelsByTeam[currentTeam] = parseYAMLValue(trimmedLeft, "model:")
		}
	}

	return modelsByTeam, defaultTeam
}

func parseYAMLValue(line string, key string) string {
	value := strings.TrimSpace(strings.TrimPrefix(line, key))
	if len(value) >= 2 {
		if (strings.HasPrefix(value, "\"") && strings.HasSuffix(value, "\"")) ||
			(strings.HasPrefix(value, "'") && strings.HasSuffix(value, "'")) {
			return value[1 : len(value)-1]
		}
	}
	return value
}

func splitProviderModel(model string) (provider string, modelName string) {
	value := strings.TrimSpace(model)
	if value == "" {
		return "", ""
	}
	parts := strings.SplitN(value, ":", 2)
	if len(parts) != 2 {
		return "", value
	}
	return strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
}

// AutonomousPolicy holds per-project configuration for headless autonomous sessions.
// Read from the autonomous: section of .engine/config.yaml.
type AutonomousPolicy struct {
	// AutoCommit: stage and commit without prompting for user approval.
	AutoCommit bool
	// AutoPush: push to remote without approval (only takes effect when AutoCommit is true).
	AutoPush bool
	// Branch: branch to work on; empty means use the current branch.
	Branch string
	// AssumptionTolerance controls how aggressively the agent resolves ambiguity
	// autonomously vs escalating to the user.
	//   "conservative" — ask the user on any meaningful ambiguity.
	//   "standard"     — resolve design ambiguity autonomously; escalate only for
	//                    credentials and destructive actions. (default when empty)
	//   "aggressive"   — resolve almost everything autonomously; escalate only for
	//                    credentials and irreversible actions with no safe default.
	AssumptionTolerance string
}

// ResolveAutonomousPolicy reads the autonomous: section from .engine/config.yaml.
// Returns a zero-value policy (all defaults off) when the file is absent or the
// section is missing, so callers can safely fall through to RequestApproval.
func ResolveAutonomousPolicy(projectPath string) AutonomousPolicy {
	if strings.TrimSpace(projectPath) == "" {
		return AutonomousPolicy{}
	}
	configPath := filepath.Join(projectPath, ".engine", "config.yaml")
	content, err := os.ReadFile(configPath)
	if err != nil {
		return AutonomousPolicy{}
	}
	return parseAutonomousPolicy(string(content))
}

// parseAutonomousPolicy extracts the autonomous: block from raw YAML text.
// Only indent-2 keys directly under autonomous: are read; deeper nesting is ignored.
func parseAutonomousPolicy(yaml string) AutonomousPolicy {
	var p AutonomousPolicy
	inAutonomous := false
	for _, rawLine := range strings.Split(yaml, "\n") {
		trimmedRight := strings.TrimRight(rawLine, " \t")
		trimmedLeft := strings.TrimLeft(trimmedRight, " \t")
		if strings.TrimSpace(trimmedLeft) == "" || strings.HasPrefix(trimmedLeft, "#") {
			continue
		}
		indent := len(trimmedRight) - len(trimmedLeft)
		if indent == 0 {
			inAutonomous = trimmedLeft == "autonomous:"
			continue
		}
		if !inAutonomous || indent != 2 {
			continue
		}
		switch {
		case strings.HasPrefix(trimmedLeft, "auto_commit:"):
			p.AutoCommit = strings.TrimSpace(strings.TrimPrefix(trimmedLeft, "auto_commit:")) == "true"
		case strings.HasPrefix(trimmedLeft, "auto_push:"):
			p.AutoPush = strings.TrimSpace(strings.TrimPrefix(trimmedLeft, "auto_push:")) == "true"
		case strings.HasPrefix(trimmedLeft, "branch:"):
			p.Branch = parseYAMLValue(trimmedLeft, "branch:")
		case strings.HasPrefix(trimmedLeft, "assumption_tolerance:"):
			p.AssumptionTolerance = parseYAMLValue(trimmedLeft, "assumption_tolerance:")
		}
	}
	return p
}