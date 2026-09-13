package ai

import (
	"os"
	"strings"
	"testing"
)

func TestClampModelTier(t *testing.T) {
	tests := []struct {
		name             string
		requested        string
		ceiling          string
		expectedContains string
	}{
		// Haiku ceiling (default)
		{
			name:             "haiku requested, haiku ceiling",
			requested:        "haiku",
			ceiling:          "",
			expectedContains: "haiku",
		},
		{
			name:             "sonnet requested, haiku ceiling (default)",
			requested:        "sonnet",
			ceiling:          "",
			expectedContains: "haiku",
		},
		{
			name:             "opus requested, haiku ceiling",
			requested:        "opus",
			ceiling:          "haiku",
			expectedContains: "haiku",
		},
		{
			name:             "full opus ID requested, haiku ceiling",
			requested:        "claude-opus-4-8",
			ceiling:          "haiku",
			expectedContains: "haiku",
		},
		// Sonnet ceiling
		{
			name:             "haiku requested, sonnet ceiling",
			requested:        "haiku",
			ceiling:          "sonnet",
			expectedContains: "haiku",
		},
		{
			name:             "sonnet requested, sonnet ceiling",
			requested:        "sonnet",
			ceiling:          "sonnet",
			expectedContains: "sonnet",
		},
		{
			name:             "opus requested, sonnet ceiling",
			requested:        "opus",
			ceiling:          "sonnet",
			expectedContains: "sonnet",
		},
		{
			name:             "full sonnet ID requested, sonnet ceiling",
			requested:        "claude-sonnet-4-20250514",
			ceiling:          "sonnet",
			expectedContains: "sonnet",
		},
		// Edge cases
		{
			name:             "empty requested returns empty",
			requested:        "",
			ceiling:          "haiku",
			expectedContains: "",
		},
		{
			name:             "whitespace requested returns unchanged",
			requested:        "  ",
			ceiling:          "haiku",
			expectedContains: "  ",
		},
		{
			name:             "unknown ceiling defaults to haiku",
			requested:        "sonnet",
			ceiling:          "unknown",
			expectedContains: "haiku",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.ceiling == "" {
				os.Unsetenv("ENGINE_MODEL_CEILING")
			} else {
				os.Setenv("ENGINE_MODEL_CEILING", tt.ceiling)
			}
			defer os.Unsetenv("ENGINE_MODEL_CEILING")

			result := ClampModelTier(tt.requested)
			if !strings.Contains(strings.ToLower(result), strings.ToLower(tt.expectedContains)) {
				t.Errorf("ClampModelTier(%q) with ceiling=%q returned %q, expected to contain %q",
					tt.requested, tt.ceiling, result, tt.expectedContains)
			}
		})
	}
}

func TestClampModelTierReturnsUnchangedWhenAllowed(t *testing.T) {
	os.Setenv("ENGINE_MODEL_CEILING", "opus")
	defer os.Unsetenv("ENGINE_MODEL_CEILING")

	tests := []struct {
		requested string
	}{
		{"haiku"},
		{"sonnet"},
		{"claude-haiku-4-5-20251001"},
		{"claude-sonnet-4-20250514"},
	}

	for _, tt := range tests {
		result := ClampModelTier(tt.requested)
		if result != tt.requested {
			t.Errorf("ClampModelTier(%q) with opus ceiling should return unchanged, got %q",
				tt.requested, result)
		}
	}
}

func TestClampModelTierTaskHandover(t *testing.T) {
	// Test the scenario where a task is handed over with model "sonnet"
	// but ENGINE_MODEL_CEILING is "haiku"
	os.Setenv("ENGINE_MODEL_CEILING", "haiku")
	defer os.Unsetenv("ENGINE_MODEL_CEILING")

	handoverModel := "sonnet"
	clamped := ClampModelTier(handoverModel)

	if !strings.Contains(strings.ToLower(clamped), "haiku") {
		t.Errorf("Task handover with model %q should be clamped to haiku, got %q",
			handoverModel, clamped)
	}
}
