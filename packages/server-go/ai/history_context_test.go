package ai

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/engine/server/db"
)

func setupHistoryTestProject(t *testing.T) string {
	t.Helper()

	stateDir := t.TempDir()
	projectDir := filepath.Join(t.TempDir(), "project")
	if err := os.MkdirAll(filepath.Join(projectDir, ".github", "references"), 0755); err != nil {
		t.Fatalf("mkdir project refs: %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(projectDir, "PROJECT_GOAL.md"),
		[]byte("Engine should preserve project direction and retrieve the right history when the AI needs it."),
		0644,
	); err != nil {
		t.Fatalf("write project goal: %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(projectDir, ".github", "references", "architecture.md"),
		[]byte("Persistent context, autonomous validation, and agent orchestration are first-class architecture goals."),
		0644,
	); err != nil {
		t.Fatalf("write architecture doc: %v", err)
	}

	t.Setenv("ENGINE_STATE_DIR", stateDir)
	if err := db.Init(projectDir); err != nil {
		t.Fatalf("db init: %v", err)
	}

	return projectDir
}

func seedHistoryFixtures(t *testing.T, projectDir string) {
	t.Helper()

	if err := db.CreateSession("session-current", projectDir, "main"); err != nil {
		t.Fatalf("create current session: %v", err)
	}
	if err := db.CreateSession("session-old", projectDir, "memory"); err != nil {
		t.Fatalf("create old session: %v", err)
	}
	if err := db.CreateSession("session-other-project", projectDir+"-other", "other"); err != nil {
		t.Fatalf("create other-project session: %v", err)
	}

	if err := db.UpdateSessionSummary("session-current", "Current focus: history search and weighted context selection for AI prompts."); err != nil {
		t.Fatalf("update current summary: %v", err)
	}
	if err := db.UpdateSessionSummary("session-old", "Past fix: retrieved validation evidence and session memory to unblock long-running debugging."); err != nil {
		t.Fatalf("update old summary: %v", err)
	}
	if err := db.UpsertProjectDirection(projectDir, "Project goal: Engine should preserve project direction across sessions.\nArchitecture direction: Persistent context is first-class."); err != nil {
		t.Fatalf("upsert project direction: %v", err)
	}

	messages := []struct {
		id        string
		sessionID string
		role      string
		content   string
	}{
		{"m1", "session-old", "assistant", "We fixed context selection by retrieving validation evidence before generating the next code change."},
		{"m2", "session-old", "user", "Please make history search reliable for long-running sessions."},
		{"m3", "session-current", "assistant", "Weighted context blocks should prefer active file focus and relevant prior outcomes."},
		{"m4", "session-other-project", "assistant", "Other project should never leak into this workspace history search."},
	}
	for _, message := range messages {
		if err := db.SaveMessage(message.id, message.sessionID, message.role, message.content, nil); err != nil {
			t.Fatalf("save message %s: %v", message.id, err)
		}
	}

	if err := db.SaveLearningEvent(
		"learning-1",
		"session-old",
		"history search for context selection",
		"Search stored workspace history before asking the model to act.",
		0.95,
		"memory",
		"Used when the agent lost earlier debugging context.",
	); err != nil {
		t.Fatalf("save learning event: %v", err)
	}

	if err := db.SaveValidationResult(
		"validation-1",
		"session-old",
		"context retrieval regression",
		true,
		true,
		0,
		0,
		1200,
		"Validated weighted prompt context with retrieved history evidence.",
		"go test ./...",
	); err != nil {
		t.Fatalf("save validation result: %v", err)
	}
}

func TestBuildConversationWindowKeepsRecentMessages(t *testing.T) {
	history := make([]db.Message, 0, 19)
	for index := 0; index < 18; index++ {
		role := "assistant"
		if index%2 == 0 {
			role = "user"
		}
		history = append(history, db.Message{
			ID:      string(rune('a' + index)),
			Role:    role,
			Content: "message-" + string(rune('a'+index)),
		})
	}
	history = append(history, db.Message{ID: "latest", Role: "user", Content: "current-user"})

	window := BuildConversationWindow(history, "current-user")

	if len(window) != conversationWindowSize+1 {
		t.Fatalf("expected %d messages in window, got %d", conversationWindowSize+1, len(window))
	}
	if got := window[0].Content.(string); got != "message-e" {
		t.Fatalf("expected oldest retained message to be message-e, got %q", got)
	}
	if got := window[len(window)-1].Content.(string); got != "current-user" {
		t.Fatalf("expected final user message to be preserved, got %q", got)
	}
}

func TestSearchHistoryReturnsProjectScopedRelevantHits(t *testing.T) {
	projectDir := setupHistoryTestProject(t)
	seedHistoryFixtures(t, projectDir)

	hits, err := SearchHistory(
		projectDir,
		"session-current",
		"history search context selection",
		[]TabInfo{{Path: filepath.Join(projectDir, "packages", "server-go", "ai", "context.go"), IsActive: true}},
		"project",
		6,
	)
	if err != nil {
		t.Fatalf("search history: %v", err)
	}
	if len(hits) == 0 {
		t.Fatal("expected at least one history hit")
	}

	foundLearning := false
	for _, hit := range hits {
		if strings.Contains(hit.Text, "Other project should never leak") {
			t.Fatalf("search history leaked other project content: %+v", hit)
		}
		if hit.Source == "learning" && strings.Contains(hit.Text, "history search for context selection") {
			foundLearning = true
		}
	}
	if !foundLearning {
		t.Fatalf("expected learning hit in results, got %+v", hits)
	}
}

func TestBuildSelectiveContextPromptIncludesWeightedHistoryAndFocus(t *testing.T) {
	projectDir := setupHistoryTestProject(t)
	seedHistoryFixtures(t, projectDir)

	session, err := db.GetSession("session-current")
	if err != nil {
		t.Fatalf("get session: %v", err)
	}

	prompt := BuildSelectiveContextPrompt(
		projectDir,
		session,
		"Use history search and weighted context selection for AI prompts.",
		[]TabInfo{
			{Path: filepath.Join(projectDir, "packages", "server-go", "ai", "context.go"), IsActive: true},
			{Path: filepath.Join(projectDir, "PROJECT_GOAL.md"), IsDirty: true},
		},
	)

	if !strings.Contains(prompt, "Selective context blocks for this request") {
		t.Fatalf("expected prompt header, got %q", prompt)
	}
	if !strings.Contains(prompt, "Current workspace focus") {
		t.Fatalf("expected current focus block, got %q", prompt)
	}
	if !strings.Contains(prompt, "Retrieved history") {
		t.Fatalf("expected retrieved history block, got %q", prompt)
	}
	if !strings.Contains(prompt, "Active tab:") {
		t.Fatalf("expected active tab in prompt, got %q", prompt)
	}
}

func TestBuildAttentionConversationWindowUsesResidualsForOlderMessages(t *testing.T) {
	history := make([]db.Message, 0, 21)
	history = append(history, db.Message{ID: "carry", Role: "assistant", Content: "validation note"})
	for index := 1; index <= 13; index++ {
		history = append(history, db.Message{
			ID:      "older-" + string(rune('a'+index)),
			Role:    "assistant",
			Content: "attention retrieval note",
		})
	}
	for index := 14; index < 20; index++ {
		history = append(history, db.Message{
			ID:      "anchor-" + string(rune('a'+index)),
			Role:    "assistant",
			Content: "recent anchor",
		})
	}
	history = append(history, db.Message{ID: "latest-user", Role: "user", Content: "current-user"})

	withoutResiduals := BuildAttentionConversationWindow(history, "attention retrieval validation", nil, nil)
	withResiduals := BuildAttentionConversationWindow(history, "attention retrieval validation", nil, map[string]float64{
		attentionSourceKey("message", "carry"): 1,
	})

	if selectionIDs(withoutResiduals.Selections)[attentionSourceKey("message", "carry")] {
		t.Fatalf("expected carry message to be excluded without residual boost: %+v", withoutResiduals.Selections)
	}
	if !selectionIDs(withResiduals.Selections)[attentionSourceKey("message", "carry")] {
		t.Fatalf("expected carry message to be included with residual boost: %+v", withResiduals.Selections)
	}
}

func TestBuildAttentionResidualProfilePrefersMatchingResiduals(t *testing.T) {
	now := time.Now().UTC().Format(time.RFC3339)
	profile := buildAttentionResidualProfile([]db.AttentionResidual{
		{
			SourceKey:   attentionSourceKey("block", "retrieved-history"),
			SessionID:   "session-current",
			QueryText:   "validation retrieval history",
			SourceLabel: "Retrieved history",
			Context:     "validation evidence from old session",
			Weight:      0.9,
			Score:       4.2,
			CreatedAt:   now,
		},
		{
			SourceKey:   attentionSourceKey("block", "workspace-direction"),
			SessionID:   "session-old",
			QueryText:   "frontend styling palette",
			SourceLabel: "Workspace direction",
			Context:     "colors and typography",
			Weight:      0.9,
			Score:       4.2,
			CreatedAt:   now,
		},
	}, "session-current", "need validation history retrieval", nil)

	if profile[attentionSourceKey("block", "retrieved-history")] <= profile[attentionSourceKey("block", "workspace-direction")] {
		t.Fatalf("expected matching residual to score higher, got %+v", profile)
	}
}

func TestSearchHistoryWithResidualsBoostsPreferredSource(t *testing.T) {
	projectDir := setupHistoryTestProject(t)
	if err := db.CreateSession("session-current", projectDir, "main"); err != nil {
		t.Fatalf("create session: %v", err)
	}
	if err := db.SaveMessage("message-preferred", "session-current", "assistant", "retrieval fallback", nil); err != nil {
		t.Fatalf("save message: %v", err)
	}
	if err := db.SaveLearningEvent(
		"learning-other",
		"session-current",
		"retrieval fallback",
		"use session memory",
		0.5,
		"memory",
		"same phrase but different source",
	); err != nil {
		t.Fatalf("save learning: %v", err)
	}

	withoutResiduals, err := SearchHistoryWithResiduals(projectDir, "session-current", "retrieval fallback", nil, "project", 2, nil)
	if err != nil {
		t.Fatalf("search without residuals: %v", err)
	}
	withResiduals, err := SearchHistoryWithResiduals(projectDir, "session-current", "retrieval fallback", nil, "project", 2, map[string]float64{
		attentionSourceKey("message", "message-preferred"): 1,
	})
	if err != nil {
		t.Fatalf("search with residuals: %v", err)
	}

	if len(withoutResiduals) == 0 || len(withResiduals) == 0 {
		t.Fatalf("expected history hits in both searches, got without=%+v with=%+v", withoutResiduals, withResiduals)
	}
	if withoutResiduals[0].Source != "learning" {
		t.Fatalf("expected learning to win baseline scoring, got %+v", withoutResiduals)
	}
	if withResiduals[0].Source != "message" {
		t.Fatalf("expected message to win with residual boost, got %+v", withResiduals)
	}
}

func TestAttentionResidualsRoundTripThroughDB(t *testing.T) {
	projectDir := setupHistoryTestProject(t)
	if err := db.CreateSession("session-current", projectDir, "main"); err != nil {
		t.Fatalf("create session: %v", err)
	}
	if err := db.SaveMessage("user-msg", "session-current", "user", "remember this", nil); err != nil {
		t.Fatalf("save message: %v", err)
	}
	if err := db.SaveAttentionResiduals([]db.AttentionResidual{
		{
			ID:          "residual-1",
			SessionID:   "session-current",
			MessageID:   "user-msg",
			SourceKey:   attentionSourceKey("block", "retrieved-history"),
			SourceType:  "context_block",
			SourceLabel: "Retrieved history",
			QueryText:   "remember this",
			Weight:      0.72,
			Score:       4.1,
			Context:     "history evidence",
		},
	}); err != nil {
		t.Fatalf("save residuals: %v", err)
	}

	residuals, err := db.GetProjectAttentionResiduals(projectDir, 10)
	if err != nil {
		t.Fatalf("get residuals: %v", err)
	}
	if len(residuals) != 1 {
		t.Fatalf("expected one residual, got %+v", residuals)
	}
	if residuals[0].SourceKey != attentionSourceKey("block", "retrieved-history") {
		t.Fatalf("unexpected residual round trip: %+v", residuals[0])
	}
}

func selectionIDs(selections []conversationWindowSelection) map[string]bool {
	ids := make(map[string]bool, len(selections))
	for _, selection := range selections {
		ids[attentionSourceKey("message", selection.MessageID)] = true
	}
	return ids
}

// ── FormatHistorySearchResults ────────────────────────────────────────────────

func TestFormatHistorySearchResults_Empty(t *testing.T) {
	result := FormatHistorySearchResults("my query", nil, "sess-1")
	if !strings.Contains(result, "No stored history matched") {
		t.Errorf("expected no-match message, got %q", result)
	}
	if !strings.Contains(result, "my query") {
		t.Errorf("expected query in message, got %q", result)
	}
}

func TestFormatHistorySearchResults_CurrentSession(t *testing.T) {
	hits := []historySearchHit{
		{SessionID: "sess-1", Role: "user", Source: "message", Text: "test output shows PASS", Score: 0.9},
	}
	result := FormatHistorySearchResults("test", hits, "sess-1")
	if !strings.Contains(result, "current-session") {
		t.Errorf("expected current-session scope, got %q", result)
	}
}

func TestFormatHistorySearchResults_OtherSession(t *testing.T) {
	hits := []historySearchHit{
		{SessionID: "sess-old", Role: "assistant", Source: "message", Text: "old context", Score: 0.5},
	}
	result := FormatHistorySearchResults("context", hits, "sess-1")
	if !strings.Contains(result, "project") {
		t.Errorf("expected project scope, got %q", result)
	}
}

func TestFormatHistorySearchResults_EmptyRole_UsesSource(t *testing.T) {
	hits := []historySearchHit{
		{SessionID: "sess-2", Role: "", Source: "learning", Text: "learning entry text", Score: 0.7},
	}
	result := FormatHistorySearchResults("learn", hits, "sess-1")
	if !strings.Contains(result, "learning") {
		t.Errorf("expected source used as role when role empty, got %q", result)
	}
}
