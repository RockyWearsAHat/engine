package ai

import (
	"fmt"
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
	records := BuildAttentionResidualRecords("sess", "msg", "query text", window, ctx, "")
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
	records := BuildAttentionResidualRecords("sess", "msg", "user query", window, ctx, "")
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
	records := BuildAttentionResidualRecords("sess", "msg", "query", window, ctx, "")
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

func TestBuildAttentionResidualRecords_SessionConclusionAdded(t *testing.T) {
	window := conversationWindowResult{}
	ctx := selectiveContextResult{}
	summary := "We decided to use SQLite for the attention residual store."
	records := BuildAttentionResidualRecords("sess", "msg", "query", window, ctx, summary)

	var found *db.AttentionResidual
	for i := range records {
		if records[i].SourceType == "session_conclusion" {
			found = &records[i]
			break
		}
	}
	if found == nil {
		t.Fatal("expected a session_conclusion residual when sessionSummary is non-empty")
	}
	if found.SourceKey != attentionSourceKey("block", "session-memory") {
		t.Errorf("session_conclusion SourceKey = %q, want %q", found.SourceKey, attentionSourceKey("block", "session-memory"))
	}
	if found.Weight < 1.0 {
		t.Errorf("session_conclusion Weight = %.2f, want >= 1.0", found.Weight)
	}
	if found.Context == "" {
		t.Error("session_conclusion Context should not be empty")
	}
}

func TestBuildAttentionResidualRecords_EmptySummaryOmitted(t *testing.T) {
	window := conversationWindowResult{}
	ctx := selectiveContextResult{}
	records := BuildAttentionResidualRecords("sess", "msg", "query", window, ctx, "   ")
	for _, r := range records {
		if r.SourceType == "session_conclusion" {
			t.Error("expected no session_conclusion residual for blank summary")
		}
	}
}

// ─── BuildAttentionResidualProfile ───────────────────────────────────────────

func TestBuildAttentionResidualProfile_EmptyDB(t *testing.T) {
	setupHistoryTestProject(t) // initializes DB
	// A project path with no residuals should succeed with an empty map.
	profile := BuildAttentionResidualProfile("/no-such-project-xyz", "sess", "query", nil)
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

// ─── New coverage tests ───────────────────────────────────────────────────────

// TestBuildAttentionConversationWindow_SingleMessage covers the len(prior)==0 path.
func TestBuildAttentionConversationWindow_SingleMessage(t *testing.T) {
	history := []db.Message{{ID: "only", Role: "user", Content: "hi"}}
	result := BuildAttentionConversationWindow(history, "new question", nil, nil)
	if len(result.Messages) == 0 {
		t.Error("expected at least one message")
	}
}

// TestBuildAttentionConversationWindow_EmptyHistory covers the len(history)==0 path.
func TestBuildAttentionConversationWindow_EmptyHistory(t *testing.T) {
	result := BuildAttentionConversationWindow(nil, "hello", nil, nil)
	if len(result.Messages) != 1 {
		t.Errorf("expected 1 message for empty history, got %d", len(result.Messages))
	}
}

// TestBuildAttentionConversationWindow_SmallWindow covers the len(prior)<=conversationWindowSize path.
func TestBuildAttentionConversationWindow_SmallWindow(t *testing.T) {
	history := []db.Message{
		{ID: "m1", Role: "user", Content: "first"},
		{ID: "m2", Role: "assistant", Content: "second"},
		{ID: "m3", Role: "user", Content: "third"},
	}
	result := BuildAttentionConversationWindow(history, "query", nil, nil)
	if len(result.Messages) == 0 {
		t.Error("expected messages in result")
	}
}

func TestFlattenTabPaths_EmptyPath(t *testing.T) {
	tabs := []TabInfo{{Path: ""}, {Path: "src/main.go"}}
	got := flattenTabPaths(tabs)
	if strings.Contains(got, "  ") {
		t.Errorf("unexpected double space from empty path: %q", got)
	}
	if !strings.Contains(got, "src/main.go") {
		t.Errorf("expected src/main.go in result: %q", got)
	}
}

func TestAttentionSourceKey_EmptyID(t *testing.T) {
	got := attentionSourceKey("message", "")
	if got != "message" {
		t.Errorf("expected 'message', got %q", got)
	}
}

func TestBuildCurrentFocusContext_RootPath(t *testing.T) {
	// filepath.Base("/") == "/" which triggers the label=tab.Path branch.
	tabs := []TabInfo{{Path: "/", IsActive: false}}
	got := buildCurrentFocusContext(tabs)
	// "/" should appear as the label since Base returns "/"
	if got == "" {
		t.Error("expected non-empty focus context")
	}
}

func TestApplySoftmaxWeights_Empty(t *testing.T) {
	got := applySoftmaxWeights([]contextBlock{})
	if len(got) != 0 {
		t.Errorf("expected empty result, got %d blocks", len(got))
	}
}

func TestNormalizeScoreWeights_Empty(t *testing.T) {
	got := normalizeScoreWeights([]float64{})
	if len(got) != 0 {
		t.Errorf("expected empty result, got %v", got)
	}
}

func TestScoreHistoryCandidate_TabBase(t *testing.T) {
	// Base of "/project/main.go" is "main.go"; text contains "main.go" → score += 0.8.
	tabs := []TabInfo{{Path: "/project/main.go"}}
	score := scoreHistoryCandidate("see main.go for details", "query", nil, tabs)
	if score <= 0 {
		t.Error("expected positive score for tab base match")
	}
}

func TestScoreHistoryCandidate_TabFull(t *testing.T) {
	// text contains the full path → score += 1.1.
	tabs := []TabInfo{{Path: "/project/main.go"}}
	score := scoreHistoryCandidate("see /project/main.go for details", "query", nil, tabs)
	if score <= 0 {
		t.Error("expected positive score for full path match")
	}
}

func TestScoreHistoryCandidate_EmptyTabPath(t *testing.T) {
	// Tab with empty path hits the continue branch.
	tabs := []TabInfo{{Path: ""}, {Path: "helper.go"}}
	score := scoreHistoryCandidate("see helper.go for details", "query", nil, tabs)
	if score < 0 {
		t.Error("unexpected negative score")
	}
}

func TestScoreTermCoverage_EmptyTerms(t *testing.T) {
	got := scoreTermCoverage("some text", []string{})
	if got != 0 {
		t.Errorf("expected 0 for empty terms, got %v", got)
	}
}

func TestSearchHistoryWithResiduals_LimitOverMax(t *testing.T) {
	setupHistoryTestProject(t)
	// limit > maxHistorySearchResultLimit → clamped to max.
	hits, err := SearchHistoryWithResiduals(t.TempDir(), "", "hello", nil, "project", 999, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// hits may be empty (clean DB), but the function must not panic.
	_ = hits
}

func TestBuildSelectiveContext_EmptyProject(t *testing.T) {
	setupHistoryTestProject(t)
	// Empty project dir with no history and no workspace guide → returns empty result.
	dir := t.TempDir()
	result := BuildSelectiveContext(dir, nil, "query", nil, nil)
	// Prompt may be empty if no context is available.
	_ = result
}

func TestBuildAttentionResidualProfile_EmptySourceKey(t *testing.T) {
	// Residual with empty SourceKey hits the first continue branch.
	profile := buildAttentionResidualProfile([]db.AttentionResidual{
		{SourceKey: "", Weight: 1.0, Score: 1.0},
	}, "sess", "query", nil)
	if len(profile) != 0 {
		t.Errorf("expected empty profile when SourceKey is blank, got %v", profile)
	}
}

func TestBuildAttentionResidualProfile_ZeroScore(t *testing.T) {
	// Residual with effective score <= 0 hits the second continue branch.
	profile := buildAttentionResidualProfile([]db.AttentionResidual{
		{SourceKey: "k1", Weight: -100.0, Score: 0.0, CreatedAt: ""},
	}, "sess", "query", nil)
	if len(profile) != 0 {
		t.Errorf("expected empty profile when score <= 0, got %v", profile)
	}
}

func TestFormatHistoryHits_BudgetBreak(t *testing.T) {
	// maxChars=1 ensures the first hit exceeds budget and causes a break.
	hits := []historySearchHit{
		{Score: 1.0, Source: "message", Text: "a long piece of text that exceeds budget"},
		{Score: 0.5, Source: "message", Text: "second hit"},
	}
	got := formatHistoryHits(hits, "", 1)
	// With maxChars=1, nothing fits.
	_ = got
}

func TestExtractSearchTerms_ShortTerm(t *testing.T) {
	// "ab" is len 2 with no digits → skipped by the len < 3 continue.
	got := extractSearchTerms("ab hello world")
	for _, term := range got {
		if term == "ab" {
			t.Error("expected 'ab' to be filtered (len < 3, no digits)")
		}
	}
	found := false
	for _, term := range got {
		if term == "hello" {
			found = true
		}
	}
	if !found {
		t.Error("expected 'hello' to be included")
	}
}

func TestExtractSearchTerms_DuplicateTerm(t *testing.T) {
	// "hello hello" → second "hello" hits the seen[term] continue.
	got := extractSearchTerms("hello hello world")
	count := 0
	for _, term := range got {
		if term == "hello" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected 'hello' to appear exactly once, got %d", count)
	}
}

func TestFormatSelectiveContextPrompt_BudgetExceeded(t *testing.T) {
	// Fill the budget with the first block, then remaining <= 0 → break on second block.
	bigBody := strings.Repeat("x", selectiveContextCharBudget+100)
	blocks := []contextBlock{
		{Title: "Block1", Body: bigBody, Weight: 0.8, Score: 1.0},
		{Title: "Block2", Body: "second block", Weight: 0.2, Score: 0.5},
	}
	got := formatSelectiveContextPrompt(blocks)
	if got == "" {
		t.Error("expected some output")
	}
	if strings.Contains(got, "Block2") {
		t.Error("second block should not appear when budget is exceeded")
	}
}

func TestFormatSelectiveContextPrompt_LargeEntry(t *testing.T) {
	// Block body fits but formatted entry > remaining → triggers inner truncation path.
	// Use a body that will result in entry larger than remaining budget.
	remaining := 100
	body := strings.Repeat("b", remaining) // body that fits remaining-32 budget
	blocks := []contextBlock{
		{Title: "TitleThatIsLong", Body: body, Weight: 0.5, Score: 0.5},
	}
	got := formatSelectiveContextPrompt(blocks)
	_ = got // just ensure no panic
}

func TestFormatSelectiveContextPrompt_ExistingEmpty(t *testing.T) {
	result := formatSelectiveContextPrompt([]contextBlock{})
	if result != "" {
		t.Errorf("expected empty string for empty blocks, got %q", result)
	}
}

// TestExtractSearchTerms_EmptyAfterTrim covers the `term == ""` continue.
// "..." is kept as a token by FieldsFunc (dot is not a split char) then Trim removes all dots → "".
func TestExtractSearchTerms_EmptyAfterTrim(t *testing.T) {
	got := extractSearchTerms("... hello world")
	for _, term := range got {
		if term == "" {
			t.Error("empty term should not appear in results")
		}
	}
}

// TestExtractSearchTerms_StopWord covers the historySearchStopWords blocked continue.
func TestExtractSearchTerms_StopWord(t *testing.T) {
	got := extractSearchTerms("the hello world")
	for _, term := range got {
		if term == "the" {
			t.Error("stop word 'the' should be filtered")
		}
	}
}

// TestFormatSelectiveContextPrompt_LongTitleEntryTruncation covers the
// inner `len(entry) > remaining` path. A title longer than 23 chars makes the
// entry prefix overhead exceed 32, so entry length > remaining when budget is tight.
// Scenario: block1 leaves ~40 chars remaining; block2 has 50-char title.
// bodyBudget2 = max(40-32,0)=8; entry2 = prefix(60)+body(8)=68 > 40 → inner truncate path.
func TestFormatSelectiveContextPrompt_LongTitleEntryTruncation(t *testing.T) {
	// Consume budget so that ~40 chars remain after block1.
	header := "Selective context blocks for this request (higher weight means more relevant right now):"
	headerLen := len(header)
	targetRemaining := 40
	// firstBodySize such that headerLen + entry1Len = 3200 - targetRemaining
	// entry1 prefix = "\n[0.90] First\n" = 14 chars
	// entry1 = prefix + body = budget - headerLen - targetRemaining - 14 → body
	targetFill := selectiveContextCharBudget - headerLen - targetRemaining
	// entry = prefix(14) + body → body = targetFill - 14
	firstBodySize := targetFill - 14 - 3 // -3 for truncation suffix "..."
	if firstBodySize < 1 {
		firstBodySize = 1
	}
	longTitle := strings.Repeat("T", 50) // 50-char title makes prefix = "\n[0.10] " + 50 + "\n" = 60 chars
	blocks := []contextBlock{
		{Title: "First", Body: strings.Repeat("a", firstBodySize+3), Weight: 0.9, Score: 1.0},
		{Title: longTitle, Body: "smallbod", Weight: 0.1, Score: 0.5},
	}
	got := formatSelectiveContextPrompt(blocks)
	if got == "" {
		t.Error("expected non-empty output")
	}
}

// TestFormatSelectiveContextPrompt_EntryBodyBudgetZero covers the
// `entryBodyBudget == 0 → break` path (inner break in len(entry) > remaining branch).
// Scenario: remaining=24 after block1, block2 has long title.
// bodyBudget = max(24-32,0) = 0 → wait, that triggers bodyBudget==0 first.
// Actually need remaining between 25 and 32 for bodyBudget>0 but entryBodyBudget==0.
// remaining=30: bodyBudget=max(30-32,0)=0 → still hits bodyBudget break first.
// The entryBodyBudget==0 path needs: bodyBudget>0 AND entryBodyBudget==0.
// remaining=33: bodyBudget=max(33-32,0)=1; body=1char, longTitle entry=60+1=61>33
// entryBodyBudget=max(33-24,0)=9 → not 0.
// remaining=25: bodyBudget=max(25-32,0)=0 → still bodyBudget break.
// entryBodyBudget==0 fires when remaining<=24 AND bodyBudget>0 is impossible since bodyBudget=max(rem-32,0).
// TestFormatSelectiveContextPrompt_RemainderLEQ0Break exercises the body=="" continue path.
func TestFormatSelectiveContextPrompt_RemainderLEQ0Break(t *testing.T) {
	blocks := []contextBlock{
		{Title: "X", Body: "", Weight: 0.5, Score: 0.5},
		{Title: "Y", Body: "hello world great content here", Weight: 0.5, Score: 0.5},
	}
	got := formatSelectiveContextPrompt(blocks)
	_ = got
}

// TestFormatSelectiveContextPrompt_BodyBudgetZero covers `if bodyBudget == 0 { break }`.
// This triggers when remaining <= 32. Use a first block that leaves < 32 chars.
func TestFormatSelectiveContextPrompt_BodyBudgetZero(t *testing.T) {
	// First block uses all but 10 chars of budget.
	budgetLeft := 10 // remaining after first block write
	// Initial builder writes a header ~82 chars. selectiveContextCharBudget=3200.
	// After first block: builder.Len() = 3200 - budgetLeft → first block body must be 3200-82-budgetLeft.
	firstBodySize := selectiveContextCharBudget - 82 - budgetLeft // ~3108 chars
	blocks := []contextBlock{
		{Title: "First", Body: strings.Repeat("a", firstBodySize), Weight: 0.9, Score: 1.0},
		{Title: "Second", Body: "hello", Weight: 0.1, Score: 0.5},
	}
	got := formatSelectiveContextPrompt(blocks)
	if !strings.Contains(got, "First") {
		t.Error("first block should appear")
	}
}

// TestSearchHistoryWithResiduals_ScopeSession covers the "session" scope path.
func TestSearchHistoryWithResiduals_ScopeSession(t *testing.T) {
	projectDir := setupHistoryTestProject(t)
	hits, err := SearchHistoryWithResiduals(projectDir, "sess1", "anything", nil, "session", 5, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_ = hits
}

// TestSearchHistoryWithResiduals_Dedup covers the deduplication `continue` in SearchHistoryWithResiduals.
// We insert the same message content multiple times across sessions so it deduplicates.
func TestSearchHistoryWithResiduals_Dedup(t *testing.T) {
	projectDir := setupHistoryTestProject(t)
	if err := db.CreateSession("dedup-s1", projectDir, "main"); err != nil {
		t.Fatalf("create session: %v", err)
	}
	if err := db.CreateSession("dedup-s2", projectDir, "main"); err != nil {
		t.Fatalf("create session: %v", err)
	}
	// Insert identical content in both sessions so they deduplicate.
	content := "uniquededupcontent hello world foo bar"
	for i := range 10 {
		_ = db.SaveMessage(fmt.Sprintf("dedup-s%d-m%d", i%2+1, i), fmt.Sprintf("dedup-s%d", i%2+1), "user", content, "main")
	}
	hits, err := SearchHistoryWithResiduals(projectDir, "dedup-s1", "uniquededupcontent hello world", nil, "project", 2, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_ = hits
}
// TestSearchHistoryWithResiduals_ValidationSameSession covers score += 0.25 when
// a validation result belongs to the current session.
func TestSearchHistoryWithResiduals_ValidationSameSession(t *testing.T) {
	projectDir := setupHistoryTestProject(t)
	sessionID := "val-same-sess"
	if err := db.CreateSession(sessionID, projectDir, "main"); err != nil {
		t.Fatalf("create session: %v", err)
	}
	if err := db.SaveValidationResult("vr-same", sessionID, "fix login bug", true, true, 0, 0, 100, "tests pass login authentication", "go test"); err != nil {
		t.Fatalf("save validation: %v", err)
	}
	hits, err := SearchHistoryWithResiduals(projectDir, sessionID, "login authentication", nil, "project", 10, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_ = hits
}

// TestSearchHistoryWithResiduals_LowScoreContinue covers the score<=threshold continue
// paths where messages don't match the query.
func TestSearchHistoryWithResiduals_LowScoreContinue(t *testing.T) {
	projectDir := setupHistoryTestProject(t)
	// Create two sessions: one with unrelated data, one we search from.
	dataSess := "low-score-data"
	searchSess := "low-score-search"
	if err := db.CreateSession(dataSess, projectDir, "main"); err != nil {
		t.Fatalf("create data session: %v", err)
	}
	if err := db.CreateSession(searchSess, projectDir, "main"); err != nil {
		t.Fatalf("create search session: %v", err)
	}
	// Message belongs to dataSess — no session bonus from searchSess.
	// Content has no terms matching "abc".
	if err := db.SaveMessage("msg-low", dataSess, "user", "xyzzy frobble grault waldo", nil); err != nil {
		t.Fatalf("save message: %v", err)
	}
	// Search from searchSess so dataSess message gets no session bonus.
	// scoreHistoryCandidate("xyzzy frobble grault waldo", "abc", ...) == 0 → score=1.2 → continue.
	hits, err := SearchHistoryWithResiduals(projectDir, searchSess, "abc", nil, "project", 10, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_ = hits
}

// TestFormatSelectiveContextPrompt_BudgetExhausted covers the remaining<=0 break path.
func TestFormatSelectiveContextPrompt_BudgetExhausted(t *testing.T) {
	// selectiveContextCharBudget = 3200; one large block should exhaust the budget.
	largeBody := string(make([]rune, selectiveContextCharBudget+100))
	for i := range []rune(largeBody) {
		largeBody = largeBody[:i] + "x" + largeBody[i+1:]
		if i >= 10 { // just fill first 10 chars; rest stays as zero-value
			break
		}
	}
	// Actually just use strings.Repeat for simplicity.
	largeBody = fmt.Sprintf("%0*d", selectiveContextCharBudget+100, 0)
	blocks := []contextBlock{
		{Key: "k1", Title: "Block 1", Body: largeBody, Weight: 1.0},
		{Key: "k2", Title: "Block 2", Body: "should not appear", Weight: 0.5},
	}
	result := formatSelectiveContextPrompt(blocks)
	if result == "" {
		t.Error("expected non-empty result")
	}
	if strings.Contains(result, "should not appear") {
		t.Error("expected second block to be cut off by budget")
	}
}

// TestFormatSelectiveContextPrompt_EntryExceedsRemaining covers lines 625-626:
// the `if remaining <= 0 { break }` inside the loop.
// Strategy: use a block with a huge title (not clamped by bodyBudget) so that
// after writing it, remaining goes negative and the next iteration's guard fires.
func TestFormatSelectiveContextPrompt_EntryExceedsRemaining(t *testing.T) {
	// Block with a massive title — title is not budget-clamped.
	// After writing this block, remaining goes negative.
	// The second block's iteration hits `if remaining <= 0 { break }` at line 625.
	hugeTitle := strings.Repeat("T", 10000)
	blocks := []contextBlock{
		{Title: hugeTitle, Body: "x", Weight: 0.9, Score: 1.0},
		{Title: "Second", Body: "should not appear", Weight: 0.5, Score: 0.5},
	}
	got := formatSelectiveContextPrompt(blocks)
	if strings.Contains(got, "Second") {
		t.Error("second block should be cut off by inner remaining<=0 break")
	}
}

// TestSearchHistoryWithResiduals_ScopeFiltersOtherSessions inserts summaries,
// learnings, and validations in session-A, then searches with scope="session"
// from session-B. The scope filter must `continue` (skip) those records.
// Also covers the low-score `continue` for summaries/learnings/validations
// when content does not match the query.
func TestSearchHistoryWithResiduals_ScopeFiltersOtherSessions(t *testing.T) {
	projectDir := setupHistoryTestProject(t)
	sessA := "scope-filter-a"
	sessB := "scope-filter-b"
	for _, s := range []string{sessA, sessB} {
		if err := db.CreateSession(s, projectDir, "main"); err != nil {
			t.Fatalf("create session %s: %v", s, err)
		}
	}
	// Insert a summary for sessA — scope=session from sessB should skip it.
	if err := db.CreateSession(sessA+"tmp", projectDir, "main"); err != nil {
		t.Fatalf("create tmp session: %v", err)
	}
	// Insert a message for sessA — current-session scope from sessB should skip it.
	if err := db.SaveMessage("msg-scope-a", sessA, "user", "alpha beta gamma scope filter content", nil); err != nil {
		t.Fatalf("save message for sessA: %v", err)
	}
	if err := db.UpdateSessionSummary(sessA, "summary text scope filter alpha beta"); err != nil {
		t.Fatalf("save summary: %v", err)
	}
	// Insert a learning for sessA — scope=session from sessB should skip it.
	if err := db.SaveLearningEvent("learn-scope", sessA, "pattern", "outcome", 0.5, "category", "context"); err != nil {
		t.Fatalf("save learning: %v", err)
	}
	// Insert a validation for sessA — scope=session from sessB should skip it.
	if err := db.SaveValidationResult("vr-scope", sessA, "fix login", false, false, 0, 0, 100, "evidence", "go test"); err != nil {
		t.Fatalf("save validation: %v", err)
	}
	// Search from sessB with scope="current-session" — all three should be skipped.
	hits, err := SearchHistoryWithResiduals(projectDir, sessB, "alpha beta gamma", nil, "current-session", 10, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(hits) != 0 {
		t.Errorf("expected 0 hits under session scope, got %d", len(hits))
	}
	// Now insert records in sessA with content that won't score above threshold
	// when searching from sessB with scope=project (no session bonus for sessA records).
	// Learning base score 1.9 + 0 confidence + 0 text match = 1.9 → continue.
	// Validation base score 1.7 + 0 text match = 1.7 → continue.
	if err := db.SaveLearningEvent("learn-low", sessA, "xyzzy", "grault", 0.0, "cat", "waldo"); err != nil {
		t.Fatalf("save low-score learning: %v", err)
	}
	if err := db.SaveValidationResult("vr-low", sessA, "xyzzy", false, false, 0, 0, 100, "grault", "waldo"); err != nil {
		t.Fatalf("save low-score validation: %v", err)
	}
	// Query from sessB with project scope — sessA records get no session bonus,
	// content doesn't match "zzzunrelated", scores stay at thresholds → continue.
	hits2, err := SearchHistoryWithResiduals(projectDir, sessB, "zzzunrelated", nil, "project", 10, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_ = hits2
}