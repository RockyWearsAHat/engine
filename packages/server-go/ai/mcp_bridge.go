package ai

// MCP bridge. `claude -p` runs its own tool loop; Engine's comms tools
// (agent_*, signal_done, discord_*, mesh_exec, search_tools) live in Engine's
// loop. Bridge = stdio MCP server the CLI spawns per worker. Each tools/call
// hops back into the running Engine over HTTP (ENGINE_MCP_ADDR) or runs
// in-process when no addr set. Identity bound by env: ENGINE_MCP_PROJECT,
// ENGINE_MCP_AGENT, ENGINE_MCP_ROLE.
//
// Wire: JSON-RPC 2.0, one object per line. Methods: initialize, tools/list,
// tools/call, ping. Notifications (no id) get no reply.

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// MCPBridgeServerName: MCP server key. Tools surface as mcp__engine__<name>.
const MCPBridgeServerName = "engine"

// MCPBridgeSubcommand: os.Args[1] that turns the server binary into a bridge.
const MCPBridgeSubcommand = "mcp-bridge"

// MCPBridgeToolPath: HTTP route on the running Engine the bridge calls.
const MCPBridgeToolPath = "/mcp/tool"

const (
	envMCPProject   = "ENGINE_MCP_PROJECT"
	envMCPAgent     = "ENGINE_MCP_AGENT"
	envMCPRole      = "ENGINE_MCP_ROLE"
	envMCPAddr      = "ENGINE_MCP_ADDR"
	envMCPBridgeCmd = "ENGINE_MCP_BRIDGE_CMD"
)

// mcpBridgeToolNames: Engine tools exposed over the bridge. Only comms and
// signalling — file/shell tools stay Claude Code's own.
var mcpBridgeToolNames = []string{
	"agent_list", "agent_send", "agent_inbox", "agent_receive", "agent_await",
	"signal_done", "discord_post_progress", "discord_dm", "mesh_exec", "search_tools",
}

// mcpToolDef is one tools/list entry.
type mcpToolDef struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	InputSchema any    `json:"inputSchema"`
}

// MCPBridgeToolDefs: registry entries for the bridged names, registry order.
func MCPBridgeToolDefs() []mcpToolDef {
	want := map[string]bool{}
	for _, n := range mcpBridgeToolNames {
		want[n] = true
	}
	out := []mcpToolDef{}
	for _, t := range toolRegistry {
		if !want[t.Name] {
			continue
		}
		schema := t.InputSchema
		if schema == nil {
			schema = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		out = append(out, mcpToolDef{Name: t.Name, Description: t.Description, InputSchema: schema})
	}
	return out
}

func mcpBridgeToolAllowed(name string) bool {
	for _, n := range mcpBridgeToolNames {
		if n == name {
			return true
		}
	}
	return false
}

// BridgeToolRequest is the HTTP body the bridge posts to the Engine.
type BridgeToolRequest struct {
	Project string         `json:"project"`
	Agent   string         `json:"agent"`
	Role    string         `json:"role,omitempty"`
	Name    string         `json:"name"`
	Input   map[string]any `json:"input"`
}

// BridgeToolResponse is what the Engine answers.
type BridgeToolResponse struct {
	Result  string `json:"result"`
	IsError bool   `json:"isError"`
}

// Discord hooks for bridged tools. Set by main once the Discord service is up.
var (
	bridgeDiscordDM       func(message string) error
	bridgeDiscordProgress func(projectPath, message string)
)

// SetBridgeDiscord wires Discord into bridged discord_* calls.
func SetBridgeDiscord(dm func(string) error, progress func(projectPath, message string)) {
	bridgeDiscordDM = dm
	bridgeDiscordProgress = progress
}

// bridgeChatContext: minimal ChatContext bound to project + agent identity.
func bridgeChatContext(project, agent string) *ChatContext {
	ctx := &ChatContext{
		ProjectPath: strings.TrimSpace(project),
		AgentName:   strings.TrimSpace(agent),
		Role:        RoleAutonomousBuilder,
		AgentComms:  AgentCommsForProject(project),
	}
	if bridgeDiscordDM != nil {
		ctx.DiscordDM = bridgeDiscordDM
	}
	if bridgeDiscordProgress != nil {
		p := ctx.ProjectPath
		ctx.DiscordProgress = func(msg string) error {
			bridgeDiscordProgress(p, msg)
			return nil
		}
	}
	return ctx
}

// ExecuteBridgeTool runs one bridged tool in-process against the project hub.
func ExecuteBridgeTool(req BridgeToolRequest) BridgeToolResponse {
	name := strings.TrimSpace(req.Name)
	if !mcpBridgeToolAllowed(name) {
		return BridgeToolResponse{Result: "mcp bridge: tool not exposed: " + name, IsError: true}
	}
	if strings.TrimSpace(req.Agent) == "" {
		return BridgeToolResponse{Result: "mcp bridge: agent identity missing", IsError: true}
	}
	ctx := bridgeChatContext(req.Project, req.Agent)
	// Worker may call before its launcher registered it (race) — self-heal.
	role := strings.TrimSpace(req.Role)
	if role == "" {
		role = "worker"
	}
	ctx.AgentComms.Register(ctx.AgentName, role, "active")
	if req.Input == nil {
		req.Input = map[string]any{}
	}
	out, isErr := aiExecuteTool(name, req.Input, ctx)
	return BridgeToolResponse{Result: out, IsError: isErr}
}

// MCPBridgeHTTPHandler serves MCPBridgeToolPath on the running Engine.
func MCPBridgeHTTPHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req BridgeToolRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<20)).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(BridgeToolResponse{Result: "bad json: " + err.Error(), IsError: true})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(ExecuteBridgeTool(req))
}

