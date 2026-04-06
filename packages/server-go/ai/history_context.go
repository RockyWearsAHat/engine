package ai

import (
	"fmt"
	"math"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/engine/server/db"
)

const (
	conversationWindowSize          = 14
	conversationAnchorMessageCount  = 6
	projectHistoryMessageLimit      = 240
	projectHistorySummaryLimit      = 24
	projectHistoryLearningLimit     = 36
	projectHistoryValidationLimit   = 24
	projectAttentionResidualLimit   = 160
	selectiveContextCharBudget      = 3200
	defaultHistorySearchResultLimit = 5
	maxHistorySearchResultLimit     = 10
	contextBlockResidualBoost       = 1.4
	historyResidualBoost            = 2.2
	conversationResidualBoost       = 2.6
	conversationQueryBoost          = 1.4
)

var historySearchStopWords = map[string]struct{}{
	"a": {}, "an": {}, "and": {}, "are": {}, "as": {}, "at": {}, "be": {}, "by": {},
	"do": {}, "for": {}, "from": {}, "get": {}, "has": {}, "have": {}, "how": {}, "i": {},
	"in": {}, "into": {}, "is": {}, "it": {}, "me": {}, "my": {}, "of": {}, "on": {}, "or": {},
	"our": {}, "please": {}, "so": {}, "that": {}, "the": {}, "their": {}, "them": {}, "this": {},
	"to": {}, "use": {}, "want": {}, "we": {}, "what": {}, "when": {}, "why": {}, "with": {}, "you": {},
}

type contextBlock struct {
	Key    string
	Title  string
	Body   string
	Score  float64
	Weight float64
}

type selectiveContextResult struct {
	Prompt      string
	Blocks      []contextBlock
	HistoryHits []historySearchHit
}

type conversationWindowSelection struct {
	MessageID      string
	Role           string
	Content        string
	CreatedAt      string
	Score          float64
	Weight         float64
	ResidualWeight float64
	QueryScore     float64
	Recency        float64
	Order          int
}

type conversationWindowResult struct {
	Messages   []anthropicMessage
	Selections []conversationWindowSelection
}

type historySearchHit struct {
	ID             string
	SourceKey      string
	Source         string
	SessionID      string
	BranchName     string
	Role           string
	Text           string
	CreatedAt      string
	Score          float64
	Weight         float64
	ResidualWeight float64
}

func BuildConversationWindow(history []db.Message, userMessage string) []anthropicMessage {
	return BuildAttentionConversationWindow(history, userMessage, nil, nil).Messages
}

