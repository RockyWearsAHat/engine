package ai

// AgentRole identifies the specialist role of a subagent.
type AgentRole string

const (
	RoleImpl   AgentRole = "impl"
	RoleReview AgentRole = "review"
	RoleTest   AgentRole = "test"
	RoleDebug  AgentRole = "debug"
	RoleSearch AgentRole = "search"
)

// agentProfile holds the system prompt and allowed tool names for a specialist role.
type agentProfile struct {
	SystemPrompt string
	AllowedTools []string // empty means all tools allowed
}

// agentProfiles maps each AgentRole to its profile.
var agentProfiles = map[AgentRole]agentProfile{
	RoleImpl: {
		SystemPrompt: `You are an implementation agent inside Engine's autonomous development system.
Your ONLY job: write and edit code to complete the given task exactly as specified.
Do not plan. Do not explain your approach. Do not summarize. Just execute.
When done, output only: a one-line summary of what you changed and which files were modified.`,
		AllowedTools: []string{"read_file", "write_file", "list_directory", "shell", "search_tools"},
	},
	RoleReview: {
		SystemPrompt: `You are a code review agent inside Engine's autonomous development system.
Your ONLY job: read the specified files and return a JSON array of issues found.
Format: [{"file":"path","line":N,"severity":"error|warn|info","description":"..."}]
Do not fix anything. Critique only. If no issues, return [].`,
		AllowedTools: []string{"read_file", "list_directory", "search_tools"},
	},
	RoleTest: {
		SystemPrompt: `You are a test execution agent inside Engine's autonomous development system.
Your ONLY job: run the test suite for the specified scope and report results.
Return JSON: {"passed":N,"failed":N,"failures":[{"test":"name","error":"message"}]}
Run tests, read the output, return structured results. Do not fix anything.`,
		AllowedTools: []string{"shell", "read_file", "search_tools"},
	},
	RoleDebug: {
		SystemPrompt: `You are a debugging agent inside Engine's autonomous development system.
Your ONLY job: given a failing test or error, trace the root cause and produce a minimal fix description.
Return JSON: {"rootCause":"...","fix":{"file":"path","description":"exactly what to change"}}
Read code, trace the failure, identify the minimal correct fix. Do not apply it yourself.`,
		AllowedTools: []string{"read_file", "shell", "list_directory", "search_tools"},
	},
	RoleSearch: {
		SystemPrompt: `You are a codebase search agent inside Engine's autonomous development system.
Your ONLY job: find relevant files, functions, and patterns for the given query.
Return JSON: {"results":[{"file":"path","symbol":"name","relevance":"why this matters"}]}
Search broadly and return the most relevant locations.`,
		AllowedTools: []string{"read_file", "list_directory", "shell", "search_tools"},
	},
}

// profileForRole returns the agentProfile for the given role, defaulting to impl.
func profileForRole(role AgentRole) agentProfile {
	if p, ok := agentProfiles[role]; ok {
		return p
	}
	return agentProfiles[RoleImpl]
}

// isToolAllowed returns true if the tool is allowed for this profile.
// Empty AllowedTools means all tools are allowed.
func (p agentProfile) isToolAllowed(toolName string) bool {
	if len(p.AllowedTools) == 0 {
		return true
	}
	for _, t := range p.AllowedTools {
		if t == toolName {
			return true
		}
	}
	return false
}
