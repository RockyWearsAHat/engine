package discord

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/engine/server/ai"
)

type fakeOrchHandle struct {
	stops     int
	pauses    int
	resumes   int
	redirects []string
	mu        sync.Mutex
}

// matchOrch returns a control_test.go-local injection for orchestratorHandleFn.
func swapOrch(t *testing.T, byPath map[string]*ai.OrchestratorHandle) func() {
	t.Helper()
	orig := orchestratorHandleFn
	orchestratorHandleFn = func(projectPath string) *ai.OrchestratorHandle {
		return byPath[projectPath]
	}
	return func() { orchestratorHandleFn = orig }
}

func TestHandleStopCommand_NoActiveOrchestrator(t *testing.T) {
	t.Cleanup(swapOrch(t, map[string]*ai.OrchestratorHandle{}))

	var sent []string
	svc := newTestServiceForControl(map[string]ProjectBinding{
		"/proj": {ProjectPath: "/proj", RepoName: "demo", ChannelID: "ch-1"},
	}, &sent)

	svc.handleStopCommand(&discordgo.MessageCreate{Message: &discordgo.Message{ChannelID: "ch-1"}}, nil)
	if len(sent) == 0 || !strings.Contains(sent[0], "No active orchestrator") {
		t.Fatalf("expected 'No active orchestrator' message, got %v", sent)
	}
}

func TestHandleRedirectCommand_NoActiveOrchestrator(t *testing.T) {
	t.Cleanup(swapOrch(t, map[string]*ai.OrchestratorHandle{}))

	var sent []string
	svc := newTestServiceForControl(map[string]ProjectBinding{
		"/proj": {ProjectPath: "/proj", RepoName: "demo", ChannelID: "ch-1"},
	}, &sent)

	svc.handleRedirectCommand(&discordgo.MessageCreate{Message: &discordgo.Message{ChannelID: "ch-1"}}, []string{"focus", "on", "tests"})
	joined := strings.Join(sent, "\n")
	if !strings.Contains(joined, "No active orchestrator") {
		t.Fatalf("expected no-orch message, got %v", sent)
	}
}

func TestHandleRedirectCommand_RequiresInstruction(t *testing.T) {
	var sent []string
	svc := newTestServiceForControl(map[string]ProjectBinding{
		"/proj": {ProjectPath: "/proj", RepoName: "demo", ChannelID: "ch-1"},
	}, &sent)

	svc.handleRedirectCommand(&discordgo.MessageCreate{Message: &discordgo.Message{ChannelID: "ch-1"}}, nil)
	joined := strings.Join(sent, "\n")
	if !strings.Contains(joined, "Usage") {
		t.Fatalf("expected usage hint, got %v", sent)
	}
}

func TestHandlePlanCommand_NoPlanFile(t *testing.T) {
	var sent []string
	tmp := t.TempDir()
	svc := newTestServiceForControl(map[string]ProjectBinding{
		tmp: {ProjectPath: tmp, RepoName: "demo", ChannelID: "ch-1"},
	}, &sent)
	svc.handlePlanCommand(&discordgo.MessageCreate{Message: &discordgo.Message{ChannelID: "ch-1"}}, nil)
	if len(sent) == 0 || !strings.Contains(sent[0], "No plan recorded") {
		t.Fatalf("expected no-plan message, got %v", sent)
	}
}

func TestHandlePlanCommand_PrintsPlanFile(t *testing.T) {
	var sent []string
	tmp := t.TempDir()
	_ = os.MkdirAll(filepath.Join(tmp, ".engine"), 0o755)
	_ = os.WriteFile(filepath.Join(tmp, ".engine", "plan.md"), []byte("# Plan\n- step one\n"), 0o644)

	svc := newTestServiceForControl(map[string]ProjectBinding{
		tmp: {ProjectPath: tmp, RepoName: "demo", ChannelID: "ch-1"},
	}, &sent)
	svc.handlePlanCommand(&discordgo.MessageCreate{Message: &discordgo.Message{ChannelID: "ch-1"}}, nil)
	joined := strings.Join(sent, "\n")
	if !strings.Contains(joined, "step one") {
		t.Fatalf("expected plan content, got %v", sent)
	}
}

func TestHandleOrchestratorsCommand_Empty(t *testing.T) {
	orig := orchestratorListFn
	t.Cleanup(func() { orchestratorListFn = orig })
	orchestratorListFn = func() []string { return nil }

	var sent []string
	svc := newTestServiceForControl(nil, &sent)
	svc.handleOrchestratorsCommand(&discordgo.MessageCreate{Message: &discordgo.Message{ChannelID: "ch-1"}}, nil)
	if len(sent) == 0 || !strings.Contains(sent[0], "No active orchestrators") {
		t.Fatalf("expected empty message, got %v", sent)
	}
}

func TestHandleOrchestratorsCommand_Lists(t *testing.T) {
	orig := orchestratorListFn
	t.Cleanup(func() { orchestratorListFn = orig })
	orchestratorListFn = func() []string { return []string{"/proj/a", "/proj/b"} }

	var sent []string
	svc := newTestServiceForControl(nil, &sent)
	svc.handleOrchestratorsCommand(&discordgo.MessageCreate{Message: &discordgo.Message{ChannelID: "ch-1"}}, nil)
	joined := strings.Join(sent, "\n")
	if !strings.Contains(joined, "/proj/a") || !strings.Contains(joined, "/proj/b") {
		t.Fatalf("expected both projects listed, got %v", sent)
	}
}

// newTestServiceForControl builds a Service stub that records messages instead
// of hitting Discord. Tests against the control commands need to send fake
// MessageCreate events and verify the reply text.
func newTestServiceForControl(projects map[string]ProjectBinding, sent *[]string) *Service {
	if projects == nil {
		projects = map[string]ProjectBinding{}
	}
	svc := &Service{
		cfg:             Config{CommandPrefix: "!"},
		state:           persistedState{Projects: projects},
		active:          map[string]bool{},
		activeByChannel: map[string]bool{},
	}
	// Override the channel-send path by intercepting via the tagged channel
	// archive. Since send() short-circuits when dg is nil, just shadow send
	// behaviour: we set a hook that records what's about to be sent.
	svc.testSendHook = func(channelID, msg string) {
		_ = channelID
		*sent = append(*sent, msg)
	}
	return svc
}