func BuildAttentionConversationWindow(
	history []db.Message,
	userMessage string,
	openTabs []TabInfo,
	residualProfile map[string]float64,
) conversationWindowResult {
	if len(history) == 0 {
		return conversationWindowResult{
			Messages: []anthropicMessage{{Role: "user", Content: userMessage}},
		}
	}

	prior := history[:len(history)-1]
	if len(prior) == 0 {
		return conversationWindowResult{
			Messages: []anthropicMessage{{Role: "user", Content: userMessage}},
		}
	}

	queryTerms := extractSearchTerms(userMessage, flattenTabPaths(openTabs))
	buildSelection := func(index int, isAnchor bool) conversationWindowSelection {
		message := prior[index]
		residualWeight := residualProfile[attentionSourceKey("message", message.ID)]
		queryScore := scoreHistoryCandidate(message.Content, userMessage, queryTerms, openTabs)
		recency := recencyScore(message.CreatedAt)
		score := recency + queryScore*conversationQueryBoost + residualWeight*conversationResidualBoost
		if isAnchor {
			score += 1.35
		}
		return conversationWindowSelection{
			MessageID:      message.ID,
			Role:           message.Role,
			Content:        message.Content,
			CreatedAt:      message.CreatedAt,
			Score:          score,
			ResidualWeight: residualWeight,
			QueryScore:     queryScore,
			Recency:        recency,
			Order:          index,
		}
	}

	selected := make([]conversationWindowSelection, 0, minInt(len(prior), conversationWindowSize))
	if len(prior) <= conversationWindowSize {
		anchorStart := maxInt(len(prior)-conversationAnchorMessageCount, 0)
		for index := range prior {
			selected = append(selected, buildSelection(index, index >= anchorStart))
		}
	} else {
		anchorCount := minInt(conversationAnchorMessageCount, conversationWindowSize)
		anchorStart := len(prior) - anchorCount
		selectedIndexes := map[int]struct{}{}
		for index := anchorStart; index < len(prior); index++ {
			selectedIndexes[index] = struct{}{}
		}

		candidates := make([]conversationWindowSelection, 0, anchorStart)
		for index := 0; index < anchorStart; index++ {
			candidates = append(candidates, buildSelection(index, false))
		}
		sort.SliceStable(candidates, func(i, j int) bool {
			if candidates[i].Score == candidates[j].Score {
				return candidates[i].Order > candidates[j].Order
			}
			return candidates[i].Score > candidates[j].Score
		})

		remaining := conversationWindowSize - anchorCount
		for _, candidate := range candidates {
			if remaining == 0 {
				break
			}
			selectedIndexes[candidate.Order] = struct{}{}
			remaining--
		}

		for index := range prior {
			if _, ok := selectedIndexes[index]; !ok {
				continue
			}
			selected = append(selected, buildSelection(index, index >= anchorStart))
		}
	}

	weights := normalizeScoreWeights(selectionScores(selected))
	messages := make([]anthropicMessage, 0, len(selected)+1)
	for index, selection := range selected {
		selection.Weight = weights[index]
		selected[index] = selection
		messages = append(messages, anthropicMessage{
			Role:    selection.Role,
			Content: selection.Content,
		})
	}
	messages = append(messages, anthropicMessage{Role: "user", Content: userMessage})

	return conversationWindowResult{
		Messages:   messages,
		Selections: selected,
	}
}

func BuildSelectiveContextPrompt(
	projectPath string,
	session *db.Session,
	userMessage string,
	openTabs []TabInfo,
) string {
	return BuildSelectiveContext(projectPath, session, userMessage, openTabs, nil).Prompt
}

func BuildSelectiveContext(
	projectPath string,
	session *db.Session,
	userMessage string,
	openTabs []TabInfo,
	residualProfile map[string]float64,
) selectiveContextResult {
	queryTerms := extractSearchTerms(userMessage, flattenTabPaths(openTabs))
	blocks := make([]contextBlock, 0, 4)

	if focus := buildCurrentFocusContext(openTabs); focus != "" {
		blocks = append(blocks, contextBlock{
			Key:   attentionSourceKey("block", "focus"),
			Title: "Current workspace focus",
			Body:  focus,
			Score: 3.8 + 0.25*scoreTermCoverage(focus, queryTerms) + residualProfile[attentionSourceKey("block", "focus")]*contextBlockResidualBoost,
		})
	}

	if workspaceGuide := BuildWorkspacePromptContext(projectPath); workspaceGuide != "" {
		blocks = append(blocks, contextBlock{
			Key:   attentionSourceKey("block", "workspace-direction"),
			Title: "Workspace direction",
			Body:  workspaceGuide,
			Score: 3.2 + 0.3*scoreTermCoverage(workspaceGuide, queryTerms) + residualProfile[attentionSourceKey("block", "workspace-direction")]*contextBlockResidualBoost,
		})
	}

	if session != nil && strings.TrimSpace(session.Summary) != "" {
		blocks = append(blocks, contextBlock{
			Key:   attentionSourceKey("block", "session-memory"),
			Title: "Session memory",
			Body:  session.Summary,
			Score: 2.8 + 0.35*scoreTermCoverage(session.Summary, queryTerms) + residualProfile[attentionSourceKey("block", "session-memory")]*contextBlockResidualBoost,
		})
	}

	historyHits, err := SearchHistoryWithResiduals(projectPath, currentSessionID(session), userMessage, openTabs, "project", 6, residualProfile)
	if err == nil && len(historyHits) > 0 {
		blocks = append(blocks, contextBlock{
			Key:   attentionSourceKey("block", "retrieved-history"),
			Title: "Retrieved history",
			Body:  formatHistoryHits(historyHits, currentSessionID(session), 1500),
			Score: 2.6 + historyHits[0].Score + residualProfile[attentionSourceKey("block", "retrieved-history")]*contextBlockResidualBoost,
		})
	}

	if len(blocks) == 0 {
		return selectiveContextResult{}
	}

	weighted := applySoftmaxWeights(blocks)
	sort.SliceStable(weighted, func(i, j int) bool {
		return weighted[i].Weight > weighted[j].Weight
	})

	result := selectiveContextResult{
		Prompt:      formatSelectiveContextPrompt(weighted),
		Blocks:      weighted,
		HistoryHits: historyHits,
	}
	return result
}

