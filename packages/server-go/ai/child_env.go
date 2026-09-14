package ai

import (
	"strings"
)

// childEnv returns a copy of base with all entries whose key matches "PORT"
// (case-insensitive on Windows) removed. This prevents spawned worker sessions
// from hijacking the orchestrator's port when they run processes like npm start
// that read PORT from the environment.
//
// The function preserves the order and byte-for-byte identity of all other entries.
// It does not mutate the input slice.
func childEnv(base []string) []string {
	out := make([]string, 0, len(base))
	for _, kv := range base {
		// Split on the first '=' to get the key.
		parts := strings.SplitN(kv, "=", 2)
		if len(parts) < 1 {
			out = append(out, kv)
			continue
		}
		key := parts[0]
		// Compare case-insensitively to match Windows environment variable semantics.
		if strings.EqualFold(key, "PORT") {
			continue
		}
		out = append(out, kv)
	}
	return out
}
