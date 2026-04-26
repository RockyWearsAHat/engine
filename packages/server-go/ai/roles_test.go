package ai

import (
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
}

func TestBuildRoleSystemPrompt_Documenter_ReferencesWorkingBehaviors(t *testing.T) {
	p := buildRoleSystemPrompt(RoleDocumenter, "/proj", "", "")
	if !strings.Contains(p, "WORKING_BEHAVIORS") {
		t.Errorf("expected WORKING_BEHAVIORS reference in documenter prompt, got %q", p)
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
	has := func(name string) bool {
		for _, n := range tools {
			if n == name {
				return true
			}
		}
		return false
	}
	if !has("read_file") {
		t.Errorf("expected read_file in planner tools, got %v", tools)
	}
	if !has("list_directory") {
		t.Errorf("expected list_directory in planner tools, got %v", tools)
	}
}

func TestRoleBootstrapTools_Tester_IncludesShell(t *testing.T) {
	tools := roleBootstrapTools(RoleTester)
	found := false
	for _, n := range tools {
		if n == "shell" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected shell in tester tools, got %v", tools)
	}
}

func TestRoleBootstrapTools_Reviewer_ReadOnly(t *testing.T) {
	tools := roleBootstrapTools(RoleReviewer)
	for _, n := range tools {
		if n == "write_file" || n == "shell" {
			t.Errorf("reviewer should not have write/shell access, found %q", n)
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
		RoleTester, RoleReviewer, RoleDocumenter,
	}
	for _, r := range roles {
		tools := roleBootstrapTools(r)
		if len(tools) == 0 {
			t.Errorf("role %d has no pre-granted tools", r)
			continue
		}
		found := false
		for _, n := range tools {
			if n == "read_file" {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("role %d missing read_file in pre-granted tools: %v", r, tools)
		}
	}
}