func SearchHistory(
	projectPath string,
	currentSessionID string,
	query string,
	openTabs []TabInfo,
	scope string,
	limit int,
) ([]historySearchHit, error) {
	return SearchHistoryWithResiduals(projectPath, currentSessionID, query, openTabs, scope, limit, nil)
}

func SearchHistoryWithResiduals(
	projectPath string,
	currentSessionID string,
	query string,
	openTabs []TabInfo,
	scope string,
	limit int,
	residualProfile map[string]float64,
) ([]historySearchHit, error) {
	if limit <= 0 {
		limit = defaultHistorySearchResultLimit
	}
	if limit > maxHistorySearchResultLimit {
		limit = maxHistorySearchResultLimit
	}

	queryTerms := extractSearchTerms(query, flattenTabPaths(openTabs))
	projectMessages, err := db.GetProjectMessages(projectPath, projectHistoryMessageLimit)
	if err != nil {
		return nil, err
	}
	projectSummaries, err := db.GetProjectSessionSummaries(projectPath, projectHistorySummaryLimit)
	if err != nil {
		return nil, err
	}
	projectLearnings, err := db.GetProjectLearnings(projectPath, projectHistoryLearningLimit)
	if err != nil {
		return nil, err
	}
	projectValidations, err := db.GetProjectValidations(projectPath, projectHistoryValidationLimit)
	if err != nil {
		return nil, err
	}

	scope = strings.TrimSpace(strings.ToLower(scope))
	if scope == "" {
		scope = "project"
	}

	hits := make([]historySearchHit, 0, limit*3)
	for _, message := range projectMessages {
		if !historyScopeAllows(scope, currentSessionID, message.SessionID) {
			continue
		}
		sourceKey := attentionSourceKey("message", message.ID)
		residualWeight := residualProfile[sourceKey]
		score := 1.2 + scoreHistoryCandidate(message.Content, query, queryTerms, openTabs) + residualWeight*historyResidualBoost
		if message.SessionID == currentSessionID {
			score += 0.6
		}
		if score <= 1.2 {
			continue
		}
		hits = append(hits, historySearchHit{
			ID:             message.ID,
			SourceKey:      sourceKey,
			Source:         "message",
			SessionID:      message.SessionID,
			BranchName:     message.BranchName,
			Role:           message.Role,
			Text:           message.Content,
			CreatedAt:      message.CreatedAt,
			Score:          score + recencyScore(message.CreatedAt),
			ResidualWeight: residualWeight,
		})
	}

	for _, summary := range projectSummaries {
		if !historyScopeAllows(scope, currentSessionID, summary.SessionID) {
			continue
		}
		sourceKey := attentionSourceKey("summary", summary.SessionID)
		residualWeight := residualProfile[sourceKey]
		score := 1.5 + scoreHistoryCandidate(summary.Summary, query, queryTerms, openTabs) + residualWeight*historyResidualBoost
		if summary.SessionID == currentSessionID {
			score += 0.45
		}
		if score <= 1.5 {
			continue
		}
		hits = append(hits, historySearchHit{
			ID:             summary.SessionID,
			SourceKey:      sourceKey,
			Source:         "summary",
			SessionID:      summary.SessionID,
			BranchName:     summary.BranchName,
			Text:           summary.Summary,
			CreatedAt:      summary.UpdatedAt,
			Score:          score + recencyScore(summary.UpdatedAt),
			ResidualWeight: residualWeight,
		})
	}

	for _, learning := range projectLearnings {
		if !historyScopeAllows(scope, currentSessionID, learning.SessionID) {
			continue
		}
		text := strings.TrimSpace(strings.Join([]string{
			learning.Category,
			learning.Pattern,
			learning.Outcome,
			learning.Context,
		}, " "))
		sourceKey := attentionSourceKey("learning", learning.ID)
		residualWeight := residualProfile[sourceKey]
		score := 1.9 + scoreHistoryCandidate(text, query, queryTerms, openTabs) + learning.Confidence + residualWeight*historyResidualBoost
		if learning.SessionID == currentSessionID {
			score += 0.35
		}
		if score <= 1.9 {
			continue
		}
		hits = append(hits, historySearchHit{
			ID:             learning.ID,
			SourceKey:      sourceKey,
			Source:         "learning",
			SessionID:      learning.SessionID,
			BranchName:     learning.BranchName,
			Text:           fmt.Sprintf("[%s] %s -> %s", learning.Category, learning.Pattern, learning.Outcome),
			CreatedAt:      learning.CreatedAt,
			Score:          score + recencyScore(learning.CreatedAt),
			ResidualWeight: residualWeight,
		})
	}

	for _, validation := range projectValidations {
		if !historyScopeAllows(scope, currentSessionID, validation.SessionID) {
			continue
		}
		status := "failed"
		if validation.IssueResolved || validation.TestPassed {
			status = "passed"
		}
		text := strings.TrimSpace(strings.Join([]string{
			validation.Issue,
			validation.Command,
			validation.Evidence,
			status,
		}, " "))
		sourceKey := attentionSourceKey("validation", validation.ID)
		residualWeight := residualProfile[sourceKey]
		score := 1.7 + scoreHistoryCandidate(text, query, queryTerms, openTabs) + residualWeight*historyResidualBoost
		if validation.IssueResolved {
			score += 0.35
		}
		if validation.TestPassed {
			score += 0.2
		}
		if validation.SessionID == currentSessionID {
			score += 0.25
		}
		if score <= 1.7 {
			continue
		}
		hits = append(hits, historySearchHit{
			ID:             validation.ID,
			SourceKey:      sourceKey,
			Source:         "validation",
			SessionID:      validation.SessionID,
			BranchName:     validation.BranchName,
			Text:           fmt.Sprintf("%s | command: %s | evidence: %s", validation.Issue, validation.Command, validation.Evidence),
			CreatedAt:      validation.CreatedAt,
			Score:          score + recencyScore(validation.CreatedAt),
			ResidualWeight: residualWeight,
		})
	}

	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].Score == hits[j].Score {
			return hits[i].CreatedAt > hits[j].CreatedAt
		}
		return hits[i].Score > hits[j].Score
	})

	deduped := make([]historySearchHit, 0, limit)
	seen := map[string]struct{}{}
	for _, hit := range hits {
		key := hit.Source + "|" + hit.SessionID + "|" + truncateSummary(normalizeSummaryText(hit.Text), 120)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		deduped = append(deduped, hit)
		if len(deduped) == limit {
			break
		}
	}

	weights := normalizeScoreWeights(hitScores(deduped))
	for index := range deduped {
		deduped[index].Weight = weights[index]
	}

	return deduped, nil
}

