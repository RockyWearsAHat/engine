package ai

import (
	"slices"
	"strings"
	"testing"
)

// ── AgentRole constants ───────────────────────────────────────────────────────

func TestAgentRoleConstants_Distinct(t *testing.T) {
	roles := []AgentRole{
		RoleInteractive,
		RolePlanner,
		RoleScaffolder,
		RoleImplementer,
		RoleTester,
		RoleReviewer,
		RoleDocumenter,
		RoleAutonomousBuilder,
	}
	seen := map[AgentRole]bool{}
	for _, r := range roles {
		if seen[r] {
			t.Fatalf("duplicate AgentRole value: %d", r)
		}
		seen[r] = true
	}
}

// ── buildRoleSystemPrompt ─────────────────────────────────────────────────────

func TestBuildRoleSystemPrompt_Interactive_ContainsProjectAndBranch(t *testing.T) {
	p := buildRoleSystemPrompt(RoleInteractive, "/my/project", "main", "")
	if !strings.Contains(p, "/my/project") {
		t.Errorf("expected project path in prompt, got %q", p)
	}
	if !strings.Contains(p, "main") {
		t.Errorf("expected branch in prompt, got %q", p)
	}
}

func TestBuildRoleSystemPrompt_Interactive_InjectsExtraContext(t *testing.T) {
	p := buildRoleSystemPrompt(RoleInteractive, "/proj", "dev", "recent file: main.go")
	if !strings.Contains(p, "recent file: main.go") {
		t.Errorf("expected extra context injected, got %q", p)
	}
}

func TestBuildRoleSystemPrompt_Interactive_EmptyContextNotLiteral(t *testing.T) {
	p := buildRoleSystemPrompt(RoleInteractive, "/proj", "dev", "")
	// The {{context}} placeholder should be replaced with "" — not appear literally.
	if strings.Contains(p, "{{context}}") {
		t.Errorf("expected {{context}} replaced, got %q", p)
	}
}

func TestBuildRoleSystemPrompt_Interactive_ContainsAutonomousBlockerRule(t *testing.T) {
	p := buildRoleSystemPrompt(RoleInteractive, "/proj", "main", "")
	if !strings.Contains(p, "workspace assistant") {
		t.Errorf("expected workspace assistant identity in interactive prompt, got %q", p)
	}
	if !strings.Contains(p, "Do not invent workspace facts") {
		t.Errorf("expected factuality guard in interactive prompt, got %q", p)
	}
}

func TestBuildRoleSystemPrompt_Planner_NoBranchPlaceholderLeft(t *testing.T) {
	p := buildRoleSystemPrompt(RolePlanner, "/proj", "feature/x", "")
	if strings.Contains(p, "{{") {
		t.Errorf("expected no leftover placeholders, got %q", p)
	}
	if !strings.Contains(p, "numbered") {
		t.Errorf("expected planner directive in prompt, got %q", p)
	}
}

func TestBuildRoleSystemPrompt_Scaffolder_ContainsStubDirective(t *testing.T) {
	p := buildRoleSystemPrompt(RoleScaffolder, "/proj", "", "")
	if !strings.Contains(p, "stub") {
		t.Errorf("expected stub directive in scaffolder prompt, got %q", p)
	}
}

func TestBuildRoleSystemPrompt_Implementer_ContainsSpecDirective(t *testing.T) {
	p := buildRoleSystemPrompt(RoleImplementer, "/proj", "", "")
	if !strings.Contains(p, "specification") {
		t.Errorf("expected spec directive in implementer prompt, got %q", p)
	}
}

func TestBuildRoleSystemPrompt_Tester_ContainsIterateDirective(t *testing.T) {
	p := buildRoleSystemPrompt(RoleTester, "/proj", "", "")
	if !strings.Contains(p, "pass") {
		t.Errorf("expected iterate-until-pass directive in tester prompt, got %q", p)
	}
}

func TestBuildRoleSystemPrompt_Reviewer_ContainsApproveReject(t *testing.T) {
	p := buildRoleSystemPrompt(RoleReviewer, "/proj", "", "")
	if !strings.Contains(p, "APPROVE") || !strings.Contains(p, "REJECT") {
		t.Errorf("expected APPROVE/REJECT in reviewer prompt, got %q", p)
	}
	if !strings.Contains(p, "intended system") {
		t.Errorf("expected runtime validation directive in reviewer prompt, got %q", p)
	}
}

