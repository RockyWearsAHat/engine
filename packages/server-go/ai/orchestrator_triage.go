package ai

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/engine/server/db"
)

// OrchestratorRoute is the routing decision the orchestrator makes about an
// incoming brief before it commits to any work.
type OrchestratorRoute int

const (
	// RouteConversational answers the user directly in a single interactive
	// chat turn — no plan, no build pipeline.
	RouteConversational OrchestratorRoute = iota
	// RouteBuild drives the full autonomous plan → execute → review → validate
	// pipeline.
	RouteBuild
)

// autonomousForcePrefixes are explicit slash-style overrides. When a brief
// starts with any of these the orchestrator ALWAYS routes to a build, no
// reasoning call. Use these for the "I know what I want, just do it" command.
var autonomousForcePrefixes = []string{
	"/auto",
	"/autonomous",
	"/build",
	"/ship",
	"!auto",
	"!autonomous",
	"!build",
}

// autonomousForceExact catches very short single-token quick commands.
var autonomousForceExact = map[string]bool{
	"/auto":       true,
	"/autonomous": true,
	"/build":      true,
	"/ship":       true,
	"!auto":       true,
	"!autonomous": true,
	"!build":      true,
}

// orchestratorTriageFn is the routing decision seam. Tests override it to force
// a deterministic route without invoking the reasoning model.
var orchestratorTriageFn = decideOrchestratorRoute

// decideOrchestratorRoute is the orchestrator's routing brain. Routing is its
// first responsibility: reason about whether a brief is a conversational turn
// or a build directive.
//
// Order of authority:
//  1. Explicit slash/bang overrides (/build, /auto, !ship, …) always build.
//  2. Reasoning — a dedicated RoleRouter model call classifies the brief. When
//     it produces a confident BUILD/CHAT answer, that decision wins. This is
//     the authority in production.
//  3. Fallback — when no reasoning answer is available (no model, ambiguous or
//     empty output), default to RouteBuild, preserving the orchestrator's
//     historical "build it" behavior.
func decideOrchestratorRoute(cfg OrchestratorConfig, brief string, cancel <-chan struct{}) OrchestratorRoute {
	normalized := strings.ToLower(strings.TrimSpace(brief))
	if normalized == "" {
		return RouteBuild
	}
	trimmed := strings.Trim(normalized, " \t\n\r?!.")
	if autonomousForceExact[trimmed] {
		return RouteBuild
	}
	for _, p := range autonomousForcePrefixes {
		if strings.HasPrefix(normalized, p) {
			return RouteBuild
		}
	}
	if route, ok := triageReasoning(cfg, brief, cancel); ok {
		return route
	}
	return RouteBuild
}

// triageReasoning fires a lean RoleRouter classification call through the
// orchestrator's chat dispatch and parses the answer. It returns ok=false when
// the model produced no usable BUILD/CHAT signal so the caller can fall back.
//
// Routing through cfg.chatFnFor() (rather than the provider directly) means the
// production handler resolves the team/role model exactly as a normal chat
// would, while tests transparently substitute their ChatFn stub — no real
// network calls and no model-config coupling.
func triageReasoning(cfg OrchestratorConfig, brief string, cancel <-chan struct{}) (OrchestratorRoute, bool) {
	sessionID := fmt.Sprintf("%s-router-%d", chooseSessionPrefix(cfg), time.Now().UnixNano())
	_ = db.CreateSession(sessionID, cfg.ProjectPath, "")

	var (
		mu  sync.Mutex
		out strings.Builder
	)
	ctx := &ChatContext{
		ProjectPath: cfg.ProjectPath,
		SessionID:   sessionID,
		Role:        RoleRouter,
		Cancel:      cancel,
		OnChunk: func(content string, _ bool) {
			if content == "" {
				return
			}
			mu.Lock()
			out.WriteString(content)
			mu.Unlock()
		},
		OnError:      func(string) {},
		OnToolCall:   func(string, any) {},
		OnToolResult: func(string, any, bool) {},
	}
	cfg.chatFnFor()(ctx, triagePrompt(brief))

	decision := strings.ToUpper(strings.TrimSpace(out.String()))
	if decision == "" {
		return RouteBuild, false
	}
	if strings.Contains(decision, "CHAT") || strings.Contains(decision, "CONVERSATION") {
		return RouteConversational, true
	}
	if strings.Contains(decision, "BUILD") {
		return RouteBuild, true
	}
	return RouteBuild, false
}

// triagePrompt frames the brief for the RoleRouter classifier.
func triagePrompt(brief string) string {
	return strings.Join([]string{
		"Classify the following user message.",
		"Answer with exactly one word: BUILD or CHAT.",
		"BUILD = the user wants the project changed, created, fixed, improved, or otherwise worked on.",
		"CHAT = the user is asking a question, chatting, or wants information with no project change.",
		"",
		"USER MESSAGE:",
		brief,
	}, "\n")
}

// runConversationalTurn answers the brief in a single interactive chat turn and
// streams tokens straight to the client. It is the RouteConversational handler.
func runConversationalTurn(cfg OrchestratorConfig, brief string, cancel <-chan struct{}) {
	sessionID := fmt.Sprintf("%s-chat-%d", chooseSessionPrefix(cfg), time.Now().UnixNano())
	_ = db.CreateSession(sessionID, cfg.ProjectPath, "")

	ctx := &ChatContext{
		ProjectPath:  cfg.ProjectPath,
		SessionID:    sessionID,
		Role:         RoleInteractive,
		Cancel:       cancel,
		OnError:      func(string) {},
		OnToolCall:   func(string, any) {},
		OnToolResult: func(string, any, bool) {},
	}
	// Forward streamed content to the client as chat chunks. The terminal
	// done chunk is emitted by the caller (handler) so exactly one is sent.
	ctx.OnChunk = func(content string, _ bool) {
		if content == "" {
			return
		}
		if ctx.SendToClient != nil {
			ctx.SendToClient("chat.chunk", map[string]any{"content": content, "done": false})
		}
	}
	cfg.chatFnFor()(ctx, brief)
}
