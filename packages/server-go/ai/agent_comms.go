package ai

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// AgentPeer is one live participant in a project's agent communication pool.
type AgentPeer struct {
	ID        string    `json:"id"`
	Role      string    `json:"role"`
	Status    string    `json:"status"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// AgentMessage is a focused handoff or reply between two project agents.
type AgentMessage struct {
	ID        string    `json:"id"`
	From      string    `json:"from"`
	To        string    `json:"to"`
	Subject   string    `json:"subject,omitempty"`
	Body      string    `json:"body"`
	ReplyTo   string    `json:"replyTo,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
}

// AgentCommsHub is the in-process message pool for agents working on one project.
type AgentCommsHub struct {
	mu       sync.Mutex
	agents   map[string]AgentPeer
	messages map[string]AgentMessage
	inbox    map[string][]string
	nextID   int
}

var projectAgentComms sync.Map

// NewAgentCommsHub creates an empty project communication pool.
func NewAgentCommsHub() *AgentCommsHub {
	return &AgentCommsHub{
		agents:   make(map[string]AgentPeer),
		messages: make(map[string]AgentMessage),
		inbox:    make(map[string][]string),
	}
}

// AgentCommsForProject returns the shared communication pool for a project path.
func AgentCommsForProject(projectPath string) *AgentCommsHub {
	key := strings.TrimSpace(projectPath)
	if key == "" {
		key = "__default__"
	}
	if existing, ok := projectAgentComms.Load(key); ok {
		return existing.(*AgentCommsHub)
	}
	created := NewAgentCommsHub()
	actual, _ := projectAgentComms.LoadOrStore(key, created)
	return actual.(*AgentCommsHub)
}

// Register adds or updates a live peer in the pool.
func (h *AgentCommsHub) Register(id, role, status string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	id = strings.TrimSpace(id)
	if id == "" {
		return
	}
	h.agents[id] = AgentPeer{
		ID:        id,
		Role:      strings.TrimSpace(role),
		Status:    strings.TrimSpace(status),
		UpdatedAt: time.Now().UTC(),
	}
}

// List returns all registered peers sorted by ID for deterministic prompts.
func (h *AgentCommsHub) List() []AgentPeer {
	h.mu.Lock()
	defer h.mu.Unlock()

	agents := make([]AgentPeer, 0, len(h.agents))
	for _, agent := range h.agents {
		agents = append(agents, agent)
	}
	sort.Slice(agents, func(i, j int) bool { return agents[i].ID < agents[j].ID })
	return agents
}

// Send records a message and places it in the recipient inbox.
func (h *AgentCommsHub) Send(from, to, subject, body, replyTo string) (AgentMessage, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	from = strings.TrimSpace(from)
	to = strings.TrimSpace(to)
	body = strings.TrimSpace(body)
	if from == "" {
		return AgentMessage{}, fmt.Errorf("agent_send: sender is required")
	}
	if to == "" {
		return AgentMessage{}, fmt.Errorf("agent_send: recipient is required")
	}
	if body == "" {
		return AgentMessage{}, fmt.Errorf("agent_send: body is required")
	}
	if _, ok := h.agents[to]; !ok {
		return AgentMessage{}, fmt.Errorf("agent_send: recipient %q is not registered", to)
	}

	h.nextID++
	message := AgentMessage{
		ID:        fmt.Sprintf("msg-%d", h.nextID),
		From:      from,
		To:        to,
		Subject:   strings.TrimSpace(subject),
		Body:      body,
		ReplyTo:   strings.TrimSpace(replyTo),
		CreatedAt: time.Now().UTC(),
	}
	h.messages[message.ID] = message
	h.inbox[to] = append(h.inbox[to], message.ID)
	return message, nil
}

// Inbox returns pending messages for an agent. When consume is true, it clears them.
func (h *AgentCommsHub) Inbox(agentID string, consume bool) []AgentMessage {
	h.mu.Lock()
	defer h.mu.Unlock()

	agentID = strings.TrimSpace(agentID)
	ids := h.inbox[agentID]
	messages := make([]AgentMessage, 0, len(ids))
	for _, id := range ids {
		messages = append(messages, h.messages[id])
	}
	if consume {
		delete(h.inbox, agentID)
	}
	return messages
}

