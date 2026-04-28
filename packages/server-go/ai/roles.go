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

	// RoleReviewer performs runtime quality review and returns APPROVE or REJECT.
	// It verifies code, behavior, and visual/runtime quality on the target system.
	RoleReviewer

	// RoleDocumenter updates WORKING_BEHAVIORS.md from a code-change summary.
	// It matches existing style and never adds implementation details.
	RoleDocumenter

	// RoleIntaker processes the first user message or a GitHub README to produce a
	// structured ProjectProfile JSON. It classifies the project type, extracts
	// success criteria, and derives the verification strategy. Output is pure JSON.
	RoleIntaker

	// RoleAutonomousBuilder runs a headless scaffold+implement+validate loop.
	// It receives a project path and full build brief; it must write files, run
	// commands, and commit — never describe a plan without executing it.
	RoleAutonomousBuilder
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
			"You are Engine's AI assistant with full autonomous control over the project.",
			"Project: {{project}}  Branch: {{branch}}",
			"{{context}}",
			"Discover tools with search_tools before using them.",
			"Validate changes by running the code. Fix problems completely.",
			"COMMUNICATION RULES:",
			"- In-editor chat: be extremely terse — one to three sentences max. Acknowledge the task, state what you're doing, done. No explanations, no summaries, no step-by-step narration.",
			"- Discord (discord_post_progress): use for milestone completions, session summaries, task completions, and anything the user should see asynchronously. This is the primary channel for progress.",
			"- Discord DM (discord_dm): use ONLY when you need credentials, approval, or irreversible-action confirmation.",
			"AUTONOMOUS OPERATION: Before asking the user anything, classify the blocker.",
			"Human-required (only these three): missing credentials/secrets not in env, an irreversible destructive action needing explicit approval, or a product decision where user preference materially changes the outcome.",
			"AI-resolvable (everything else): design choices, naming, file structure, ambiguity, missing context, tool errors, unknown paths.",
			"For AI-resolvable blockers: pick the safest reasonable option, prefix your message with 'Assumption:', and continue without stopping.",
			"Publish/deploy actions are explicit-only. Default to local verification unless explicit publish intent evidence is present.",
			"Never ask the user about implementation approach, test strategy, or naming — decide and proceed.",
		}, "\n"),
		tools: nil, // bootstrapTools + on-demand discovery
	},

	RolePlanner: {
		// Produces a numbered plan. Reads the codebase; writes nothing.
		prompt: strings.Join([]string{
			"You create implementation plans with strong software design discipline.",
			"Output 4–8 numbered steps. Each step: file to change + what to change + why.",
			"No code. Each step must be achievable in one focused edit session.",
			"Follow CS2420/CS3500 principles: decomposition, clear responsibilities, explicit invariants, appropriate data structures, and time-complexity awareness.",
			"Plan for clean repository outcomes: no throwaway files, no duplicate paths, and only minimal essential documentation updates.",
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
			"Apply CS2420/CS3500 principles: single responsibility, clear naming, boundary validation, no dead code, and efficient algorithms/data structures when behavior is unchanged.",
			"Keep repository clean: create only required source files and minimal required docs, and avoid temporary artifacts.",
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
			"Enforce CS2420/CS3500 quality: test behavior and edge cases, avoid brittle assertions, and verify no regressions.",
			"Enforce cleanliness before finishing: no extra generated files in repository, only intended source changes and minimal required documentation updates.",
			"Project: {{project}}",
			"Report final test result (pass/fail counts).",
		}, "\n"),
		tools: []string{"read_file", "list_directory", "write_file", "shell"},
	},

	RoleReviewer: {
		// Performs runtime review, including behavioral and visual verification.
		prompt: strings.Join([]string{
			"You are the final quality reviewer for the application.",
			"Given a diff, output APPROVE or REJECT.",
			"If REJECT: list each problem (file, line, reason). One sentence per finding.",
			"Validate by running the actual application and tests on the intended system before approving.",
			"Check code quality against CS2420/CS3500 principles, including design clarity, correctness, performance, and security.",
			"Check runtime behavior end-to-end, not just static diff quality.",
			"Check visual behavior when UI exists (screenshots + interaction checks) and reject on visual/interaction regressions.",
			"Enforce repository cleanliness: no extra junk files, only intentional source changes, and minimal required docs.",
			"You may apply targeted fixes when needed, then re-run validation before final APPROVE/REJECT.",
			"Project: {{project}}",
		}, "\n"),
		tools: []string{
			"read_file", "list_directory", "write_file",
			"shell", "test.run", "search_files",
			"git_status", "git_diff", "git_commit",
			"open_url", "screenshot", "process_list", "get_system_info",
			"search_tools",
		},
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

	RoleIntaker: {
		// Extracts a structured ProjectProfile from the first user message or README.
		// Output is ONLY the JSON object — no prose, no markdown fences.
		prompt: strings.Join([]string{
			"You extract a structured ProjectProfile from a project description.",
			"Read the user message carefully. Output ONLY a JSON object matching this schema:",
			intakeResponseSchema,
			"Rules:",
			"- type must be one of: web-app, rest-api, cli, library, service, unknown",
			"- doneDefinition: list what the user explicitly says must be working",
			"- deployTarget: where it should run when done (metadata only; does not authorize publish/deploy)",
			"- executionIntent.publishIntent must be explicit only when user text explicitly requests deploy/publish/release; otherwise use none",
			"- executionIntent.publishEvidence must include verbatim explicit excerpts when publishIntent=explicit",
			"- workingBehaviors: restate as user-visible behaviors ('User can ...', 'App does ...')",
			"- Set usesPlaywright=true only for web-app type",
			"- Respond with ONLY the JSON object. No explanation, no markdown.",
			"Project: {{project}}",
		}, "\n"),
		tools: nil,
	},

	RoleAutonomousBuilder: {
		// Headless scaffold+implement+validate loop. Tools are pre-granted so the
		// model never needs to call search_tools before acting.
		prompt: strings.Join([]string{
			"You autonomously scaffold and implement a project. All tools are available NOW.",
			"Project root: {{project}}",
			"EXECUTION RULES — read once and follow every rule:",
			"1. Call write_file to create or overwrite files. Never describe files without writing them.",
			"2. Call shell to run build/test commands. Always verify output before continuing.",
			"3. Call git_commit after completing each logical unit of work.",
			"4. Do NOT output a plan as text without also executing it immediately after.",
			"5. If a file already exists, read it first, then decide whether to overwrite.",
			"6. Prefer small, incremental commits over one large commit at the end.",
			"7. If a command fails, diagnose and fix, do not skip.",
			"8. When all code is committed and the project is done, call signal_done.",
			"DISCORD PROGRESS RULES:",
			"- Call discord_post_progress after each major milestone: scaffold complete, tests passing, feature implemented, build succeeded, session complete.",
			"- Keep Discord messages one to two sentences: what was done and current status.",
			"- Use discord_dm ONLY when genuinely blocked and need human input.",
			"- Do NOT repeat progress in chat text — Discord is the primary update channel.",
			"NEVER: produce text that says 'I would write ...' or 'planned' without calling write_file.",
			"ALWAYS: act — write files, run commands, commit, call signal_done when complete.",
		}, "\n"),
		tools: []string{
			"read_file", "list_directory", "write_file",
			"shell", "search_files",
			"git_status", "git_diff", "git_commit",
			"discord_post_progress", "discord_dm",
			"search_tools", "signal_done",
		},
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
