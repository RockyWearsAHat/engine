package ai

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/engine/server/db"
)

const (
	residualWeightSurprise   = 0.30
	residualWeightNovelty    = 0.18
	residualWeightVerify     = 0.22
	residualWeightDependency = 0.20
	residualWeightRecency    = 0.10
	residualRecencyLambda    = 0.003
)

// MemoryContextPack is the deterministic prompt-context result built from Memory OS state.
type MemoryContextPack struct {
	Goal               []string `json:"goal"`
	Constraints        []string `json:"constraints"`
	Blockers           []string `json:"blockers"`
	VerifiedFacts      []string `json:"verifiedFacts"`
	PriorFailedAttempt []string `json:"priorFailedAttempt"`
	PendingValidation  []string `json:"pendingValidation"`
}

func ingestMemoryEvent(projectPath, sessionID, eventType, actorModel string, payload map[string]any) (*db.MemoryLedgerEvent, error) {
	event, err := db.AppendMemoryLedgerEvent(projectPath, sessionID, eventType, actorModel, payload)
	if err != nil {
		return nil, err
	}
	updateMemoryResidualGraph(projectPath, sessionID, event, payload)
	return event, nil
}

func updateMemoryResidualGraph(projectPath, sessionID string, event *db.MemoryLedgerEvent, payload map[string]any) {
	if event == nil {
		return
	}

	terms := extractSearchTerms(strings.TrimSpace(fieldString(payload, "text")))
	label := summarizeEventLabel(event.EventType, payload)
	nodeType, verificationStatus := classifyEventNodeType(event.EventType, payload)
	dependencyCentrality := 0.45
	if len(terms) >= 4 {
		dependencyCentrality = 0.7
	}

	surprise := clamp01(fieldFloat(payload, "surprise"))
	if surprise == 0 && fieldBool(payload, "isError") {
		surprise = 0.85
	}
	novelty := clamp01(float64(len(terms)) / 8.0)
	verify := 0.2
	if verificationStatus == "verified" {
		verify = 0.9
	}
	confidence := 0.65
	if fieldBool(payload, "isError") {
		confidence = 0.9
	}

	score := compositeResidualScore(surprise, novelty, verify, dependencyCentrality, time.Now())
	nodeKey := buildNodeKey(event.EventType, payload)

	node := db.MemoryResidualNode{
		NodeKey:              nodeKey,
		ProjectPath:          projectPath,
		SessionID:            sessionID,
		NodeType:             nodeType,
		Label:                label,
		VerificationStatus:   verificationStatus,
		Confidence:           confidence,
		Novelty:              novelty,
		Surprise:             surprise,
		VerificationStrength: verify,
		DependencyCentrality: dependencyCentrality,
		ResidualScore:        score,
		LastEventSequence:    event.Sequence,
	}
	_ = db.UpsertMemoryResidualNode(node)

	if parent := strings.TrimSpace(fieldString(payload, "parentNode")); parent != "" {
		_ = db.UpsertMemoryResidualEdge(db.MemoryResidualEdge{
			ProjectPath:       projectPath,
			FromNodeKey:       parent,
			ToNodeKey:         nodeKey,
			EdgeType:          "depends_on",
			Weight:            0.8,
			Unresolved:        verificationStatus != "verified",
			LastEventSequence: event.Sequence,
		})
	}
}

func compositeResidualScore(surprise, novelty, verify, dependency float64, nowTime time.Time) float64 {
	ageSeconds := 0.0
	recency := math.Exp(-residualRecencyLambda * ageSeconds)
	return (residualWeightSurprise * surprise) +
		(residualWeightNovelty * novelty) +
		(residualWeightVerify * verify) +
		(residualWeightDependency * dependency) +
		(residualWeightRecency * recency)
}

func buildNodeKey(eventType string, payload map[string]any) string {
	id := strings.TrimSpace(fieldString(payload, "id"))
	if id == "" {
		id = strings.TrimSpace(fieldString(payload, "tool"))
	}
	if id == "" {
		id = strings.TrimSpace(fieldString(payload, "text"))
	}
	if id == "" {
		id = strings.TrimSpace(fieldString(payload, "name"))
	}
	if id == "" {
		id = eventType
	}
	id = strings.ToLower(strings.ReplaceAll(id, " ", "_"))
	if len(id) > 96 {
		id = id[:96]
	}
	return eventType + ":" + id
}