// Await waits for a matching message in an agent inbox until timeout elapses.
func (h *AgentCommsHub) Await(agentID, messageID, replyTo string, timeout time.Duration) (AgentMessage, error) {
	deadline := time.Now().Add(timeout)
	for {
		if message, ok := h.takeMatching(agentID, messageID, replyTo); ok {
			return message, nil
		}
		if timeout <= 0 || time.Now().After(deadline) {
			return AgentMessage{}, fmt.Errorf("agent_await: no matching message")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (h *AgentCommsHub) takeMatching(agentID, messageID, replyTo string) (AgentMessage, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()

	agentID = strings.TrimSpace(agentID)
	messageID = strings.TrimSpace(messageID)
	replyTo = strings.TrimSpace(replyTo)
	ids := h.inbox[agentID]
	for i, id := range ids {
		message := h.messages[id]
		if messageID != "" && message.ID != messageID {
			continue
		}
		if replyTo != "" && message.ReplyTo != replyTo {
			continue
		}
		h.inbox[agentID] = append(ids[:i], ids[i+1:]...)
		return message, true
	}
	return AgentMessage{}, false
}

func registerChatAgent(ctx *ChatContext) {
	if ctx == nil {
		return
	}
	if ctx.AgentComms == nil {
		ctx.AgentComms = AgentCommsForProject(ctx.ProjectPath)
	}
	if strings.TrimSpace(ctx.AgentName) == "" {
		ctx.AgentName = agentRoleLabel(ctx.Role)
	}
	ctx.AgentComms.Register(ctx.AgentName, agentRoleLabel(ctx.Role), "active")
}

func agentRoleLabel(role AgentRole) string {
	labels := map[AgentRole]string{
		RoleInteractive:       "lead",
		RolePlanner:           "planner",
		RoleScaffolder:        "scaffolder",
		RoleImplementer:       "implementer",
		RoleTester:            "tester",
		RoleReviewer:          "reviewer",
		RoleDocumenter:        "documenter",
		RoleIntaker:           "intaker",
		RoleAutonomousBuilder: "builder",
		RoleGriller:           "griller",
		RolePRDWriter:         "prd-writer",
		RoleModuleIndexer:     "module-indexer",
		RoleArchitect:         "architect",
	}
	if label, ok := labels[role]; ok {
		return label
	}
	return fmt.Sprintf("role-%d", role)
}

func agentCommsReady(ctx *ChatContext) (string, *AgentCommsHub, string, bool) {
	if ctx == nil || ctx.AgentComms == nil {
		return "agent communication is unavailable in this session", nil, "", false
	}
	agentID := strings.TrimSpace(ctx.AgentName)
	if agentID == "" {
		agentID = agentRoleLabel(ctx.Role)
	}
	return "", ctx.AgentComms, agentID, true
}

func executeAgentListTool(ctx *ChatContext) (string, bool) {
	if errMsg, hub, _, ok := agentCommsReady(ctx); !ok {
		return errMsg, true
	} else {
		payload, _ := json.MarshalIndent(hub.List(), "", "  ")
		return string(payload), false
	}
}

func executeAgentSendTool(input map[string]any, ctx *ChatContext) (string, bool) {
	if errMsg, hub, agentID, ok := agentCommsReady(ctx); !ok {
		return errMsg, true
	} else {
		message, err := hub.Send(agentID, stringInput(input, "to"), stringInput(input, "subject"), stringInput(input, "body"), stringInput(input, "reply_to"))
		if err != nil {
			return err.Error(), true
		}
		payload, _ := json.MarshalIndent(message, "", "  ")
		return string(payload), false
	}
}

func executeAgentInboxTool(input map[string]any, ctx *ChatContext) (string, bool) {
	if errMsg, hub, agentID, ok := agentCommsReady(ctx); !ok {
		return errMsg, true
	} else {
		payload, _ := json.MarshalIndent(hub.Inbox(agentID, boolInput(input, "consume")), "", "  ")
		return string(payload), false
	}
}

func executeAgentAwaitTool(input map[string]any, ctx *ChatContext) (string, bool) {
	if errMsg, hub, agentID, ok := agentCommsReady(ctx); !ok {
		return errMsg, true
	} else {
		message, err := hub.Await(agentID, stringInput(input, "message_id"), stringInput(input, "reply_to"), time.Duration(numberInput(input, "timeout_ms"))*time.Millisecond)
		if err != nil {
			return err.Error(), true
		}
		payload, _ := json.MarshalIndent(message, "", "  ")
		return string(payload), false
	}
}

func stringInput(input map[string]any, key string) string {
	value, _ := input[key].(string)
	return value
}

func boolInput(input map[string]any, key string) bool {
	value, _ := input[key].(bool)
	return value
}

func numberInput(input map[string]any, key string) float64 {
	value, _ := input[key].(float64)
	return value
}
