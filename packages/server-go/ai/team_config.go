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