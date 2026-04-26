package ai

import "strings"

// AgentRole identifies the specialized purpose of a Chat call.
// The role determines the system prompt and initial tool set, keeping each
// agent's context window focused on exactly one kind of task.
type AgentRole int

const (
	// RoleInteractive is the default user-facing chat agent.
	// It has broad tool-discovery access and a general helpful prompt.
	RoleInteractive AgentRole = iota

	// RolePlanner produces a concrete numbered plan from a task description.
	// It reads files to understand the codebase but writes nothing.
	RolePlanner

	// RoleScaffolder creates compile-valid file stubs for a single plan step.
	// It writes minimal structure — no logic — so the Implementer has clean targets.
	RoleScaffolder

	// RoleImplementer writes production code for one specified module or function.
	// It receives a file path and a specification; it edits only that file.
	RoleImplementer

	// RoleTester writes tests and iterates until they pass.
	// It receives failing test output or a module path and runs the suite.
	RoleTester

	// RoleReviewer inspects a diff and returns APPROVE or REJECT with findings.
	// It is read-only; it never edits files.
	RoleReviewer

	// RoleDocumenter updates WORKING_BEHAVIORS.md from a code-change summary.
	// It matches existing style and never adds implementation details.
	RoleDocumenter
)

// roleConfig holds the lean system prompt template and tool names pre-granted
// to the role. When tools is nil, bootstrapTools + search_tools discovery is used.
type roleConfig struct {
	prompt string
	tools  []string
}

var roleConfigs = map[AgentRole]roleConfig{
	RoleInteractive: {
		// Interactive chat: broad discovery, no prescribed workflow loop.
		prompt: strings.Join([]string{
			"You are Engine's AI assistant. You have full control over the project.",
			"Project: {{project}}  Branch: {{branch}}",
			"{{context}}",
			"Discover tools with search_tools before using them.",
			"Validate changes by running the code. Fix problems completely.",
		}, "\n"),
		tools: nil, // bootstrapTools + on-demand discovery
	},

	RolePlanner: {
		// Produces a numbered plan. Reads the codebase; writes nothing.
		prompt: strings.Join([]string{
			"You create implementation plans.",
			"Output 4–8 numbered steps. Each step: file to change + what to change + why.",
			"No code. Each step must be achievable in one focused edit session.",
			"Project: {{project}}  Branch: {{branch}}",
			"Respond ONLY with the numbered plan.",
		}, "\n"),
		tools: []string{"read_file", "list_directory", "search_history"},
	},

	RoleScaffolder: {
		// Creates file stubs for one plan step. No business logic.
		prompt: strings.Join([]string{
			"You create compile-valid file stubs.",
			"Given a plan step, create only the files that step requires.",
			"Write stubs only: empty functions, type declarations, no logic.",
			"Project: {{project}}",
			"Report each file you created.",
		}, "\n"),
		tools: []string{"read_file", "list_directory", "write_file", "create_directory"},
	},

	RoleImplementer: {
		// Implements ONE function or module. Touches only the specified file.
		prompt: strings.Join([]string{
			"You implement ONE function or module.",
			"Given: file path + specification. Write production code for the spec.",
			"Follow existing patterns in the codebase. Do not touch files outside the spec.",
			"Project: {{project}}",
			"Report what you changed.",
		}, "\n"),
		tools: []string{"read_file", "list_directory", "write_file"},
	},

	RoleTester: {
		// Writes tests and fixes failures. Runs the suite; iterates until green.
		prompt: strings.Join([]string{
			"You write tests and fix test failures.",
			"Given failing test output or a module to test, write targeted tests.",
			"Run them. Iterate until they pass.",
			"Project: {{project}}",
			"Report final test result (pass/fail counts).",
		}, "\n"),
		tools: []string{"read_file", "list_directory", "write_file", "shell"},
	},

	RoleReviewer: {
		// Reviews a diff. Never edits files.
		prompt: strings.Join([]string{
			"You review code changes.",
			"Given a diff, output APPROVE or REJECT.",
			"If REJECT: list each problem (file, line, reason). One sentence per finding.",
			"Check: correctness, CS3500 design principles, performance, security.",
			"Project: {{project}}",
		}, "\n"),
		tools: []string{"read_file"},
	},

	RoleDocumenter: {
		// Updates WORKING_BEHAVIORS.md only. No implementation details.
		prompt: strings.Join([]string{
			"You update project documentation.",
			"Given code changes, update WORKING_BEHAVIORS.md with new user-visible behaviors.",
			"No implementation details. Match the existing style.",
			"Project: {{project}}",
		}, "\n"),
		tools: []string{"read_file", "list_directory", "write_file"},
	},
}

// buildRoleSystemPrompt returns the lean system prompt for role,
// substituting projectPath, branch, and any optional extra context.
func buildRoleSystemPrompt(role AgentRole, projectPath, branch, extraContext string) string {
	cfg, ok := roleConfigs[role]
	if !ok {
		cfg = roleConfigs[RoleInteractive]
	}
	p := strings.ReplaceAll(cfg.prompt, "{{project}}", projectPath)
	p = strings.ReplaceAll(p, "{{branch}}", branch)
	p = strings.ReplaceAll(p, "{{context}}", extraContext)
	return p
}

// roleBootstrapTools returns the tool names pre-granted to a role.
// Returns nil when the role should use bootstrapTools + search_tools discovery.
func roleBootstrapTools(role AgentRole) []string {
	cfg, ok := roleConfigs[role]
	if !ok {
		return nil
	}
	return cfg.tools
}