func TestBuildRoleSystemPrompt_Documenter_ReferencesDx(t *testing.T) {
	p := buildRoleSystemPrompt(RoleDocumenter, "/proj", "", "")
	if !strings.Contains(p, "dx") || !strings.Contains(p, "index.dx") {
		t.Errorf("expected index.dx reference in documenter prompt, got %q", p)
	}
	if !strings.Contains(p, "rewrite") {
		t.Errorf("expected rewrite directive in documenter prompt, got %q", p)
	}
}

func TestBuildRoleSystemPrompt_UnknownRole_FallsBackToInteractive(t *testing.T) {
	unknown := AgentRole(999)
	p := buildRoleSystemPrompt(unknown, "/proj", "main", "ctx")
	interactive := buildRoleSystemPrompt(RoleInteractive, "/proj", "main", "ctx")
	if p != interactive {
		t.Errorf("expected unknown role to fall back to interactive prompt\ngot:      %q\nexpected: %q", p, interactive)
	}
}

// ── roleBootstrapTools ────────────────────────────────────────────────────────

func TestRoleBootstrapTools_Interactive_ReturnsNil(t *testing.T) {
	if roleBootstrapTools(RoleInteractive) != nil {
		t.Error("expected nil for RoleInteractive (uses discovery)")
	}
}

func TestRoleBootstrapTools_Planner_IncludesReadAndHistory(t *testing.T) {
	tools := roleBootstrapTools(RolePlanner)
	if tools == nil {
		t.Fatal("expected non-nil tool list for RolePlanner")
	}
	if !slices.Contains(tools, "read_file") {
		t.Errorf("expected read_file in planner tools, got %v", tools)
	}
	if !slices.Contains(tools, "list_directory") {
		t.Errorf("expected list_directory in planner tools, got %v", tools)
	}
}

func TestRoleBootstrapTools_Tester_IncludesShell(t *testing.T) {
	tools := roleBootstrapTools(RoleTester)
	if !slices.Contains(tools, "shell") {
		t.Errorf("expected shell in tester tools, got %v", tools)
	}
}

func TestRoleBootstrapTools_Reviewer_RuntimeCapable(t *testing.T) {
	tools := roleBootstrapTools(RoleReviewer)
	if tools == nil {
		t.Fatal("expected non-nil tool list for RoleReviewer")
	}
	for _, required := range []string{"read_file", "write_file", "shell", "test.run", "screenshot", "git_diff"} {
		if !slices.Contains(tools, required) {
			t.Errorf("RoleReviewer missing required tool %q, got %v", required, tools)
		}
	}
}

func TestRoleBootstrapTools_UnknownRole_ReturnsNil(t *testing.T) {
	if roleBootstrapTools(AgentRole(999)) != nil {
		t.Error("expected nil for unknown role")
	}
}

func TestRoleBootstrapTools_AllNonInteractiveRoles_HaveAtLeastOneReadTool(t *testing.T) {
	roles := []AgentRole{
		RolePlanner, RoleScaffolder, RoleImplementer,
		RoleTester, RoleReviewer, RoleDocumenter, RoleAutonomousBuilder,
	}
	for _, r := range roles {
		tools := roleBootstrapTools(r)
		if len(tools) == 0 {
			t.Errorf("role %d has no pre-granted tools", r)
			continue
		}
		if !slices.Contains(tools, "read_file") {
			t.Errorf("role %d missing read_file in pre-granted tools: %v", r, tools)
		}
	}
}

func TestRoleBootstrapTools_AutonomousBuilder_HasWriteAndShellAndGit(t *testing.T) {
	tools := roleBootstrapTools(RoleAutonomousBuilder)
	if tools == nil {
		t.Fatal("expected non-nil tool list for RoleAutonomousBuilder")
	}
	for _, required := range []string{"write_file", "shell", "git_commit", "read_file", "list_directory"} {
		if !slices.Contains(tools, required) {
			t.Errorf("RoleAutonomousBuilder missing required tool %q, got %v", required, tools)
		}
	}
}

