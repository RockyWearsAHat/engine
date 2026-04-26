package ai

import (
	"encoding/json"
	"fmt"
	"strings"
)

var styleSignalKeywords = []string{
	"style",
	"theme",
	"look and feel",
	"design",
	"aesthetic",
	"tone",
	"brand",
}

// ProjectType classifies the kind of software artifact being built.
type ProjectType string

const (
	ProjectTypeWebApp  ProjectType = "web-app"
	ProjectTypeRestAPI ProjectType = "rest-api"
	ProjectTypeCLI     ProjectType = "cli"
	ProjectTypeLibrary ProjectType = "library"
	ProjectTypeService ProjectType = "service"
	ProjectTypeUnknown ProjectType = "unknown"
)

// VerificationStrategy describes how to confirm a project is behaviorally working.
type VerificationStrategy struct {
	// UsesPlaywright indicates browser-level Playwright checks should run.
	UsesPlaywright bool `json:"usesPlaywright"`
	// StartCmd is the command that starts the project for verification.
	StartCmd string `json:"startCmd"`
	// CheckURL is the URL to verify for web/API/service projects.
	CheckURL string `json:"checkURL"`
	// Port is the port the project listens on, 0 if not applicable.
	Port int `json:"port"`
	// CheckCmds are shell commands used to verify CLI and library projects.
	CheckCmds []string `json:"checkCmds"`
}

// ProjectProfile is the structured intake artifact produced from the first user
// message (or a GitHub README tagged @engine). It captures what the project is,
// what "done" looks like, and how to verify the project is live and working.
// Everything downstream — the behavioral completion gate, WORKING_BEHAVIORS
// generation, and live checks — is driven by this profile.
type ProjectProfile struct {
	// ProjectPath is the absolute path to the project root.
	ProjectPath string `json:"projectPath"`
	// Type is the classified kind of project.
	Type ProjectType `json:"type"`
	// DoneDefinition is the list of success criteria stated by the user.
	DoneDefinition []string `json:"doneDefinition"`
	// DeployTarget describes where the finished artifact should end up.
	DeployTarget string `json:"deployTarget"`
	// Verification holds the derived strategy for behaviorally checking the project.
	Verification VerificationStrategy `json:"verification"`
	// LiveCheckCmd is a single command confirming the project is live and reachable.
	LiveCheckCmd string `json:"liveCheckCmd"`
	// WorkingBehaviors is a list of user-visible behaviors analogous to
	// WORKING_BEHAVIORS.md for this project.
	WorkingBehaviors []string `json:"workingBehaviors"`
}

// DeriveVerificationStrategy returns a VerificationStrategy with sensible defaults
// for the given project type. Callers may override individual fields after.
func DeriveVerificationStrategy(ptype ProjectType) VerificationStrategy {
	switch ptype {
	case ProjectTypeWebApp:
		return VerificationStrategy{
			UsesPlaywright: true,
			StartCmd:       "pnpm dev",
			Port:           3000,
			CheckURL:       "http://localhost:3000",
		}
	case ProjectTypeRestAPI:
		return VerificationStrategy{
			UsesPlaywright: false,
			StartCmd:       "go run . || npm start",
			Port:           8080,
			CheckURL:       "http://localhost:8080/health",
			CheckCmds:      []string{"curl -sf http://localhost:8080/health"},
		}
	case ProjectTypeCLI:
		return VerificationStrategy{
			UsesPlaywright: false,
			CheckCmds:      []string{"go build -o /tmp/cli-check . && /tmp/cli-check --version"},
		}
	case ProjectTypeLibrary:
		return VerificationStrategy{
			UsesPlaywright: false,
			CheckCmds:      []string{"go test ./... || pnpm test || cargo test"},
		}
	case ProjectTypeService:
		return VerificationStrategy{
			UsesPlaywright: false,
			StartCmd:       "docker compose up -d",
			Port:           8080,
			CheckURL:       "http://localhost:8080/health",
			CheckCmds:      []string{"curl -sf http://localhost:8080/health"},
		}
	default:
		return VerificationStrategy{}
	}
}

