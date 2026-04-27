package ai

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// HandoffObjective captures the current request objective and priority.
type HandoffObjective struct {
	Statement string `json:"statement"`
	Priority  string `json:"priority"`
}

// HandoffScope captures what is in and out of scope for the current turn.
type HandoffScope struct {
	InScope  []string `json:"inScope"`
	OutScope []string `json:"outScope"`
}

// HandoffSuccessCriteria captures completion conditions for the turn.
type HandoffSuccessCriteria struct {
	Items                []string `json:"items"`
	CompletionDefinition string   `json:"completionDefinition"`
}

// HandoffStyle captures explicit style settings and one-time assumption notices.
type HandoffStyle struct {
	ExplicitStyleProvided bool   `json:"explicitStyleProvided"`
	AssumedStyle          string `json:"assumedStyle"`
	AssumptionNoticeSent  struct {
		Chat    bool `json:"chat"`
		Discord bool `json:"discord"`
	} `json:"assumptionNoticeSent"`
}

// HandoffContinuity tracks completed and next autonomous steps.
type HandoffContinuity struct {
	CompletedSteps []string `json:"completedSteps"`
	NextStep       string   `json:"nextStep"`
	KnownRisks     []string `json:"knownRisks"`
	Unresolved     []string `json:"unresolvedQuestions"`
}

// HandoffObservability captures rollout-safety and trace metadata.
type HandoffObservability struct {
	CanaryEnabled bool     `json:"canaryEnabled"`
	KillSwitches  []string `json:"killSwitches"`
	TraceID       string   `json:"traceId"`
}

// AutonomyHandoff is the required persisted context payload passed across hops.
type AutonomyHandoff struct {
	Version         string                `json:"version"`
	RequestID       string                `json:"requestId"`
	SessionID       string                `json:"sessionId"`
	ProjectPath     string                `json:"projectPath"`
	Objective       HandoffObjective      `json:"objective"`
	Scope           HandoffScope          `json:"scope"`
	Constraints     []string              `json:"constraints"`
	SuccessCriteria HandoffSuccessCriteria `json:"successCriteria"`
	ProjectProfile  *ProjectProfile       `json:"projectProfile"`
	ExecutionIntent ExecutionIntent       `json:"executionIntent"`
	Style           HandoffStyle          `json:"style"`
	Continuity      HandoffContinuity     `json:"continuity"`
	Observability   HandoffObservability  `json:"observability"`
}

// BuildAutonomyHandoff builds the per-turn context payload used for continuity.
func BuildAutonomyHandoff(requestID, sessionID, projectPath, userMessage, assistantSummary string, profile *ProjectProfile) AutonomyHandoff {
	criteria := extractSuccessCriteria(userMessage)
	if len(criteria) == 0 {
		criteria = []string{"Complete implementation with project-appropriate verification"}
	}
	inScope, outScope := deriveScope(userMessage)
	constraints := deriveConstraints(userMessage)

	execIntent := ExecutionIntent{PublishIntent: PublishIntentNone}
	if profile != nil {
		execIntent = profile.ExecutionIntent
		if execIntent.PublishIntent == "" {
			execIntent.PublishIntent = PublishIntentNone
		}
	}

	style := HandoffStyle{
		ExplicitStyleProvided: HasExplicitStyleGuidance(userMessage),
		AssumedStyle:          "convention-safe default",
	}
	style.AssumptionNoticeSent.Chat = !style.ExplicitStyleProvided
	style.AssumptionNoticeSent.Discord = !style.ExplicitStyleProvided

	continuity := HandoffContinuity{
		CompletedSteps: []string{"request intake captured", "pre-start expansion generated"},
		NextStep:       "execute highest-priority implementation step and run verification",
	}
	if strings.TrimSpace(assistantSummary) != "" {
		continuity.CompletedSteps = append(continuity.CompletedSteps, "latest summary updated")
	}
	if execIntent.PublishIntent != PublishIntentExplicit {
		continuity.KnownRisks = append(continuity.KnownRisks, "publish/deploy is denied until explicit evidence is provided")
	}

	return AutonomyHandoff{
		Version:     "v1",
		RequestID:   strings.TrimSpace(requestID),
		SessionID:   strings.TrimSpace(sessionID),
		ProjectPath: strings.TrimSpace(projectPath),
		Objective: HandoffObjective{
			Statement: truncateForPrompt(firstNonEmptyLine(userMessage), 240),
			Priority:  "high",
		},
		Scope: HandoffScope{
			InScope:  []string{inScope},
			OutScope: []string{outScope},
		},
		Constraints: constraints,
		SuccessCriteria: HandoffSuccessCriteria{
			Items:                criteria,
			CompletionDefinition: "all success criteria met and verification passes",
		},
		ProjectProfile:  profile,
		ExecutionIntent: execIntent,
		Style:           style,
		Continuity:      continuity,
		Observability: HandoffObservability{
			CanaryEnabled: false,
			KillSwitches:  []string{"publish-intent-gate", "minimal-chat-policy"},
			TraceID:       strings.TrimSpace(requestID),
		},
	}
}

// WriteAutonomyHandoffCache persists handoff context to .cache/autonomy-handoff.json.
func WriteAutonomyHandoffCache(projectPath string, handoff *AutonomyHandoff) error {
	if handoff == nil || strings.TrimSpace(projectPath) == "" {
		return nil
	}
	cacheDir := filepath.Join(projectPath, ".cache")
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return fmt.Errorf("create cache dir: %w", err)
	}
	data, _ := json.MarshalIndent(handoff, "", "  ")
	dest := filepath.Join(cacheDir, "autonomy-handoff.json")
	if err := os.WriteFile(dest, data, 0o644); err != nil {
		return fmt.Errorf("write autonomy handoff cache: %w", err)
	}
	return nil
}

var publishActionTokens = []string{
	" deploy", "deploy ", "publish", "release", "go live", "vercel", "npm publish", "docker push", "kubectl apply",
}

func isPublishOrDeployAction(text string) bool {
	lower := " " + strings.ToLower(strings.TrimSpace(text)) + " "
	for _, token := range publishActionTokens {
		if strings.Contains(lower, token) {
			return true
		}
	}
	return false
}

// ValidatePublishIntentForAction blocks deploy/publish actions when explicit
// publish intent evidence is missing.
func ValidatePublishIntentForAction(projectPath, actionText string) error {
	if !isPublishOrDeployAction(actionText) {
		return nil
	}
	if !ResolveAutonomousPolicy(projectPath).RequireExplicitPublishIntent {
		return nil
	}
	profile := loadProjectProfile(projectPath)
	if profile == nil {
		return fmt.Errorf("deploy/publish blocked: no project profile with explicit publish intent evidence is available")
	}
	if profile.ExecutionIntent.PublishIntent != PublishIntentExplicit || len(profile.ExecutionIntent.PublishEvidence) == 0 {
		return fmt.Errorf("deploy/publish blocked: explicit publish intent is required (current=%s, evidence=%d)", profile.ExecutionIntent.PublishIntent, len(profile.ExecutionIntent.PublishEvidence))
	}
	return nil
}