func TestBuildRoleSystemPrompt_AutonomousBuilder_ContainsExecutionRules(t *testing.T) {
	p := buildRoleSystemPrompt(RoleAutonomousBuilder, "/proj", "main", "")
	if !strings.Contains(p, "write_file") {
		t.Errorf("expected write_file directive in autonomous builder prompt, got %q", p)
	}
	if !strings.Contains(p, "git_commit") {
		t.Errorf("expected git_commit directive in autonomous builder prompt, got %q", p)
	}
	if !strings.Contains(p, "/proj") {
		t.Errorf("expected project path in autonomous builder prompt, got %q", p)
	}
}

func TestBuildRoleSystemPrompt_Interactive_ContainsTerseRule(t *testing.T) {
	p := buildRoleSystemPrompt(RoleInteractive, "/proj", "main", "")
	if !strings.Contains(p, "Keep replies short and direct") {
		t.Errorf("expected concise chat rule in interactive prompt, got %q", p)
	}
	if !strings.Contains(p, "search_tools") {
		t.Errorf("expected search_tools mention in interactive prompt, got %q", p)
	}
}

func TestBuildRoleSystemPrompt_Interactive_ContainsLeadDelegationRule(t *testing.T) {
	p := buildRoleSystemPrompt(RoleInteractive, "/proj", "main", "")
	for _, required := range []string{"workspace assistant", "search_tools", "smallest relevant check"} {
		if !strings.Contains(p, required) {
			t.Errorf("expected interactive prompt to contain %q, got %q", required, p)
		}
	}
}

func TestRoleBootstrapTools_AutonomousBuilder_HasDiscordProgressAndDM(t *testing.T) {
	tools := roleBootstrapTools(RoleAutonomousBuilder)
	if tools == nil {
		t.Fatal("expected non-nil tool list for RoleAutonomousBuilder")
	}
	if !slices.Contains(tools, "discord_post_progress") {
		t.Errorf("RoleAutonomousBuilder missing discord_post_progress, got %v", tools)
	}
	if !slices.Contains(tools, "discord_dm") {
		t.Errorf("RoleAutonomousBuilder missing discord_dm, got %v", tools)
	}
}

func TestBuildRoleSystemPrompt_AutonomousBuilder_ContainsDiscordProgressRule(t *testing.T) {
	p := buildRoleSystemPrompt(RoleAutonomousBuilder, "/proj", "main", "")
	if !strings.Contains(p, "discord_post_progress") {
		t.Errorf("expected discord_post_progress rule in autonomous builder prompt, got %q", p)
	}
}

func TestRoleBootstrapTools_AutonomousBuilder_HasAgentCommunication(t *testing.T) {
	tools := roleBootstrapTools(RoleAutonomousBuilder)
	for _, required := range []string{"agent_list", "agent_send", "agent_inbox", "agent_receive", "agent_await"} {
		if !slices.Contains(tools, required) {
			t.Errorf("RoleAutonomousBuilder missing %q, got %v", required, tools)
		}
	}
}

func TestBuildRoleSystemPrompt_AutonomousBuilder_ContainsTeamCommunicationRules(t *testing.T) {
	p := buildRoleSystemPrompt(RoleAutonomousBuilder, "/proj", "main", "")
	for _, required := range []string{"TEAM COMMUNICATION RULES", "agent_list", "agent_receive", "clean context window"} {
		if !strings.Contains(p, required) {
			t.Errorf("expected autonomous builder prompt to contain %q, got %q", required, p)
		}
	}
}

func TestBuildRoleSystemPrompt_AutonomousBuilder_RequiresObserveAfterWrite(t *testing.T) {
	p := buildRoleSystemPrompt(RoleAutonomousBuilder, "/proj", "main", "")
	for _, required := range []string{
		"After every write_file",
		"inspect the actual output",
		"Development means changing code AND seeing the result",
		"If verification fails, fix it instead of reporting completion",
	} {
		if !strings.Contains(p, required) {
			t.Errorf("expected autonomous builder prompt to contain %q, got %q", required, p)
		}
	}
}

func TestBuildRoleSystemPrompt_AutonomousBuilder_BansWorkspaceProjectMirrors(t *testing.T) {
	p := buildRoleSystemPrompt(RoleAutonomousBuilder, "/proj", "main", "")
	for _, required := range []string{
		"<engine>/.engine/projects/...",
		"packages/server-go/.engine/projects/...",
		"never create symlinks back into the Engine workspace",
	} {
		if !strings.Contains(p, required) {
			t.Errorf("expected autonomous builder prompt to contain %q, got %q", required, p)
		}
	}
}