func summarizeEventLabel(eventType string, payload map[string]any) string {
	if v := strings.TrimSpace(fieldString(payload, "label")); v != "" {
		return v
	}
	if v := strings.TrimSpace(fieldString(payload, "text")); v != "" {
		return truncateSummary(normalizeSummaryText(v), 180)
	}
	if v := strings.TrimSpace(fieldString(payload, "tool")); v != "" {
		if fieldBool(payload, "isError") {
			return fmt.Sprintf("Tool failed: %s", v)
		}
		return fmt.Sprintf("Tool ran: %s", v)
	}
	return eventType
}

func classifyEventNodeType(eventType string, payload map[string]any) (string, string) {
	switch eventType {
	case "user_message":
		return "task", "unverified"
	case "assistant_message":
		return "decision", "unverified"
	case "tool_call_result":
		if fieldBool(payload, "isError") {
			return "outcome", "failed"
		}
		return "outcome", "verified"
	case "validation_result":
		if fieldBool(payload, "passed") {
			return "validation", "verified"
		}
		return "validation", "failed"
	default:
		return "claim", "unverified"
	}
}

func buildDeterministicMemoryContext(projectPath, sessionID, userQuery string, openTabs []TabInfo, budget int) string {
	nodes, _ := db.ListTopMemoryResidualNodes(projectPath, sessionID, 30)
	events, _ := db.GetMemoryLedgerEvents(projectPath, 0, 100)

	pack := MemoryContextPack{}
	for _, node := range nodes {
		if node.Superseded {
			continue
		}
		entry := fmt.Sprintf("[%.2f] %s", node.ResidualScore, strings.TrimSpace(node.Label))
		switch node.NodeType {
		case "task":
			pack.Goal = append(pack.Goal, entry)
		case "outcome":
			if node.VerificationStatus == "verified" {
				pack.VerifiedFacts = append(pack.VerifiedFacts, entry)
			} else if node.VerificationStatus == "failed" {
				pack.PriorFailedAttempt = append(pack.PriorFailedAttempt, entry)
			} else {
				pack.PendingValidation = append(pack.PendingValidation, entry)
			}
		case "validation":
			if node.VerificationStatus == "verified" {
				pack.VerifiedFacts = append(pack.VerifiedFacts, entry)
			} else {
				pack.PendingValidation = append(pack.PendingValidation, entry)
			}
		case "decision":
			pack.Constraints = append(pack.Constraints, entry)
		default:
			if node.Contradicted {
				pack.Blockers = append(pack.Blockers, entry)
			}
		}
	}

	for index := len(events) - 1; index >= 0 && len(pack.Blockers) < 6; index-- {
		event := events[index]
		if event.EventType != "tool_call_result" {
			continue
		}
		var payload map[string]any
		if err := json.Unmarshal([]byte(event.PayloadJSON), &payload); err != nil {
			continue
		}
		if !fieldBool(payload, "isError") {
			continue
		}
		tool := strings.TrimSpace(fieldString(payload, "tool"))
		errMsg := truncateSummary(normalizeSummaryText(fieldString(payload, "error")), 140)
		if tool == "" {
			tool = "unknown"
		}
		pack.Blockers = append(pack.Blockers, fmt.Sprintf("%s: %s", tool, errMsg))
	}

	tabs := flattenTabPaths(openTabs)
	if tabs != "" {
		pack.Constraints = append(pack.Constraints, "Active tabs: "+tabs)
	}
	if query := strings.TrimSpace(userQuery); query != "" {
		pack.Goal = append(pack.Goal, "Current user query: "+truncateSummary(normalizeSummaryText(query), 180))
	}

	stableSortStrings(pack.Goal)
	stableSortStrings(pack.Constraints)
	stableSortStrings(pack.Blockers)
	stableSortStrings(pack.VerifiedFacts)
	stableSortStrings(pack.PriorFailedAttempt)
	stableSortStrings(pack.PendingValidation)

	return renderMemoryContextPack(pack, budget)
}

