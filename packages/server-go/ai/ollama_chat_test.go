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
