package ai

import (
	"os"
	"strings"

	"github.com/engine/server/runtimecfg"
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
	RoleDocumenter: true, // mechanical text restatement
	RoleIntaker:    true, // structured JSON extraction
}

// heavyCloudRoles are explicitly NOT routed local even when LOCAL_FIRST is on.
// These benefit from a frontier model's reasoning depth.
//
// The planner and griller live here deliberately: they are the *strategic*
// brain of the whole pipeline. The plan is the ceiling on everything the
// builder, reviewer, and validator can achieve — a step that was never
// planned ambitiously enough can never be built ambitiously. Likewise the
// griller's design-tree walk decides what gets built at all. Downgrading
// either to a 7B local model caps the intelligence of every project Engine
// ever ships, which is exactly the "ok but not great" failure mode. Cheap
// local inference is reserved for genuinely mechanical roles below.
var heavyCloudRoles = map[AgentRole]bool{
	RoleGriller:           true, // design-tree reasoning sets project ambition
	RolePlanner:           true, // the plan is the ceiling on every later phase
	RoleArchitect:         true, // architectural reasoning
	RoleImplementer:       true, // production code
	RoleAutonomousBuilder: true, // tool use + iterative debugging
}

// ── Hybrid routing (cost-optimal team) ──────────────────────────────────────
//
// The hybrid model assigns each role a backend using ONLY providers already
// available — never a paid API key the user has to go get:
//
//   - LEAD / hard-reasoning roles run on the Claude Max SUBSCRIPTION via the
//     claudecode provider (Opus, flat ~$0 marginal cost): grill, plan, PRD,
//     architecture, review. Low call-count, high-value thinking.
//   - WORKER roles prefer a LOCAL model (ollama/llama.cpp) when one is already
//     configured — free, cheap, parallelisable — and otherwise fall back to the
//     same subscription Opus. No worker is ever forced onto a paid API.
//
// With only the subscription, marginal cost is already ~$0; the win this enables
// is SPEED (the orchestrator can run worker roles in parallel and keep their
// sessions warm) at flat cost. A local model, if present, makes the bulk of
// worker calls free and fast. Activate with ENGINE_HYBRID=1 (or runtimecfg).
//
// Routing precedence: explicit per-role env/runtimecfg overrides still win; this
// engages only when nothing more specific was pinned, same as local-first.

// hybridLeadRoles run on the subscription Opus (claudecode) — the lead/manager
// and the reasoning-heavy gates.
var hybridLeadRoles = map[AgentRole]bool{
	RoleGriller:   true,
	RolePlanner:   true,
	RolePRDWriter: true,
	RoleArchitect: true,
	RoleReviewer:  true,
}

// hybridWorkerRoles run cheap + parallel on the Haiku API, with full Engine
// tools + comms.
var hybridWorkerRoles = map[AgentRole]bool{
	RoleAutonomousBuilder: true,
	RoleImplementer:       true,
	RoleScaffolder:        true,
	RoleTester:            true,
	RoleDocumenter:        true,
	RoleModuleIndexer:     true,
	RoleIntaker:           true,
}

// hybridLeadModel is the subscription model the lead roles use (and the worker
// fallback when no local model is available).
const hybridLeadModel = "claude-opus-4-8"

// hybridEnabledFn reports whether hybrid routing should engage. Decoupled from
// os.Getenv so tests can override.
var hybridEnabledFn = func() bool {
	v := strings.TrimSpace(strings.ToLower(os.Getenv("ENGINE_HYBRID")))
	return v == "1" || v == "true" || v == "yes" || v == "on"
}

// ResolveHybridRouting returns (provider, model) for a role under the hybrid
// team model, or ("", "") to defer to the existing routing logic (flag off, or
// a role that is neither lead nor worker, e.g. RoleInteractive/RoleRouter).
func ResolveHybridRouting(projectPath string, role AgentRole) (string, string) {
	enabled := hybridEnabledFn()
	if cfg, err := runtimecfg.Load(projectPath); err == nil {
		if raw := strings.TrimSpace(strings.ToLower(cfg.Hybrid)); raw != "" {
			enabled = raw == "1" || raw == "true" || raw == "yes" || raw == "on"
		}
	}
	if !enabled {
		return "", ""
	}
	if hybridLeadRoles[role] {
		return "claudecode", hybridLeadModel
	}
	if hybridWorkerRoles[role] {
		// Prefer a local model if one is already configured (free + cheap);
		// otherwise fall back to the subscription Opus. Never a paid API.
		if lp, lm := availableLocalModel(projectPath); lm != "" {
			return lp, lm
		}
		return "claudecode", hybridLeadModel
	}
	return "", ""
}

// availableLocalModel returns the (provider, model) of a locally-configured
// model (llama.cpp preferred, then Ollama) from runtimecfg or env, or ("","")
// if none is configured. It does not probe the network — it reports what the
// user has already set up, so hybrid routing can use a free local model when
// present without ever requiring one.
func availableLocalModel(projectPath string) (string, string) {
	cfg, _ := runtimecfg.Load(projectPath)
	if m := strings.TrimSpace(cfg.LlamacppModel); m != "" {
		return "llamacpp", m
	}
	if m := strings.TrimSpace(os.Getenv("ENGINE_LLAMACPP_MODEL")); m != "" {
		return "llamacpp", m
	}
	if m := strings.TrimSpace(cfg.OllamaModel); m != "" {
		return "ollama", m
	}
	if m := strings.TrimSpace(os.Getenv("ENGINE_OLLAMA_MODEL")); m != "" {
		return "ollama", m
	}
	return "", ""
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
func ResolveLocalFirstRouting(projectPath string, role AgentRole) (string, string) {
	enabled := localFirstEnabledFn()
	if cfg, err := runtimecfg.Load(projectPath); err == nil {
		raw := strings.TrimSpace(strings.ToLower(cfg.LocalFirst))
		if raw != "" {
			enabled = raw == "1" || raw == "true" || raw == "yes" || raw == "on"
		}
	}
	if !enabled {
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
	model := ""
	if cfg, err := runtimecfg.Load(projectPath); err == nil {
		model = strings.TrimSpace(cfg.LlamacppModel)
	}
	if strings.TrimSpace(model) == "" {
		model = strings.TrimSpace(os.Getenv("ENGINE_LLAMACPP_MODEL"))
	}
	if strings.TrimSpace(model) == "" {
		// Fall back to Ollama if llama.cpp is not configured
		if cfg, err := runtimecfg.Load(projectPath); err == nil {
			model = strings.TrimSpace(cfg.OllamaModel)
		}
		if strings.TrimSpace(model) == "" {
			model = strings.TrimSpace(os.Getenv("ENGINE_OLLAMA_MODEL"))
		}
		if strings.TrimSpace(model) == "" {
			return "", ""
		}
		return "ollama", model
	}
	return "llamacpp", model
}
