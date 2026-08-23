package ai

import (
	"testing"
)

func TestResolveLocalFirstRouting_DisabledReturnsEmpty(t *testing.T) {
	prev := localFirstEnabledFn
	localFirstEnabledFn = func() bool { return false }
	t.Cleanup(func() { localFirstEnabledFn = prev })

	if provider, model := ResolveLocalFirstRouting("", RolePlanner); provider != "" || model != "" {
		t.Errorf("disabled router should return empty, got (%q, %q)", provider, model)
	}
}

func TestResolveLocalFirstRouting_LightRoleRoutesLocal(t *testing.T) {
	prev := localFirstEnabledFn
	localFirstEnabledFn = func() bool { return true }
	t.Cleanup(func() { localFirstEnabledFn = prev })
	t.Setenv("ENGINE_OLLAMA_MODEL", "qwen2.5:7b")

	provider, model := ResolveLocalFirstRouting("", RoleIntaker)
	if provider != "ollama" {
		t.Errorf("provider = %q, want ollama", provider)
	}
	if model != "qwen2.5:7b" {
		t.Errorf("model = %q, want qwen2.5:7b", model)
	}
}

func TestResolveLocalFirstRouting_LightRoleRoutesLlamaCpp(t *testing.T) {
	prev := localFirstEnabledFn
	localFirstEnabledFn = func() bool { return true }
	t.Cleanup(func() { localFirstEnabledFn = prev })
	t.Setenv("ENGINE_OLLAMA_MODEL", "")
	t.Setenv("ENGINE_LLAMACPP_MODEL", "llama-cpp-small")

	provider, model := ResolveLocalFirstRouting("", RoleIntaker)
	if provider != "llamacpp" {
		t.Fatalf("provider = %q, want llamacpp", provider)
	}
	if model != "llama-cpp-small" {
		t.Fatalf("model = %q, want llama-cpp-small", model)
	}
}

func TestResolveLocalFirstRouting_HeavyRoleStaysCloud(t *testing.T) {
	prev := localFirstEnabledFn
	localFirstEnabledFn = func() bool { return true }
	t.Cleanup(func() { localFirstEnabledFn = prev })

	if provider, model := ResolveLocalFirstRouting("", RoleArchitect); provider != "" || model != "" {
		t.Errorf("heavy role should not route local, got (%q, %q)", provider, model)
	}
	if provider, _ := ResolveLocalFirstRouting("", RoleAutonomousBuilder); provider != "" {
		t.Errorf("autonomous builder should not route local, got %q", provider)
	}
	if provider, _ := ResolveLocalFirstRouting("", RoleImplementer); provider != "" {
		t.Errorf("implementer should not route local, got %q", provider)
	}
}

func TestResolveLocalFirstRouting_UnknownRoleDefers(t *testing.T) {
	prev := localFirstEnabledFn
	localFirstEnabledFn = func() bool { return true }
	t.Cleanup(func() { localFirstEnabledFn = prev })

	if provider, _ := ResolveLocalFirstRouting("", RoleInteractive); provider != "" {
		t.Errorf("interactive (not in allowlist) should defer, got %q", provider)
	}
}

func TestResolveLocalFirstRouting_NoModelConfiguredDefers(t *testing.T) {
	prevEnabled := localFirstEnabledFn
	localFirstEnabledFn = func() bool { return true }
	t.Cleanup(func() { localFirstEnabledFn = prevEnabled })

	t.Setenv("ENGINE_OLLAMA_MODEL", "")

	provider, model := ResolveLocalFirstRouting("", RoleIntaker)
	if provider != "" {
		t.Fatalf("provider = %q, want empty", provider)
	}
	if model != "" {
		t.Fatalf("model = %q, want empty", model)
	}
}