// intakeResponseSchema is the JSON shape the RoleIntaker LLM must return.
const intakeResponseSchema = `{
  "projectPath": "<absolute path>",
  "type": "<web-app|rest-api|cli|library|service|unknown>",
  "doneDefinition": ["<success criterion>", "..."],
  "deployTarget": "<local|Vercel|Docker|AWS|GitHub Pages|npm|...>",
  "verification": {
    "usesPlaywright": <true|false>,
    "startCmd": "<command to start, or empty>",
    "checkURL": "<URL to verify, or empty>",
    "port": <port number or 0>,
    "checkCmds": ["<verification command>", "..."]
  },
  "liveCheckCmd": "<single shell command proving it is live>",
  "workingBehaviors": ["<user-visible behavior>", "..."]
}`

// ParseProjectProfileJSON extracts a ProjectProfile from an LLM response.
// It tolerates surrounding prose and finds the first complete JSON object.
// If the parsed profile has an empty verification strategy, defaults are applied
// via DeriveVerificationStrategy based on the detected project type.
func ParseProjectProfileJSON(response string) (*ProjectProfile, error) {
	start := strings.Index(response, "{")
	if start == -1 {
		return nil, fmt.Errorf("no JSON object found in response")
	}

	depth := 0
	end := -1
	for i := start; i < len(response); i++ {
		switch response[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				end = i
			}
		}
		if end != -1 {
			break
		}
	}
	if end == -1 {
		return nil, fmt.Errorf("unterminated JSON object in response")
	}

	var profile ProjectProfile
	if err := json.Unmarshal([]byte(response[start:end+1]), &profile); err != nil {
		return nil, fmt.Errorf("parse profile JSON: %w", err)
	}

	// Apply default verification strategy when the LLM returned an empty one.
	if profile.Verification.StartCmd == "" && profile.Verification.CheckURL == "" &&
		len(profile.Verification.CheckCmds) == 0 && !profile.Verification.UsesPlaywright {
		profile.Verification = DeriveVerificationStrategy(profile.Type)
	}

	return &profile, nil
}

// BuildPreStartExpansion expands the current request into structured context that
// can be injected into the system prompt before the main agent loop starts.
// This gives the model an explicit objective/constraints plan for the turn.
func BuildPreStartExpansion(userMessage, projectDirection string) string {
	objective := truncateForPrompt(firstNonEmptyLine(userMessage), 240)
	if objective == "" {
		objective = "No explicit objective provided. Infer safest high-impact objective from context."
	}

	criteria := extractSuccessCriteria(userMessage)
	if len(criteria) == 0 {
		criteria = []string{"Ship a working solution with project-appropriate verification."}
	}

	assumption := "No style-specific preference was provided. Choose a sensible default based on established patterns."
	if HasExplicitStyleGuidance(userMessage) {
		assumption = "Style guidance is provided in the request. Follow it exactly unless it conflicts with safety/correctness."
	}

	parts := []string{
		"Pre-start expansion:",
		"- Objective: " + objective,
		"- Success criteria: " + strings.Join(criteria, " | "),
		"- Direction context: " + truncateForPrompt(strings.TrimSpace(projectDirection), 260),
		"- Style assumption: " + assumption,
		"- Communication mode: Keep updates minimal and action-focused; avoid verbose status chatter.",
	}

	return strings.Join(parts, "\n")
}

// HasExplicitStyleGuidance returns true when the request contains explicit style
// direction. Used to decide whether a style-assumption notice should be sent.
func HasExplicitStyleGuidance(userMessage string) bool {
	lower := strings.ToLower(userMessage)
	for _, token := range styleSignalKeywords {
		if strings.Contains(lower, token) {
			return true
		}
	}
	return false
}