func FormatHistorySearchResults(query string, hits []historySearchHit, currentSessionID string) string {
	if len(hits) == 0 {
		return fmt.Sprintf("No stored history matched %q.", query)
	}

	var builder strings.Builder
	builder.WriteString(fmt.Sprintf("History search results for %q:\n", query))
	for index, hit := range hits {
		scope := "project"
		if hit.SessionID == currentSessionID {
			scope = "current-session"
		}
		role := hit.Role
		if role == "" {
			role = hit.Source
		}
		builder.WriteString(fmt.Sprintf(
			"%d. [%.2f | %s | %s | %s] %s\n",
			index+1,
			hit.Score,
			hit.Source,
			scope,
			role,
			truncateSummary(normalizeSummaryText(hit.Text), 240),
		))
	}
	return strings.TrimSpace(builder.String())
}

func BuildAttentionResidualProfile(
	projectPath string,
	currentSessionID string,
	query string,
	openTabs []TabInfo,
) (map[string]float64, error) {
	residuals, err := db.GetProjectAttentionResiduals(projectPath, projectAttentionResidualLimit)
	if err != nil {
		return nil, err
	}
	return buildAttentionResidualProfile(residuals, currentSessionID, query, openTabs), nil
}

func BuildAttentionResidualRecords(
	sessionID string,
	messageID string,
	userMessage string,
	window conversationWindowResult,
	contextResult selectiveContextResult,
) []db.AttentionResidual {
	queryText := truncateSummary(normalizeSummaryText(userMessage), 280)
	historyLimit := minInt(len(contextResult.HistoryHits), 4)
	residuals := make([]db.AttentionResidual, 0, len(window.Selections)+len(contextResult.Blocks)+historyLimit)

	for _, selection := range window.Selections {
		if selection.Weight <= 0 {
			continue
		}
		residuals = append(residuals, db.AttentionResidual{
			ID:          newID(),
			SessionID:   sessionID,
			MessageID:   messageID,
			SourceKey:   attentionSourceKey("message", selection.MessageID),
			SourceType:  "conversation_message",
			SourceLabel: selection.Role,
			QueryText:   queryText,
			Weight:      selection.Weight,
			Score:       selection.Score,
			Context:     truncateSummary(normalizeSummaryText(selection.Content), 220),
		})
	}

	for _, block := range contextResult.Blocks {
		if block.Weight <= 0 {
			continue
		}
		residuals = append(residuals, db.AttentionResidual{
			ID:          newID(),
			SessionID:   sessionID,
			MessageID:   messageID,
			SourceKey:   block.Key,
			SourceType:  "context_block",
			SourceLabel: block.Title,
			QueryText:   queryText,
			Weight:      block.Weight,
			Score:       block.Score,
			Context:     truncateSummary(normalizeSummaryText(block.Body), 220),
		})
	}

	for index := 0; index < historyLimit; index++ {
		hit := contextResult.HistoryHits[index]
		if hit.Weight <= 0 {
			continue
		}
		residuals = append(residuals, db.AttentionResidual{
			ID:          newID(),
			SessionID:   sessionID,
			MessageID:   messageID,
			SourceKey:   hit.SourceKey,
			SourceType:  "history_hit",
			SourceLabel: hit.Source,
			QueryText:   queryText,
			Weight:      hit.Weight,
			Score:       hit.Score,
			Context:     truncateSummary(normalizeSummaryText(hit.Text), 220),
		})
	}

	return residuals
}

