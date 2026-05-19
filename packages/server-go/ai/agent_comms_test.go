package ai

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestAgentCommsHub_RegisterListSendInboxAwait(t *testing.T) {
	hub := NewAgentCommsHub()
	hub.Register("", "ignored", "active")
	hub.Register("builder", "implementation", "queued")
	hub.Register("lead", "orchestrator", "active")
	hub.Register("builder", "implementation", "running")

	agents := hub.List()
	if len(agents) != 2 {
		t.Fatalf("expected 2 agents, got %d", len(agents))
	}
	if agents[0].ID != "builder" || agents[0].Status != "running" || agents[1].ID != "lead" {
		t.Fatalf("agents not sorted/updated: %+v", agents)
	}

	if _, err := hub.Send("", "builder", "", "body", ""); err == nil {
		t.Fatal("expected missing sender to error")
	}
	if _, err := hub.Send("lead", "", "", "body", ""); err == nil {
		t.Fatal("expected missing recipient to error")
	}
	if _, err := hub.Send("lead", "builder", "", "", ""); err == nil {
		t.Fatal("expected missing body to error")
	}
	if _, err := hub.Send("lead", "missing", "", "body", ""); err == nil {
		t.Fatal("expected unregistered recipient to error")
	}

	request, err := hub.Send("lead", "builder", "question", "what changed?", "")
	if err != nil {
		t.Fatalf("send request: %v", err)
	}
	peeked := hub.Inbox("builder", false)
	if len(peeked) != 1 || peeked[0].ID != request.ID {
		t.Fatalf("peek inbox mismatch: %+v", peeked)
	}
	consumed := hub.Inbox("builder", true)
	if len(consumed) != 1 || consumed[0].Subject != "question" {
		t.Fatalf("consume inbox mismatch: %+v", consumed)
	}
	if remaining := hub.Inbox("builder", false); len(remaining) != 0 {
		t.Fatalf("expected consumed inbox to be empty, got %+v", remaining)
	}

	wrongID, err := hub.Send("builder", "lead", "wrong id", "skip me", "")
	if err != nil {
		t.Fatalf("send wrong id: %v", err)
	}
	targetID, err := hub.Send("builder", "lead", "right id", "match me", "target-reply")
	if err != nil {
		t.Fatalf("send target id: %v", err)
	}
	matchedID, err := hub.Await("lead", targetID.ID, "target-reply", 0)
	if err != nil {
		t.Fatalf("await by id/reply: %v", err)
	}
	if matchedID.ID != targetID.ID || wrongID.ID == matchedID.ID {
		t.Fatalf("await matched wrong message: %+v", matchedID)
	}

	wrongReply, err := hub.Send("builder", "lead", "wrong reply", "skip reply", "other")
	if err != nil {
		t.Fatalf("send wrong reply: %v", err)
	}
	targetReply, err := hub.Send("builder", "lead", "right reply", "reply body", request.ID)
	if err != nil {
		t.Fatalf("send target reply: %v", err)
	}
	matchedReply, err := hub.Await("lead", "", request.ID, 0)
	if err != nil {
		t.Fatalf("await reply: %v", err)
	}
	if matchedReply.ID != targetReply.ID || wrongReply.ID == matchedReply.ID {
		t.Fatalf("await matched wrong reply: %+v", matchedReply)
	}

	go func() {
		time.Sleep(15 * time.Millisecond)
		_, _ = hub.Send("builder", "lead", "later", "async", "")
	}()
	asyncMessage, err := hub.Await("lead", "", "", 100*time.Millisecond)
	if err != nil {
		t.Fatalf("await async message: %v", err)
	}
	if asyncMessage.Body == "" {
		t.Fatalf("expected async message body, got %+v", asyncMessage)
	}

	if _, err := hub.Await("lead", "missing", "", 0); err == nil {
		t.Fatal("expected await without match to error")
	}
	if _, err := hub.Await("lead", "missing", "", 20*time.Millisecond); err == nil {
		t.Fatal("expected timed await without match to error")
	}
}

