package ai

import (
	"os"
	"path/filepath"
	"strings"
)

func readEngineConfigText(projectPath string) (string, bool) {
	if strings.TrimSpace(projectPath) == "" {
		return "", false
	}
	for _, name := range []string{"config.yaml", "config.example.yaml"} {
		configPath := filepath.Join(projectPath, ".engine", name)
		content, err := os.ReadFile(configPath)
		if err == nil {
			return string(content), true
		}
	}
	return "", false
}

// ResolveTeamOrchestratorModel reads .engine/config.yaml and resolves the
// selected team's orchestrator model/provider.
func ResolveTeamOrchestratorModel(projectPath string, selectedTeam string) (resolvedTeam string, provider string, model string, ok bool) {
	if strings.TrimSpace(projectPath) == "" {
		return "", "", "", false
	}
	teamName := strings.TrimSpace(selectedTeam)
	if teamName == "" {
		return "", "", "", false
	}

	content, ok := readEngineConfigText(projectPath)
	if !ok {
		return "", "", "", false
	}

	teamModels := parseTeamConfigYAML(content)

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

func parseDevLoopDefaultTeam(yaml string) string {
	if strings.TrimSpace(yaml) == "" {
		return ""
	}

	inDevLoop := false
	for _, rawLine := range strings.Split(yaml, "\n") {
		trimmedRight := strings.TrimRight(rawLine, " \t")
		trimmedLeft := strings.TrimLeft(trimmedRight, " \t")
		if strings.TrimSpace(trimmedLeft) == "" || strings.HasPrefix(trimmedLeft, "#") {
			continue
		}

		indent := len(trimmedRight) - len(trimmedLeft)
		if indent == 0 {
			inDevLoop = trimmedLeft == "dev_loop:"
			continue
		}
		if !inDevLoop || indent != 2 {
			continue
		}
		if strings.HasPrefix(trimmedLeft, "default_team:") {
			return parseYAMLValue(trimmedLeft, "default_team:")
		}
	}

	return ""
}

func parseTeamConfigYAML(yaml string) map[string]string {
	modelsByTeam := make(map[string]string)
	if strings.TrimSpace(yaml) == "" {
		return modelsByTeam
	}

	lines := strings.Split(yaml, "\n")
	inTeams := false
	currentTeam := ""
	currentRole := ""

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
			currentTeam = ""
			currentRole = ""
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

	return modelsByTeam
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
	// Team is the lead-selected team used to bootstrap autonomous runs.
	// Must map to teams.<name>.orchestrator.model in .engine/config.yaml.
	Team string
	// AssumptionTolerance controls how aggressively the agent resolves ambiguity
	// autonomously vs escalating to the user.
	//   "conservative" — ask the user on any meaningful ambiguity.
	//   "standard"     — resolve design ambiguity autonomously; escalate only for
	//                    credentials and destructive actions. (default when empty)
	//   "aggressive"   — resolve almost everything autonomously; escalate only for
	//                    credentials and irreversible actions with no safe default.
	AssumptionTolerance string
	// RequireExplicitPublishIntent blocks deploy/publish actions unless explicit
	// request evidence is present in execution intent.
	RequireExplicitPublishIntent bool
	// MinimalChatMode keeps runtime updates sparse and action-focused.
	MinimalChatMode bool
	// StyleAssumptionNotice enables one short style-assumption notice when style
	// direction is omitted on first-turn intake.
	StyleAssumptionNotice bool
}

// ResolveAutonomousPolicy reads the autonomous: section from .engine/config.yaml.
// Returns a zero-value policy (all defaults off) when the file is absent or the
// section is missing, so callers can safely fall through to RequestApproval.
func ResolveAutonomousPolicy(projectPath string) AutonomousPolicy {
	defaults := AutonomousPolicy{
		RequireExplicitPublishIntent: true,
		MinimalChatMode:              true,
		StyleAssumptionNotice:        true,
	}
	content, ok := readEngineConfigText(projectPath)
	if !ok {
		return defaults
	}
	return parseAutonomousPolicy(content)
}

// parseAutonomousPolicy extracts the autonomous: block from raw YAML text.
// Only indent-2 keys directly under autonomous: are read; deeper nesting is ignored.
func parseAutonomousPolicy(yaml string) AutonomousPolicy {
	p := AutonomousPolicy{
		RequireExplicitPublishIntent: true,
		MinimalChatMode:              true,
		StyleAssumptionNotice:        true,
	}
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
		case strings.HasPrefix(trimmedLeft, "team:"):
			p.Team = parseYAMLValue(trimmedLeft, "team:")
		case strings.HasPrefix(trimmedLeft, "assumption_tolerance:"):
			p.AssumptionTolerance = parseYAMLValue(trimmedLeft, "assumption_tolerance:")
		case strings.HasPrefix(trimmedLeft, "require_explicit_publish_intent:"):
			p.RequireExplicitPublishIntent = strings.TrimSpace(strings.TrimPrefix(trimmedLeft, "require_explicit_publish_intent:")) != "false"
		case strings.HasPrefix(trimmedLeft, "minimal_chat_mode:"):
			p.MinimalChatMode = strings.TrimSpace(strings.TrimPrefix(trimmedLeft, "minimal_chat_mode:")) != "false"
		case strings.HasPrefix(trimmedLeft, "style_assumption_notice:"):
			p.StyleAssumptionNotice = strings.TrimSpace(strings.TrimPrefix(trimmedLeft, "style_assumption_notice:")) != "false"
		}
	}
	return p
}

// ResolveAutonomousStartupTeam resolves the explicit lead-selected team for
// autonomous startup and returns the orchestrator provider/model for that team.
//
// Selection precedence:
// 1) explicitTeam argument (for example ENGINE_ACTIVE_TEAM when pre-set)
// 2) autonomous.team from .engine/config.yaml
// 3) dev_loop.default_team from .engine/config.yaml
// 4) teams.default when present
func ResolveAutonomousStartupTeam(projectPath string, explicitTeam string) (resolvedTeam string, provider string, model string, ok bool) {
	team := strings.TrimSpace(explicitTeam)
	if team != "" {
		return ResolveTeamOrchestratorModel(projectPath, team)
	}

	content, ok := readEngineConfigText(projectPath)
	if !ok {
		return "", "", "", false
	}

	policyTeam := strings.TrimSpace(parseAutonomousPolicy(content).Team)
	if policyTeam != "" {
		team = policyTeam
	} else {
		team = strings.TrimSpace(parseDevLoopDefaultTeam(content))
		if team == "" {
			if _, exists := parseTeamConfigYAML(content)["default"]; exists {
				team = "default"
			}
		}
	}
	if team == "" {
		return "", "", "", false
	}

	return ResolveTeamOrchestratorModel(projectPath, team)
}
