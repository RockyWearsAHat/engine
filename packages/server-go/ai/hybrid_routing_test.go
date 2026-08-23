package ai

import "testing"

func withHybrid(t *testing.T, on bool) {
	t.Helper()
	prev := hybridEnabledFn
	hybridEnabledFn = func() bool { return on }
	t.Cleanup(func() { hybridEnabledFn = prev })
}

func TestResolveHybridRouting_DisabledDefers(t *testing.T) {
	withHybrid(t, false)
	if p, m := ResolveHybridRouting("", RolePlanner); p != "" || m != "" {
		t.Errorf("disabled hybrid should defer, got (%q,%q)", p, m)
	}
}

func TestResolveHybridRouting_LeadRolesGoSubscriptionOpus(t *testing.T) {
	withHybrid(t, true)
	for _, role := range []AgentRole{RoleGriller, RolePlanner, RolePRDWriter, RoleArchitect, RoleReviewer} {
		p, m := ResolveHybridRouting("", role)
		if p != "claudecode" {
			t.Errorf("role %v provider = %q, want claudecode", role, p)
		}
		if m != hybridLeadModel {
			t.Errorf("role %v model = %q, want %q", role, m, hybridLeadModel)
		}
	}
}

func TestResolveHybridRouting_WorkerRolesFallBackToSubscription(t *testing.T) {
	// No local model configured → workers must use the subscription Opus, never
	// a paid API. Clear any local env that could leak in.
	withHybrid(t, true)
	t.Setenv("ENGINE_LLAMACPP_MODEL", "")
	t.Setenv("ENGINE_OLLAMA_MODEL", "")
	for _, role := range []AgentRole{
		RoleAutonomousBuilder, RoleImplementer, RoleScaffolder,
		RoleTester, RoleDocumenter, RoleModuleIndexer, RoleIntaker,
	} {
		p, m := ResolveHybridRouting("", role)
		if p != "claudecode" {
			t.Errorf("role %v provider = %q, want claudecode (no key required)", role, p)
		}
		if m != hybridLeadModel {
			t.Errorf("role %v model = %q, want %q", role, m, hybridLeadModel)
		}
		if p == "anthropic" || p == "openai" {
			t.Errorf("role %v routed to a paid API (%q) — must never happen", role, p)
		}
	}
}

func TestResolveHybridRouting_WorkerRolesUseLocalWhenAvailable(t *testing.T) {
	// A configured local model makes workers free + cheap.
	withHybrid(t, true)
	t.Setenv("ENGINE_LLAMACPP_MODEL", "")
	t.Setenv("ENGINE_OLLAMA_MODEL", "qwen2.5-coder:7b")
	p, m := ResolveHybridRouting("", RoleImplementer)
	if p != "ollama" || m != "qwen2.5-coder:7b" {
		t.Errorf("worker with local model = (%q,%q), want (ollama, qwen2.5-coder:7b)", p, m)
	}
}

func TestResolveHybridRouting_NeutralRolesDefer(t *testing.T) {
	withHybrid(t, true)
	for _, role := range []AgentRole{RoleInteractive, RoleRouter} {
		if p, m := ResolveHybridRouting("", role); p != "" || m != "" {
			t.Errorf("neutral role %v should defer, got (%q,%q)", role, p, m)
		}
	}
}