func renderMemoryContextPack(pack MemoryContextPack, budget int) string {
	if budget <= 0 {
		budget = 2200
	}
	sections := []struct {
		title string
		rows  []string
	}{
		{title: "Active Goals", rows: pack.Goal},
		{title: "Hard Constraints", rows: pack.Constraints},
		{title: "Unresolved Blockers", rows: pack.Blockers},
		{title: "Prior Failed Attempts", rows: pack.PriorFailedAttempt},
		{title: "Verified Facts", rows: pack.VerifiedFacts},
		{title: "Pending Validation", rows: pack.PendingValidation},
	}

	var builder strings.Builder
	builder.WriteString("Deterministic memory context (Memory OS):")
	for _, section := range sections {
		if len(section.rows) == 0 {
			continue
		}
		line := "\n- " + section.title + ":\n"
		if builder.Len()+len(line) > budget {
			break
		}
		builder.WriteString(line)
		for _, row := range section.rows {
			entry := "  * " + row + "\n"
			if builder.Len()+len(entry) > budget {
				break
			}
			builder.WriteString(entry)
		}
	}
	return strings.TrimSpace(builder.String())
}

func persistScribeSnapshot(projectPath, sessionID, actorModel, compiledContext string, eventSequence int64) {
	if strings.TrimSpace(projectPath) == "" || strings.TrimSpace(compiledContext) == "" {
		return
	}
	snapshotBody := map[string]any{
		"model":      strings.TrimSpace(actorModel),
		"sessionId":  strings.TrimSpace(sessionID),
		"compiled":   compiledContext,
		"updatedAt":  time.Now().UTC().Format(time.RFC3339),
		"eventOffset": eventSequence,
	}
	bodyJSON, _ := json.Marshal(snapshotBody)
	_ = db.SaveMemoryStateSnapshot(db.MemoryStateSnapshot{
		ProjectPath:   projectPath,
		SessionID:     sessionID,
		SnapshotType:  "scribe-shared",
		EventSequence: eventSequence,
		SnapshotJSON:  string(bodyJSON),
	})

	if strings.TrimSpace(actorModel) != "" {
		_ = db.SaveMemoryStateSnapshot(db.MemoryStateSnapshot{
			ProjectPath:   projectPath,
			SessionID:     sessionID,
			SnapshotType:  "scribe-model-" + sanitizeSnapshotToken(actorModel),
			EventSequence: eventSequence,
			SnapshotJSON:  string(bodyJSON),
		})
	}

	writeScribeMarkdown(projectPath, actorModel, compiledContext)
}

func writeScribeMarkdown(projectPath, actorModel, compiledContext string) {
	baseDir := filepath.Join(projectPath, ".engine", "memory", "scribe")
	if err := os.MkdirAll(filepath.Join(baseDir, "models"), 0o755); err != nil {
		return
	}
	stamp := time.Now().UTC().Format(time.RFC3339)
	shared := "# Shared Scribe Memory\n\nUpdated: " + stamp + "\n\n" + compiledContext + "\n"
	_ = os.WriteFile(filepath.Join(baseDir, "shared.md"), []byte(shared), 0o644)
	if strings.TrimSpace(actorModel) == "" {
		return
	}
	modelBody := "# Model Memory\n\nModel: " + actorModel + "\nUpdated: " + stamp + "\n\n" + compiledContext + "\n"
	_ = os.WriteFile(filepath.Join(baseDir, "models", sanitizeSnapshotToken(actorModel)+".md"), []byte(modelBody), 0o644)
}

func sanitizeSnapshotToken(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "" {
		return "unknown"
	}
	value = strings.ReplaceAll(value, " ", "-")
	value = strings.ReplaceAll(value, "/", "-")
	value = strings.ReplaceAll(value, "\\", "-")
	return value
}

func fieldString(payload map[string]any, key string) string {
	if payload == nil {
		return ""
	}
	value, ok := payload[key]
	if !ok || value == nil {
		return ""
	}
	switch v := value.(type) {
	case string:
		return v
	default:
		return fmt.Sprintf("%v", value)
	}
}

func fieldFloat(payload map[string]any, key string) float64 {
	if payload == nil {
		return 0
	}
	value, ok := payload[key]
	if !ok || value == nil {
		return 0
	}
	switch v := value.(type) {
	case float64:
		return v
	case float32:
		return float64(v)
	case int:
		return float64(v)
	case int64:
		return float64(v)
	default:
		return 0
	}
}

func fieldBool(payload map[string]any, key string) bool {
	if payload == nil {
		return false
	}
	value, ok := payload[key]
	if !ok || value == nil {
		return false
	}
	if v, ok := value.(bool); ok {
		return v
	}
	return false
}

func stableSortStrings(values []string) {
	sort.SliceStable(values, func(i, j int) bool {
		if values[i] == values[j] {
			return false
		}
		return values[i] < values[j]
	})
}

func clamp01(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 1 {
		return 1
	}
	return value
}
