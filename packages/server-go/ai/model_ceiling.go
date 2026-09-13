package ai

import (
	"os"
	"strings"
)

// modelTierHierarchy defines the model hierarchy from cheap to expensive.
// Used to enforce the ENGINE_MODEL_CEILING policy.
var modelTierHierarchy = []struct {
	tier   string
	prefix string
	id     string
}{
	{"haiku", "claude-haiku", "claude-haiku-4-5-20251001"},
	{"sonnet", "claude-sonnet", "claude-sonnet-4-20250514"},
	{"opus", "claude-opus", "claude-opus-4-8"},
	{"fable", "claude-fable", "claude-fable-20250305"},
}

// ClampModelTier enforces the ENGINE_MODEL_CEILING policy.
// If the requested model tier exceeds the ceiling, it returns the ceiling model.
// Otherwise returns the requested model unchanged.
// The model string can be a tier name ("haiku", "sonnet", "opus", "fable") or a full model ID.
func ClampModelTier(requested string) string {
	ceiling := os.Getenv("ENGINE_MODEL_CEILING")
	if ceiling == "" {
		ceiling = "haiku" // Default ceiling is haiku
	}
	ceiling = strings.TrimSpace(strings.ToLower(ceiling))

	// Find the ceiling tier in the hierarchy
	var ceilingIndex int = -1
	for i, entry := range modelTierHierarchy {
		if entry.tier == ceiling || strings.HasPrefix(strings.ToLower(entry.id), strings.ToLower(ceiling)) {
			ceilingIndex = i
			break
		}
	}
	if ceilingIndex == -1 {
		// Unknown ceiling, default to haiku
		ceilingIndex = 0
	}

	// Find which tier the requested model belongs to
	requestedLower := strings.ToLower(strings.TrimSpace(requested))
	if requestedLower == "" {
		// Empty model: return unchanged (let caller handle the error)
		return requested
	}

	var requestedIndex int = -1
	for i, entry := range modelTierHierarchy {
		if entry.tier == requestedLower ||
			strings.HasPrefix(requestedLower, entry.prefix) ||
			strings.HasPrefix(requestedLower, entry.id) {
			requestedIndex = i
			break
		}
	}

	// If requested tier is above ceiling (higher index), clamp to ceiling
	if requestedIndex > ceilingIndex {
		return modelTierHierarchy[ceilingIndex].id
	}

	// Requested tier is at or below ceiling, return as-is
	return requested
}
