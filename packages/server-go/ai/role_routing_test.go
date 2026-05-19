package ai

import (
	"testing"
)

func TestResolveLocalFirstRouting_DisabledReturnsEmpty(t *testing.T) {
	prev := localFirstEnabledFn
	localFirstEnabledFn = func() bool { return false }
	t.Cleanup(func() { localFirstEnabledFn = prev })

	if provider, model := ResolveLocalFirstRouting(RolePlanner); provider != "" || model != "" {
		t.Errorf("disabled router should return empty, got (%q, %q)", provider, model)
	}
}

func TestResolveLocalFirstRouting_LightRoleRoutesLocal(t *testing.T) {
	prev := localFirstEnabledFn
	localFirstEnabledFn = func() bool { return true }
	t.Cleanup(func() { localFirstEnabledFn = prev })

	provider, model := ResolveLocalFirstRouting(RolePlanner)
	if provider != "ollama" {
		t.Errorf("provider = %q, want ollama", provider)
	}
	if model == "" {
		t.Error("model should be set")
	}
}

func TestResolveLocalFirstRouting_HeavyRoleStaysCloud(t *testing.T) {
	prev := localFirstEnabledFn
	localFirstEnabledFn = func() bool { return true }
	t.Cleanup(func() { localFirstEnabledFn = prev })

	if provider, model := ResolveLocalFirstRouting(RoleArchitect); provider != "" || model != "" {
		t.Errorf("heavy role should not route local, got (%q, %q)", provider, model)
	}
	if provider, _ := ResolveLocalFirstRouting(RoleAutonomousBuilder); provider != "" {
		t.Errorf("autonomous builder should not route local, got %q", provider)
	}
	if provider, _ := ResolveLocalFirstRouting(RoleImplementer); provider != "" {
		t.Errorf("implementer should not route local, got %q", provider)
	}
}

func TestResolveLocalFirstRouting_UnknownRoleDefers(t *testing.T) {
	prev := localFirstEnabledFn
	localFirstEnabledFn = func() bool { return true }
	t.Cleanup(func() { localFirstEnabledFn = prev })

	if provider, _ := ResolveLocalFirstRouting(RoleInteractive); provider != "" {
		t.Errorf("interactive (not in allowlist) should defer, got %q", provider)
	}
}

func TestResolveLocalFirstRouting_DefaultModelFallback(t *testing.T) {
	prevEnabled := localFirstEnabledFn
	localFirstEnabledFn = func() bool { return true }
	t.Cleanup(func() { localFirstEnabledFn = prevEnabled })

	t.Setenv("ENGINE_OLLAMA_MODEL", "")
	t.Setenv("OLLAMA_BASE_URL", "http://127.0.0.1:1")

	provider, model := ResolveLocalFirstRouting(RolePlanner)
	if provider != "ollama" {
		t.Fatalf("provider = %q, want ollama", provider)
	}
	if model != defaultOllamaModel {
		t.Fatalf("model = %q, want default %q", model, defaultOllamaModel)
	}
}