// bridgeHTTPClient: long timeout. agent_await blocks on purpose.
var bridgeHTTPClient = &http.Client{Timeout: 10 * time.Minute}

// callBridgeTool: HTTP hop when addr set, in-process otherwise.
func callBridgeTool(addr string, req BridgeToolRequest) BridgeToolResponse {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return ExecuteBridgeTool(req)
	}
	if !strings.HasPrefix(addr, "http://") && !strings.HasPrefix(addr, "https://") {
		addr = "http://" + addr
	}
	body, _ := json.Marshal(req)
	resp, err := bridgeHTTPClient.Post(strings.TrimRight(addr, "/")+MCPBridgeToolPath, "application/json", bytes.NewReader(body))
	if err != nil {
		return BridgeToolResponse{Result: "mcp bridge: engine unreachable at " + addr + ": " + err.Error(), IsError: true}
	}
	defer resp.Body.Close()
	var out BridgeToolResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return BridgeToolResponse{Result: "mcp bridge: bad engine reply: " + err.Error(), IsError: true}
	}
	return out
}

// ── JSON-RPC ────────────────────────────────────────────────────────────────

type jsonRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type jsonRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type jsonRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *jsonRPCError   `json:"error,omitempty"`
}

// MCPBridgeIdentity: who this bridge process speaks as.
type MCPBridgeIdentity struct {
	Project string
	Agent   string
	Role    string
	Addr    string
}

// MCPBridgeIdentityFromEnv reads identity from the env the mcp config set.
func MCPBridgeIdentityFromEnv(getenv func(string) string) MCPBridgeIdentity {
	if getenv == nil {
		getenv = os.Getenv
	}
	return MCPBridgeIdentity{
		Project: strings.TrimSpace(getenv(envMCPProject)),
		Agent:   strings.TrimSpace(getenv(envMCPAgent)),
		Role:    strings.TrimSpace(getenv(envMCPRole)),
		Addr:    strings.TrimSpace(getenv(envMCPAddr)),
	}
}

// ServeMCPBridge runs the stdio JSON-RPC loop until r closes.
func ServeMCPBridge(r io.Reader, w io.Writer, id MCPBridgeIdentity) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 16<<20)
	enc := json.NewEncoder(w)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var req jsonRPCRequest
		if err := json.Unmarshal(line, &req); err != nil {
			_ = enc.Encode(jsonRPCResponse{JSONRPC: "2.0", Error: &jsonRPCError{Code: -32700, Message: "parse error"}})
			continue
		}
		resp, reply := handleMCPRequest(req, id)
		if reply {
			if err := enc.Encode(resp); err != nil {
				return err
			}
		}
	}
	return sc.Err()
}