func TestAgentCommsForProjectAndRegistration(t *testing.T) {
	defaultHub := AgentCommsForProject("")
	if defaultHub != AgentCommsForProject("") {
		t.Fatal("expected empty project path to reuse default hub")
	}

	ctx := &ChatContext{ProjectPath: t.TempDir(), Role: RolePlanner}
	registerChatAgent(nil)
	registerChatAgent(ctx)
	if ctx.AgentName != "planner" {
		t.Fatalf("expected planner agent name, got %q", ctx.AgentName)
	}
	if ctx.AgentComms == nil {
		t.Fatal("expected registerChatAgent to attach project comms")
	}
	agents := ctx.AgentComms.List()
	if len(agents) != 1 || agents[0].ID != "planner" {
		t.Fatalf("expected planner registered, got %+v", agents)
	}

	if got := agentRoleLabel(AgentRole(999)); got != "role-999" {
		t.Fatalf("expected unknown role fallback, got %q", got)
	}
}

func TestAgentCommsTools(t *testing.T) {
	hub := NewAgentCommsHub()
	hub.Register("lead", "orchestrator", "active")
	hub.Register("builder", "implementation", "idle")
	ctx := &ChatContext{ProjectPath: t.TempDir(), Role: RoleInteractive, AgentName: "lead", AgentComms: hub}

	listResult, isErr := ExecuteToolForTest("agent_list", map[string]any{}, ctx)
	if isErr || !strings.Contains(listResult, "builder") {
		t.Fatalf("agent_list failed: %v %s", isErr, listResult)
	}

	sendResult, isErr := ExecuteToolForTest("agent_send", map[string]any{"to": "builder", "subject": "build", "body": "please implement"}, ctx)
	if isErr {
		t.Fatalf("agent_send failed: %s", sendResult)
	}
	var sent AgentMessage
	if err := json.Unmarshal([]byte(sendResult), &sent); err != nil {
		t.Fatalf("send result was not JSON: %v", err)
	}
	if sent.From != "lead" || sent.To != "builder" {
		t.Fatalf("unexpected sent message: %+v", sent)
	}

	inboxResult, isErr := ExecuteToolForTest("agent_inbox", map[string]any{"consume": false}, &ChatContext{Role: RoleAutonomousBuilder, AgentComms: hub})
	if isErr || !strings.Contains(inboxResult, "please implement") {
		t.Fatalf("agent_inbox fallback-name failed: %v %s", isErr, inboxResult)
	}

	if badResult, badErr := ExecuteToolForTest("agent_send", map[string]any{"to": "missing", "body": "hello"}, ctx); !badErr || !strings.Contains(badResult, "not registered") {
		t.Fatalf("expected missing recipient error, got err=%v result=%s", badErr, badResult)
	}

	reply, err := hub.Send("builder", "lead", "done", "finished", sent.ID)
	if err != nil {
		t.Fatalf("send reply: %v", err)
	}
	awaitResult, isErr := ExecuteToolForTest("agent_await", map[string]any{"reply_to": sent.ID}, ctx)
	if isErr || !strings.Contains(awaitResult, reply.ID) {
		t.Fatalf("agent_await failed: %v %s", isErr, awaitResult)
	}

	if awaitResult, isErr := ExecuteToolForTest("agent_await", map[string]any{"message_id": "missing"}, ctx); !isErr || !strings.Contains(awaitResult, "no matching message") {
		t.Fatalf("expected no-match await error, got err=%v result=%s", isErr, awaitResult)
	}

	if unavailable, isErr := ExecuteToolForTest("agent_list", map[string]any{}, &ChatContext{}); !isErr || !strings.Contains(unavailable, "unavailable") {
		t.Fatalf("expected unavailable comms error, got err=%v result=%s", isErr, unavailable)
	}
	for _, toolName := range []string{"agent_send", "agent_inbox", "agent_await"} {
		if unavailable, isErr := ExecuteToolForTest(toolName, map[string]any{}, &ChatContext{}); !isErr || !strings.Contains(unavailable, "unavailable") {
			t.Fatalf("expected unavailable %s error, got err=%v result=%s", toolName, isErr, unavailable)
		}
	}
}