func buildAttentionResidualProfile(
	residuals []db.AttentionResidual,
	currentSessionID string,
	query string,
	openTabs []TabInfo,
) map[string]float64 {
	queryTerms := extractSearchTerms(query, flattenTabPaths(openTabs))
	scoreByKey := map[string]float64{}
	maxScore := 0.0

	for _, residual := range residuals {
		if strings.TrimSpace(residual.SourceKey) == "" {
			continue
		}

		score := residual.Weight + math.Min(residual.Score/6.0, 1.2) + recencyScore(residual.CreatedAt)
		if residual.SessionID == currentSessionID {
			score += 0.45
		}
		if coverage := scoreTermCoverage(strings.Join([]string{
			residual.QueryText,
			residual.SourceLabel,
			residual.Context,
		}, " "), queryTerms); coverage > 0 {
			score += coverage * 0.9
		}
		if score <= 0 {
			continue
		}

		scoreByKey[residual.SourceKey] += score
		if scoreByKey[residual.SourceKey] > maxScore {
			maxScore = scoreByKey[residual.SourceKey]
		}
	}

	if maxScore == 0 {
		return map[string]float64{}
	}

	profile := make(map[string]float64, len(scoreByKey))
	for key, score := range scoreByKey {
		profile[key] = math.Min(score/maxScore, 1)
	}
	return profile
}