// BuildStyleAssumptionNotice returns a user-facing note used in chat/Discord
// when style guidance was omitted from the request.
func BuildStyleAssumptionNotice() string {
	return "I selected the implementation style because style direction was not specified. I followed established design patterns and project conventions. If you want a different style, tell me and I will reshape it."
}

// BuildHeuristicProjectProfile builds a structured project profile from the
// first request and direction context when no persisted profile exists yet.
func BuildHeuristicProjectProfile(projectPath, userMessage, projectDirection string) ProjectProfile {
	ptype := detectProjectTypeHeuristic(userMessage, projectDirection)
	verification := DeriveVerificationStrategy(ptype)

	done := extractSuccessCriteria(userMessage)
	if len(done) == 0 {
		done = []string{"Deliver a verified, working implementation of the requested project."}
	}

	behaviors := make([]string, 0, len(done))
	for _, d := range done {
		behaviors = append(behaviors, "User can "+strings.TrimSpace(strings.TrimSuffix(strings.ToLower(d), ".")))
	}

	liveCheck := ""
	if strings.TrimSpace(verification.CheckURL) != "" {
		liveCheck = "curl -sf " + verification.CheckURL
	} else if len(verification.CheckCmds) > 0 {
		liveCheck = verification.CheckCmds[0]
	}

	return ProjectProfile{
		ProjectPath:      projectPath,
		Type:             ptype,
		DoneDefinition:   done,
		DeployTarget:     detectDeployTargetHeuristic(userMessage),
		Verification:     verification,
		LiveCheckCmd:     liveCheck,
		WorkingBehaviors: behaviors,
	}
}

func detectProjectTypeHeuristic(userMessage, projectDirection string) ProjectType {
	lower := strings.ToLower(userMessage + "\n" + projectDirection)
	switch {
	case strings.Contains(lower, "rest api") || strings.Contains(lower, "endpoint") || strings.Contains(lower, "http api"):
		return ProjectTypeRestAPI
	case strings.Contains(lower, "cli") || strings.Contains(lower, "command line") || strings.Contains(lower, "terminal tool"):
		return ProjectTypeCLI
	case strings.Contains(lower, "library") || strings.Contains(lower, "sdk") || strings.Contains(lower, "package"):
		return ProjectTypeLibrary
	case strings.Contains(lower, "service") || strings.Contains(lower, "daemon") || strings.Contains(lower, "worker"):
		return ProjectTypeService
	case strings.Contains(lower, "web") || strings.Contains(lower, "frontend") || strings.Contains(lower, "ui") || strings.Contains(lower, "website"):
		return ProjectTypeWebApp
	default:
		return ProjectTypeUnknown
	}
}

func detectDeployTargetHeuristic(userMessage string) string {
	lower := strings.ToLower(userMessage)
	switch {
	case strings.Contains(lower, "vercel"):
		return "Vercel"
	case strings.Contains(lower, "docker"):
		return "Docker"
	case strings.Contains(lower, "aws"):
		return "AWS"
	case strings.Contains(lower, "github pages"):
		return "GitHub Pages"
	case strings.Contains(lower, "npm"):
		return "npm"
	default:
		return "local"
	}
}

func extractSuccessCriteria(userMessage string) []string {
	parts := strings.FieldsFunc(userMessage, func(r rune) bool {
		return r == '\n' || r == '.' || r == ';'
	})
	out := make([]string, 0, 6)
	for _, p := range parts {
		item := strings.TrimSpace(p)
		if len(item) < 10 {
			continue
		}
		out = append(out, truncateForPrompt(item, 140))
		if len(out) >= 6 {
			break
		}
	}
	return out
}

func firstNonEmptyLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			return line
		}
	}
	return ""
}

func truncateForPrompt(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	if n <= 3 {
		return s[:n]
	}
	return s[:n-3] + "..."
}
