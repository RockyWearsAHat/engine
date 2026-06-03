package ai

import (
	"os"
	"strings"
)

// Role routing decisions: which roles should default to a local Ollama model
// (cheap, no $$ per token) vs. which require a cloud provider for quality.
//
// Matt Pocock's framework: "delegate implementation, design interfaces." The
// design moments (architecture, hard implementation) are where cloud spend is
// justified. The mechanical moments (parsing planner output, summarising,
// triaging blockers, asking grill-questions, posting status) are perfectly
// well served by a local 7B-13B model.
//
// Activation: set ENGINE_LOCAL_FIRST=1. When the flag is on, the roles below
// in localFirstRoles are routed to Ollama unless the caller already overrode
// the provider explicitly. Heavy roles keep whatever the caller / env says.
//
// This sits BEFORE the existing ENGINE_PLANNER_MODEL / ENGINE_REVIEWER_MODEL
// overrides — if a user has set role-specific env vars, those still win.

// localFirstRoles is the allowlist of roles that prefer local inference.
// These are the "design-then-delegate" tasks: the design is already in the
// brief or the plan, and the role is doing well-bounded mechanical work where
// a small local model can match a big cloud one at zero marginal cost.
var localFirstRoles = map[AgentRole]bool{
	RoleGriller:    true, // ask + self-answer is templated work
	RolePlanner:    true, // output a numbered plan in a fixed format
	RoleDocumenter: true, // mechanical text restatement
	RoleIntaker:    true, // structured JSON extraction
}

// heavyCloudRoles are explicitly NOT routed local even when LOCAL_FIRST is on.
// These benefit from a frontier model's reasoning depth.
var heavyCloudRoles = map[AgentRole]bool{
	RoleArchitect:         true, // architectural reasoning
	RoleImplementer:       true, // production code
	RoleAutonomousBuilder: true, // tool use + iterative debugging
}

// localFirstEnabled reports whether the local-first router should engage.
// Decoupled from os.Getenv so tests can override.
var localFirstEnabledFn = func() bool {
	v := strings.TrimSpace(strings.ToLower(os.Getenv("ENGINE_LOCAL_FIRST")))
	return v == "1" || v == "true" || v == "yes" || v == "on"
}

// ResolveLocalFirstRouting returns (provider, model) when the role should be
// routed to a local model, or ("", "") to defer to the existing routing logic.
// Falls back to deferred when LOCAL_FIRST is off, the role is heavy, or no
// local model is detectable.
func ResolveLocalFirstRouting(role AgentRole) (string, string) {
	if !localFirstEnabledFn() {
		return "", ""
	}
	if heavyCloudRoles[role] {
		return "", ""
	}
	if !localFirstRoles[role] {
		// Unlisted roles defer to existing logic. Conservative — only route
		// what's explicitly allowlisted.
		return "", ""
	}
	// Choose a local llama.cpp model for optimal speed. If a role-specific override exists in env
	// it's respected by the existing pathway, so we only kick in when nothing
	// stronger has been requested.
	model := strings.TrimSpace(os.Getenv("ENGINE_LLAMACPP_MODEL"))
	if strings.TrimSpace(model) == "" {
		// Fall back to Ollama if llama.cpp is not configured
		model = strings.TrimSpace(os.Getenv("ENGINE_OLLAMA_MODEL"))
		if strings.TrimSpace(model) == "" {
			return "", ""
		}
		return "ollama", model
	}
	return "llamacpp", model
}