func formatSelectiveContextPrompt(blocks []contextBlock) string {
	if len(blocks) == 0 {
		return ""
	}

	var builder strings.Builder
	builder.WriteString("Selective context blocks for this request (higher weight means more relevant right now):")
	remaining := selectiveContextCharBudget - builder.Len()

	for _, block := range blocks {
		if remaining <= 0 {
			break
		}

		bodyBudget := minInt(maxInt(remaining-32, 0), len(block.Body))
		if bodyBudget == 0 {
			break
		}
		body := truncateSummary(block.Body, bodyBudget)
		if body == "" {
			continue
		}

		entry := fmt.Sprintf("\n[%.2f] %s\n%s", block.Weight, block.Title, body)
		if len(entry) > remaining {
			entryBodyBudget := maxInt(remaining-24, 0)
			if entryBodyBudget == 0 {
				break
			}
			entry = fmt.Sprintf("\n[%.2f] %s\n%s", block.Weight, block.Title, truncateSummary(body, entryBodyBudget))
		}
		if strings.TrimSpace(entry) == "" {
			continue
		}
		builder.WriteString(entry)
		remaining = selectiveContextCharBudget - builder.Len()
	}

	return builder.String()
}

func flattenTabPaths(openTabs []TabInfo) string {
	parts := make([]string, 0, len(openTabs))
	for _, tab := range openTabs {
		if strings.TrimSpace(tab.Path) == "" {
			continue
		}
		parts = append(parts, tab.Path)
	}
	return strings.Join(parts, " ")
}

func attentionSourceKey(sourceType string, id string) string {
	sourceType = strings.TrimSpace(sourceType)
	id = strings.TrimSpace(id)
	if id == "" {
		return sourceType
	}
	return sourceType + ":" + id
}

func currentSessionID(session *db.Session) string {
	if session == nil {
		return ""
	}
	return session.ID
}

func buildCurrentFocusContext(openTabs []TabInfo) string {
	if len(openTabs) == 0 {
		return ""
	}

	activeParts := make([]string, 0, 1)
	otherParts := make([]string, 0, len(openTabs))
	for _, tab := range openTabs {
		label := filepath.Base(tab.Path)
		if label == "." || label == "/" || label == "" {
			label = tab.Path
		}
		if tab.IsDirty {
			label += " (dirty)"
		}
		if tab.IsActive {
			activeParts = append(activeParts, fmt.Sprintf("Active tab: %s", tab.Path))
			continue
		}
		otherParts = append(otherParts, label)
	}

	sections := make([]string, 0, 2)
	if len(activeParts) > 0 {
		sections = append(sections, strings.Join(activeParts, "; "))
	}
	if len(otherParts) > 0 {
		sections = append(sections, "Other open tabs: "+strings.Join(otherParts[:minInt(len(otherParts), 6)], ", "))
	}
	return strings.Join(sections, "\n")
}

func applySoftmaxWeights(blocks []contextBlock) []contextBlock {
	if len(blocks) == 0 {
		return blocks
	}
	weighted := make([]contextBlock, len(blocks))
	weights := normalizeScoreWeights(blockScores(blocks))
	for index := range weighted {
		weighted[index] = blocks[index]
		weighted[index].Weight = weights[index]
	}
	return weighted
}

func blockScores(blocks []contextBlock) []float64 {
	scores := make([]float64, len(blocks))
	for index, block := range blocks {
		scores[index] = block.Score
	}
	return scores
}

func selectionScores(selections []conversationWindowSelection) []float64 {
	scores := make([]float64, len(selections))
	for index, selection := range selections {
		scores[index] = selection.Score
	}
	return scores
}

func hitScores(hits []historySearchHit) []float64 {
	scores := make([]float64, len(hits))
	for index, hit := range hits {
		scores[index] = hit.Score
	}
	return scores
}

