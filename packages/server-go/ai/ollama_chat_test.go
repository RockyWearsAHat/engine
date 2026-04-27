package ai

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/engine/server/db"
)

func TestChat_OllamaProvider_UsesRunningModelAndPersistsMessages(t *testing.T) {
	projectDir := setupHistoryTestProject(t)
	if err := db.CreateSession("session-ollama", projectDir, "main"); err != nil {
		t.Fatalf("create session: %v", err)
	}

	requestedModels := make([]string, 0, 1)
	ollamaServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/ps":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"models":[{"name":"gemma4:31b"}]}`))
		case "/v1/chat/completions":
			var request struct {
				Model string `json:"model"`
			}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatalf("decode ollama request: %v", err)
			}
			requestedModels = append(requestedModels, request.Model)
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"reasoning\":\"thinking\"},\"finish_reason\":null}]}\n\n"))
			_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"pong\"},\"finish_reason\":null}]}\n\n"))
			_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n"))
			_, _ = w.Write([]byte("data: [DONE]\n\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer ollamaServer.Close()

	t.Setenv("ENGINE_MODEL_PROVIDER", "ollama")
	t.Setenv("ENGINE_MODEL", "")
	t.Setenv("OLLAMA_BASE_URL", ollamaServer.URL)

	var chunks []string
	var sessionCounts []int
	ctx := &ChatContext{
		ProjectPath: projectDir,
		SessionID:   "session-ollama",
		OnSessionUpdated: func(session *db.Session) {
			sessionCounts = append(sessionCounts, session.MessageCount)
		},
		OnChunk: func(content string, done bool) {
			if done {
				chunks = append(chunks, "<done>")
				return
			}
			chunks = append(chunks, content)
		},
		OnToolCall:   func(string, any) {},
		OnToolResult: func(string, any, bool) {},
		OnError: func(err string) {
			t.Fatalf("unexpected chat error: %s", err)
		},
	}

	Chat(ctx, "hello local beast")

	if len(requestedModels) != 1 || requestedModels[0] != "gemma4:31b" {
		t.Fatalf("expected running ollama model to be used, got %+v", requestedModels)
	}
	if len(sessionCounts) == 0 || sessionCounts[0] < 1 {
		t.Fatalf("expected session updates after persisting the user message, got %+v", sessionCounts)
	}
	if got := strings.Join(chunks, ""); !strings.Contains(got, "pong") || !strings.Contains(got, "<done>") {
		t.Fatalf("expected streamed response and done marker, got %q", got)
	}

	messages, err := db.GetMessages("session-ollama")
	if err != nil {
		t.Fatalf("get messages: %v", err)
	}
	if len(messages) != 2 {
		t.Fatalf("expected persisted user and assistant messages, got %+v", messages)
	}
	if messages[0].Role != "user" || messages[0].Content != "hello local beast" {
		t.Fatalf("expected first persisted message to be user content, got %+v", messages[0])
	}
	if messages[1].Role != "assistant" || !strings.Contains(messages[1].Content, "pong") {
		t.Fatalf("expected assistant response to persist, got %+v", messages[1])
	}
}

func TestChat_RolePlanner_SeedsPreGrantedTools(t *testing.T) {
	projectDir := setupHistoryTestProject(t)
	if err := db.CreateSession("session-planner", projectDir, "main"); err != nil {
		t.Fatalf("create session: %v", err)
	}

	ollamaServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/ps":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"models":[{"name":"llama3.2"}]}`))
		case "/v1/chat/completions":
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"plan ready\"},\"finish_reason\":null}]}\n\n"))
			_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n"))
			_, _ = w.Write([]byte("data: [DONE]\n\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer ollamaServer.Close()

	t.Setenv("ENGINE_MODEL_PROVIDER", "ollama")
	t.Setenv("ENGINE_MODEL", "")
	t.Setenv("OLLAMA_BASE_URL", ollamaServer.URL)

	var got strings.Builder
	ctx := &ChatContext{
		ProjectPath: projectDir,
		SessionID:   "session-planner",
		Role:        RolePlanner,
		OnSessionUpdated: func(_ *db.Session) {},
		OnChunk: func(content string, done bool) {
			if !done {
				got.WriteString(content)
			}
		},
		OnToolCall:   func(string, any) {},
		OnToolResult: func(string, any, bool) {},
		OnError: func(err string) {
			t.Fatalf("unexpected chat error: %s", err)
		},
	}

	Chat(ctx, "draft a plan")

	if !strings.Contains(got.String(), "plan ready") {
		t.Fatalf("expected planner response, got %q", got.String())
	}
}

func TestRunPlannerPrePass_ReturnsProviderOutput(t *testing.T) {
	projectDir := setupHistoryTestProject(t)

	ollamaServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/ps":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"models":[{"name":"qwen2.5:1.5b"}]}`))
		case "/v1/chat/completions":
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"1. Create main.py\"},\"finish_reason\":null}]}\n\n"))
			_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n"))
			_, _ = w.Write([]byte("data: [DONE]\n\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer ollamaServer.Close()

	t.Setenv("OLLAMA_BASE_URL", ollamaServer.URL)

	ctx := &ChatContext{
		ProjectPath: projectDir,
		SessionID:   "session-prepass",
		Role:        RoleInteractive,
		Usage:       &SessionUsage{},
		Quarantine:  NewToolQuarantine(),
		Cancel:      make(chan struct{}),
		OnError:     func(string) {},
	}

	result := runPlannerPrePass(ctx, "ollama", "qwen2.5:1.5b", "build a trading bot", "main")
	if !strings.Contains(result, "Create main.py") {
		t.Fatalf("expected plan output, got %q", result)
	}
}

func TestRunPlannerPrePass_EmptyOutput_ReturnsEmpty(t *testing.T) {
	projectDir := setupHistoryTestProject(t)

	ollamaServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/ps":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"models":[{"name":"qwen2.5:1.5b"}]}`))
		case "/v1/chat/completions":
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"   \"},\"finish_reason\":null}]}\n\n"))
			_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n"))
			_, _ = w.Write([]byte("data: [DONE]\n\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer ollamaServer.Close()

	t.Setenv("OLLAMA_BASE_URL", ollamaServer.URL)

	ctx := &ChatContext{
		ProjectPath: projectDir,
		SessionID:   "session-prepass-empty",
		Role:        RoleInteractive,
		Usage:       &SessionUsage{},
		Quarantine:  NewToolQuarantine(),
		Cancel:      make(chan struct{}),
		OnError:     func(string) {},
	}

	result := runPlannerPrePass(ctx, "ollama", "qwen2.5:1.5b", "build a trading bot", "main")
	if result != "" {
		t.Fatalf("expected empty result for whitespace-only output, got %q", result)
	}
}

func TestRunPlannerPrePass_UsesPlannerModelOverride(t *testing.T) {
	projectDir := setupHistoryTestProject(t)

	requestedModels := make([]string, 0, 1)
	ollamaServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/ps":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"models":[{"name":"qwen2.5:1.5b"}]}`))
		case "/v1/chat/completions":
			var request struct {
				Model string `json:"model"`
			}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatalf("decode ollama request: %v", err)
			}
			requestedModels = append(requestedModels, request.Model)
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"1. Plan step\"},\"finish_reason\":null}]}\n\n"))
			_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n"))
			_, _ = w.Write([]byte("data: [DONE]\n\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer ollamaServer.Close()

	t.Setenv("OLLAMA_BASE_URL", ollamaServer.URL)
	t.Setenv("ENGINE_PLANNER_PROVIDER", "ollama")
	t.Setenv("ENGINE_PLANNER_MODEL", "gemma4:31b")

	ctx := &ChatContext{
		ProjectPath: projectDir,
		SessionID:   "session-prepass-override",
		Role:        RoleInteractive,
		Usage:       &SessionUsage{},
		Quarantine:  NewToolQuarantine(),
		Cancel:      make(chan struct{}),
		OnError:     func(string) {},
	}

	_ = runPlannerPrePass(ctx, "ollama", "qwen2.5:1.5b", "build a trading bot", "main")
	if len(requestedModels) != 1 || requestedModels[0] != "gemma4:31b" {
		t.Fatalf("expected planner override model gemma4:31b, got %+v", requestedModels)
	}
}

func TestChat_ReviewerRole_UsesReviewerModelOverride(t *testing.T) {
	projectDir := setupHistoryTestProject(t)
	if err := db.CreateSession("session-reviewer-override", projectDir, "main"); err != nil {
		t.Fatalf("create session: %v", err)
	}

	requestedModels := make([]string, 0, 1)
	ollamaServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/ps":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"models":[{"name":"qwen2.5:1.5b"}]}`))
		case "/v1/chat/completions":
			var request struct {
				Model string `json:"model"`
			}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatalf("decode ollama request: %v", err)
			}
			requestedModels = append(requestedModels, request.Model)
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"APPROVE\"},\"finish_reason\":null}]}\n\n"))
			_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n"))
			_, _ = w.Write([]byte("data: [DONE]\n\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer ollamaServer.Close()

	t.Setenv("ENGINE_MODEL_PROVIDER", "ollama")
	t.Setenv("ENGINE_MODEL", "qwen2.5:1.5b")
	t.Setenv("ENGINE_REVIEWER_PROVIDER", "ollama")
	t.Setenv("ENGINE_REVIEWER_MODEL", "gemma4:31b")
	t.Setenv("OLLAMA_BASE_URL", ollamaServer.URL)

	ctx := &ChatContext{
		ProjectPath: projectDir,
		SessionID:   "session-reviewer-override",
		Role:        RoleReviewer,
		OnSessionUpdated: func(_ *db.Session) {},
		OnChunk:      func(string, bool) {},
		OnToolCall:   func(string, any) {},
		OnToolResult: func(string, any, bool) {},
		OnError: func(err string) {
			t.Fatalf("unexpected chat error: %s", err)
		},
	}

	Chat(ctx, "review this change")
	if len(requestedModels) == 0 || requestedModels[0] != "gemma4:31b" {
		t.Fatalf("expected reviewer override model gemma4:31b, got %+v", requestedModels)
	}
}

func TestChat_WorkflowRequest_TriggersPlannerPrePass(t *testing.T) {
	projectDir := setupHistoryTestProject(t)
	if err := db.CreateSession("session-workflow-plan", projectDir, "main"); err != nil {
		t.Fatalf("create session: %v", err)
	}

	callCount := 0
	ollamaServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/ps":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"models":[{"name":"qwen2.5:1.5b"}]}`))
		case "/v1/chat/completions":
			callCount++
			w.Header().Set("Content-Type", "text/event-stream")
			if callCount == 1 {
				// Planner pre-pass response.
				_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"1. Build the bot core\"},\"finish_reason\":null}]}\n\n"))
			} else {
				// Interactive agent response.
				_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"Starting implementation\"},\"finish_reason\":null}]}\n\n"))
			}
			_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n"))
			_, _ = w.Write([]byte("data: [DONE]\n\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer ollamaServer.Close()

	t.Setenv("ENGINE_MODEL_PROVIDER", "ollama")
	t.Setenv("ENGINE_MODEL", "qwen2.5:1.5b")
	t.Setenv("OLLAMA_BASE_URL", ollamaServer.URL)

	var planMessages []string
	ctx := &ChatContext{
		ProjectPath: projectDir,
		SessionID:   "session-workflow-plan",
		Role:        RoleInteractive,
		OnSessionUpdated: func(_ *db.Session) {},
		OnChunk:      func(string, bool) {},
		OnToolCall:   func(string, any) {},
		OnToolResult: func(string, any, bool) {},
		OnError:      func(err string) { t.Logf("chat error: %s", err) },
		SendToClient: func(msgType string, payload any) {
			if msgType == "chat.plan" {
				if m, ok := payload.(map[string]any); ok {
					if p, ok := m["plan"].(string); ok {
						planMessages = append(planMessages, p)
					}
				}
			}
		},
	}

	Chat(ctx, "build a trading bot that makes money")

	if len(planMessages) == 0 {
		t.Fatal("expected chat.plan message to be sent to client for workflow request")
	}
	if !strings.Contains(planMessages[0], "Build the bot core") {
		t.Fatalf("expected plan content, got %q", planMessages[0])
	}
	if callCount < 2 {
		t.Fatalf("expected at least 2 provider calls (planner + interactive), got %d", callCount)
	}
}

func TestChat_NonWorkflowRequest_SkipsPlannerPrePass(t *testing.T) {
	projectDir := setupHistoryTestProject(t)
	if err := db.CreateSession("session-qa-noplan", projectDir, "main"); err != nil {
		t.Fatalf("create session: %v", err)
	}

	callCount := 0
	ollamaServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/ps":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"models":[{"name":"qwen2.5:1.5b"}]}`))
		case "/v1/chat/completions":
			callCount++
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"42\"},\"finish_reason\":null}]}\n\n"))
			_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n"))
			_, _ = w.Write([]byte("data: [DONE]\n\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer ollamaServer.Close()

	t.Setenv("ENGINE_MODEL_PROVIDER", "ollama")
	t.Setenv("ENGINE_MODEL", "qwen2.5:1.5b")
	t.Setenv("OLLAMA_BASE_URL", ollamaServer.URL)

	var planMessages []string
	ctx := &ChatContext{
		ProjectPath: projectDir,
		SessionID:   "session-qa-noplan",
		Role:        RoleInteractive,
		OnSessionUpdated: func(_ *db.Session) {},
		OnChunk:      func(string, bool) {},
		OnToolCall:   func(string, any) {},
		OnToolResult: func(string, any, bool) {},
		OnError:      func(err string) { t.Logf("chat error: %s", err) },
		SendToClient: func(msgType string, payload any) {
			if msgType == "chat.plan" {
				planMessages = append(planMessages, "plan")
			}
		},
	}

	Chat(ctx, "what is the answer to life?")

	if len(planMessages) != 0 {
		t.Fatalf("expected no chat.plan for simple Q&A, got %d", len(planMessages))
	}
	if callCount != 1 {
		t.Fatalf("expected exactly 1 provider call for simple Q&A, got %d", callCount)
	}
}