// handleMCPRequest: one request → (response, whether to send it).
func handleMCPRequest(req jsonRPCRequest, id MCPBridgeIdentity) (jsonRPCResponse, bool) {
	isNotification := len(req.ID) == 0 || string(req.ID) == "null"
	resp := jsonRPCResponse{JSONRPC: "2.0", ID: req.ID}
	switch req.Method {
	case "initialize":
		resp.Result = map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "engine-mcp-bridge", "version": "1"},
		}
	case "ping":
		resp.Result = map[string]any{}
	case "tools/list":
		resp.Result = map[string]any{"tools": MCPBridgeToolDefs()}
	case "tools/call":
		var p struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			resp.Error = &jsonRPCError{Code: -32602, Message: "bad params: " + err.Error()}
			break
		}
		out := callBridgeTool(id.Addr, BridgeToolRequest{
			Project: id.Project, Agent: id.Agent, Role: id.Role, Name: p.Name, Input: p.Arguments,
		})
		resp.Result = map[string]any{
			"content": []map[string]any{{"type": "text", "text": out.Result}},
			"isError": out.IsError,
		}
	default:
		if isNotification {
			return resp, false
		}
		resp.Error = &jsonRPCError{Code: -32601, Message: "method not found: " + req.Method}
	}
	return resp, !isNotification
}

// ── Launch side (buildClaudeArgs) ───────────────────────────────────────────

// mcpBridgeCommand: what the CLI spawns. ENGINE_MCP_BRIDGE_CMD overrides
// (tests, exotic installs); default = this binary + "mcp-bridge".
func mcpBridgeCommand() (string, []string) {
	if v := strings.TrimSpace(os.Getenv(envMCPBridgeCmd)); v != "" {
		parts := strings.Fields(v)
		return parts[0], parts[1:]
	}
	exe, err := os.Executable()
	if err != nil || exe == "" {
		exe = os.Args[0]
	}
	return exe, []string{MCPBridgeSubcommand}
}

// engineMCPAddr: where the bridge calls back. ENGINE_MCP_ADDR else localhost:PORT.
func engineMCPAddr() string {
	if v := strings.TrimSpace(os.Getenv(envMCPAddr)); v != "" {
		return v
	}
	port := strings.TrimSpace(os.Getenv("PORT"))
	if port == "" {
		port = "24444"
	}
	return "http://127.0.0.1:" + port
}

// writeMCPConfig writes the per-worker mcp config json. Caller removes it.
func writeMCPConfig(ctx *ChatContext) (string, error) {
	cmd, args := mcpBridgeCommand()
	agent := strings.TrimSpace(ctx.AgentName)
	if agent == "" {
		agent = agentRoleLabel(ctx.Role)
	}
	env := map[string]string{
		envMCPProject: strings.TrimSpace(ctx.ProjectPath),
		envMCPAgent:   agent,
		envMCPRole:    agentRoleLabel(ctx.Role),
		envMCPAddr:    engineMCPAddr(),
	}
	// Bridge process needs the same override so a test stub chain stays inside
	// the test binary.
	if v := strings.TrimSpace(os.Getenv(envMCPBridgeCmd)); v != "" {
		env[envMCPBridgeCmd] = v
	}
	cfg := map[string]any{
		"mcpServers": map[string]any{
			MCPBridgeServerName: map[string]any{
				"command": cmd,
				"args":    args,
				"env":     env,
			},
		},
	}
	data, _ := json.MarshalIndent(cfg, "", "  ")
	dir := os.TempDir()
	name := "engine-mcp-" + taskSlug(agent) + "-" + strconv.FormatInt(time.Now().UnixNano(), 36) + ".json"
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, data, 0600); err != nil {
		return "", err
	}
	return path, nil
}

// mcpConfigPathFromArgs: the --mcp-config value, "" if absent.
func mcpConfigPathFromArgs(args []string) string {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "--mcp-config" {
			return args[i+1]
		}
	}
	return ""
}

// mcpBridgeArgError formats a config-write failure for the run log.
func mcpBridgeArgError(err error) string {
	return fmt.Sprintf("claudecode: mcp bridge config not written (%v) — worker runs without agent_* tools", err)
}