func normalizeScoreWeights(scores []float64) []float64 {
	if len(scores) == 0 {
		return []float64{}
	}

	maxScore := scores[0]
	for _, score := range scores[1:] {
		if score > maxScore {
			maxScore = score
		}
	}

	weights := make([]float64, len(scores))
	total := 0.0
	for index, score := range scores {
		weight := math.Exp(score - maxScore)
		weights[index] = weight
		total += weight
	}
	if total == 0 {
		return weights
	}
	for index := range weights {
		weights[index] = weights[index] / total
	}
	return weights
}

func formatHistoryHits(hits []historySearchHit, currentSessionID string, maxChars int) string {
	var builder strings.Builder
	for _, hit := range hits {
		scope := "project"
		if hit.SessionID == currentSessionID {
			scope = "current-session"
		}
		role := hit.Role
		if role == "" {
			role = hit.Source
		}
		line := fmt.Sprintf(
			"- [%.2f | %s | %s | %s] %s\n",
			hit.Score,
			hit.Source,
			scope,
			role,
			truncateSummary(normalizeSummaryText(hit.Text), 220),
		)
		if builder.Len()+len(line) > maxChars {
			break
		}
		builder.WriteString(line)
	}
	return strings.TrimSpace(builder.String())
}

func extractSearchTerms(inputs ...string) []string {
	seen := map[string]struct{}{}
	terms := make([]string, 0, 24)

	for _, input := range inputs {
		for _, raw := range strings.FieldsFunc(strings.ToLower(input), func(r rune) bool {
			return !unicode.IsLetter(r) && !unicode.IsNumber(r) && r != '.' && r != '_' && r != '-'
		}) {
			term := strings.Trim(raw, "./-_")
			if term == "" {
				continue
			}
			if _, blocked := historySearchStopWords[term]; blocked {
				continue
			}
			if len(term) < 3 && !strings.ContainsAny(term, "0123456789") {
				continue
			}
			if _, ok := seen[term]; ok {
				continue
			}
			seen[term] = struct{}{}
			terms = append(terms, term)
		}
	}

	return terms
}

func scoreHistoryCandidate(text string, query string, queryTerms []string, openTabs []TabInfo) float64 {
	lowerText := strings.ToLower(text)
	score := 0.0

	normalizedQuery := strings.ToLower(strings.TrimSpace(query))
	if len(normalizedQuery) >= 8 && strings.Contains(lowerText, normalizedQuery) {
		score += 2.8
	}

	for _, term := range queryTerms {
		if strings.Contains(lowerText, term) {
			score += 1.0 + math.Min(float64(len(term))/10.0, 0.7)
		}
	}

	for _, tab := range openTabs {
		path := strings.ToLower(tab.Path)
		if path == "" {
			continue
		}
		base := strings.ToLower(filepath.Base(path))
		if base != "" && strings.Contains(lowerText, base) {
			score += 0.8
		}
		if strings.Contains(lowerText, path) {
			score += 1.1
		}
	}

	return score
}

func scoreTermCoverage(text string, terms []string) float64 {
	if len(terms) == 0 {
		return 0
	}

	lowerText := strings.ToLower(text)
	matches := 0
	for _, term := range terms {
		if strings.Contains(lowerText, term) {
			matches++
		}
	}
	return float64(matches) / float64(len(terms))
}

func recencyScore(timestamp string) float64 {
	parsedTime, err := time.Parse(time.RFC3339, timestamp)
	if err != nil {
		return 0
	}

	ageHours := time.Since(parsedTime).Hours()
	switch {
	case ageHours <= 6:
		return 0.6
	case ageHours <= 24:
		return 0.4
	case ageHours <= 24*7:
		return 0.2
	default:
		return 0.05
	}
}

func historyScopeAllows(scope string, currentSessionID string, candidateSessionID string) bool {
	if scope != "current-session" {
		return true
	}
	return currentSessionID != "" && candidateSessionID == currentSessionID
}

func minInt(a int, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a int, b int) int {
	if a > b {
		return a
	}
	return b
}
