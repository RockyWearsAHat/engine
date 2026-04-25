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

// ── Pure helper coverage ──────────────────────────────────────────────────────

func TestCurrentSessionID_Nil(t *testing.T) {
	if got := currentSessionID(nil); got != "" {
		t.Errorf("expected empty string for nil session, got %q", got)
	}
}

func TestCurrentSessionID_NonNil(t *testing.T) {
	sess := &db.Session{ID: "sess-abc"}
	if got := currentSessionID(sess); got != "sess-abc" {
		t.Errorf("expected sess-abc, got %q", got)
	}
}

func TestRecencyScore_InvalidTimestamp(t *testing.T) {
	if got := recencyScore("not-a-time"); got != 0 {
		t.Errorf("expected 0 for invalid timestamp, got %f", got)
	}
}

func TestRecencyScore_VeryOld(t *testing.T) {
	old := time.Now().Add(-30 * 24 * time.Hour).UTC().Format(time.RFC3339)
	if got := recencyScore(old); got != 0.05 {
		t.Errorf("expected 0.05 for old timestamp, got %f", got)
	}
}

func TestRecencyScore_Recent(t *testing.T) {
	recent := time.Now().Add(-1 * time.Hour).UTC().Format(time.RFC3339)
	if got := recencyScore(recent); got != 0.6 {
		t.Errorf("expected 0.6 for recent timestamp, got %f", got)
	}
}

func TestRecencyScore_WithinDay(t *testing.T) {
	ts := time.Now().Add(-12 * time.Hour).UTC().Format(time.RFC3339)
	if got := recencyScore(ts); got != 0.4 {
		t.Errorf("expected 0.4 for 12h-old timestamp, got %f", got)
	}
}

func TestRecencyScore_WithinWeek(t *testing.T) {
	ts := time.Now().Add(-3 * 24 * time.Hour).UTC().Format(time.RFC3339)
	if got := recencyScore(ts); got != 0.2 {
		t.Errorf("expected 0.2 for 3-day-old timestamp, got %f", got)
	}
}

func TestHistoryScopeAllows_NotCurrentSession(t *testing.T) {
	if !historyScopeAllows("project", "sess-1", "sess-2") {
		t.Error("expected true for non-current-session scope")
	}
}

func TestHistoryScopeAllows_CurrentSession_Match(t *testing.T) {
	if !historyScopeAllows("current-session", "sess-1", "sess-1") {
		t.Error("expected true when session IDs match")
	}
}

func TestHistoryScopeAllows_CurrentSession_NoMatch(t *testing.T) {
	if historyScopeAllows("current-session", "sess-1", "sess-2") {
		t.Error("expected false when session IDs differ")
	}
}

func TestHistoryScopeAllows_CurrentSession_EmptyID(t *testing.T) {
	if historyScopeAllows("current-session", "", "sess-2") {
		t.Error("expected false when currentSessionID is empty")
	}
}

func TestMaxInt_FirstLarger(t *testing.T) {
	if got := maxInt(5, 3); got != 5 {
		t.Errorf("expected 5, got %d", got)
	}
}

func TestMaxInt_SecondLarger(t *testing.T) {
	if got := maxInt(3, 5); got != 5 {
		t.Errorf("expected 5, got %d", got)
	}
}

// ─── formatSelectiveContextPrompt ─────────────────────────────────────────────

func TestFormatSelectiveContextPrompt_Empty(t *testing.T) {
	got := formatSelectiveContextPrompt(nil)
	if got != "" {
		t.Errorf("expected empty string for nil blocks, got %q", got)
	}
}

func TestFormatSelectiveContextPrompt_WithBlocks(t *testing.T) {
	blocks := []contextBlock{
		{Weight: 0.9, Title: "Block A", Body: "body content A"},
		{Weight: 0.5, Title: "Block B", Body: "body content B"},
	}
	got := formatSelectiveContextPrompt(blocks)
	if got == "" {
		t.Error("expected non-empty output for blocks")
	}
	if !strings.Contains(got, "Block A") {
		t.Errorf("expected Block A in output, got %q", got)
	}
}

func TestFormatSelectiveContextPrompt_EmptyBody(t *testing.T) {
	blocks := []contextBlock{
		{Weight: 0.9, Title: "Empty Body Block", Body: ""},
	}
	// Block with empty body should be skipped.
	got := formatSelectiveContextPrompt(blocks)
	_ = got // Should not panic.
}

// ─── BuildAttentionResidualRecords ────────────────────────────────────────────

func TestBuildAttentionResidualRecords_ZeroWeight(t *testing.T) {
	window := conversationWindowResult{}
	ctx := selectiveContextResult{}
	records := BuildAttentionResidualRecords("sess", "msg", "query text", window, ctx)
	if records == nil {
		t.Error("expected non-nil slice")
	}
}

func TestBuildAttentionResidualRecords_WithSelections(t *testing.T) {
	window := conversationWindowResult{
		Selections: []conversationWindowSelection{
			{MessageID: "m1", Role: "user", Content: "hello world", Weight: 0.8, Score: 1.2},
			{MessageID: "m2", Role: "user", Content: "skipped", Weight: 0.0, Score: 0.0}, // zero weight, skipped
		},
	}
	ctx := selectiveContextResult{
		Blocks: []contextBlock{
			{Key: "k1", Title: "T1", Body: "body", Weight: 0.7, Score: 1.0},
			{Key: "k2", Title: "T2", Body: "body2", Weight: 0.0, Score: 0.0}, // zero weight, skipped
		},
		HistoryHits: []historySearchHit{},
	}
	records := BuildAttentionResidualRecords("sess", "msg", "user query", window, ctx)
	if len(records) < 2 {
		t.Errorf("expected at least 2 records (selection + block), got %d", len(records))
	}
}

func TestBuildAttentionResidualRecords_WithHistoryHits(t *testing.T) {
	window := conversationWindowResult{}
	ctx := selectiveContextResult{
		HistoryHits: []historySearchHit{
			{Source: "src", SourceKey: "sk1", Text: "hit text", Weight: 0.6, Score: 2.0},
			{Source: "src2", SourceKey: "sk2", Text: "skip", Weight: 0.0, Score: 0.0},
		},
	}
	records := BuildAttentionResidualRecords("sess", "msg", "query", window, ctx)
	found := false
	for _, r := range records {
		if r.SourceKey == "sk1" {
			found = true
		}
	}
	if !found {
		t.Error("expected history hit sk1 in records")
	}
}

// ─── BuildAttentionResidualProfile ───────────────────────────────────────────

func TestBuildAttentionResidualProfile_EmptyDB(t *testing.T) {
	setupHistoryTestProject(t) // initializes DB
	// A project path with no residuals should succeed with an empty map.
	profile, err := BuildAttentionResidualProfile("/no-such-project-xyz", "sess", "query", nil)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if profile == nil {
		t.Error("expected non-nil profile")
	}
}

func TestFormatSelectiveContextPrompt_NonEmpty(t *testing.T) {
	result := formatSelectiveContextPrompt([]contextBlock{
		{Title: "File", Body: "content", Key: "k1", Weight: 1.0, Score: 0.5},
	})
	if result == "" {
		t.Error("expected non-empty prompt")
	}
}

func TestFormatSelectiveContextPrompt_ExistingEmpty(t *testing.T) {
	result := formatSelectiveContextPrompt([]contextBlock{})
	if result != "" {
		t.Errorf("expected empty string for empty blocks, got %q", result)
	}
}
