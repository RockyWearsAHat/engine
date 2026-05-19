package discord

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/engine/server/ai"
)

func TestHandleStopCommand_BindingNotFound(t *testing.T) {
	var sent []string
	svc := newTestServiceForControl(map[string]ProjectBinding{
		"/proj": {ProjectPath: "/proj", RepoName: "demo", ChannelID: "ch-1"},
	}, &sent)

	// "unknown-ch" has no binding → "Project not found"
	svc.handleStopCommand(&discordgo.MessageCreate{Message: &discordgo.Message{ChannelID: "unknown-ch"}}, nil)
	joined := strings.Join(sent, "\n")
	if !strings.Contains(joined, "Project not found") {
		t.Fatalf("expected 'Project not found' message, got %v", sent)
	}
}

func TestHandleStopCommand_StopsHandle(t *testing.T) {
	h := ai.NewHandle("/proj")
	t.Cleanup(swapOrch(t, map[string]*ai.OrchestratorHandle{"/proj": h}))

	var sent []string
	svc := newTestServiceForControl(map[string]ProjectBinding{
		"/proj": {ProjectPath: "/proj", RepoName: "demo", ChannelID: "ch-1"},
	}, &sent)

	svc.handleStopCommand(&discordgo.MessageCreate{Message: &discordgo.Message{ChannelID: "ch-1"}}, nil)
	joined := strings.Join(sent, "\n")
	if !strings.Contains(joined, "Stopping") {
		t.Fatalf("expected 'Stopping' message, got %v", sent)
	}
}

func TestHandleRedirectCommand_EmptyInstruction(t *testing.T) {
	h := ai.NewHandle("/proj")
	t.Cleanup(swapOrch(t, map[string]*ai.OrchestratorHandle{"/proj": h}))

	var sent []string
	svc := newTestServiceForControl(map[string]ProjectBinding{
		"/proj": {ProjectPath: "/proj", RepoName: "demo", ChannelID: "ch-1"},
	}, &sent)

	// args resolves to channel-bound project but empty instruction after trim
	svc.handleRedirectCommand(
		&discordgo.MessageCreate{Message: &discordgo.Message{ChannelID: "ch-1"}},
		[]string{"   "},
	)
	joined := strings.Join(sent, "\n")
	if !strings.Contains(joined, "Instruction is required") {
		t.Fatalf("expected 'Instruction is required' message, got %v", sent)
	}
}

func TestHandleRedirectCommand_Success(t *testing.T) {
	h := ai.NewHandle("/proj")
	t.Cleanup(swapOrch(t, map[string]*ai.OrchestratorHandle{"/proj": h}))

	var sent []string
	svc := newTestServiceForControl(map[string]ProjectBinding{
		"/proj": {ProjectPath: "/proj", RepoName: "demo", ChannelID: "ch-1"},
	}, &sent)

	svc.handleRedirectCommand(
		&discordgo.MessageCreate{Message: &discordgo.Message{ChannelID: "ch-1"}},
		[]string{"focus", "on", "login"},
	)
	joined := strings.Join(sent, "\n")
	if !strings.Contains(joined, "redirect") && !strings.Contains(joined, "Queued") {
		t.Fatalf("expected redirect confirmation, got %v", sent)
	}
}

func TestHandlePlanCommand_BindingNotFound(t *testing.T) {
	var sent []string
	svc := newTestServiceForControl(map[string]ProjectBinding{
		"/proj": {ProjectPath: "/proj", RepoName: "demo", ChannelID: "ch-1"},
	}, &sent)

	// "unknown-ch" has no binding → "Project not found"
	svc.handlePlanCommand(&discordgo.MessageCreate{Message: &discordgo.Message{ChannelID: "unknown-ch"}}, nil)
	joined := strings.Join(sent, "\n")
	if !strings.Contains(joined, "Project not found") {
		t.Fatalf("expected 'Project not found' message, got %v", sent)
	}
}

func TestHandlePlanCommand_TruncatedPlan(t *testing.T) {
	var sent []string
	tmp := t.TempDir()
	os.MkdirAll(filepath.Join(tmp, ".engine"), 0o755) //nolint:errcheck

	// Write a plan larger than maxDiscordMessageChars.
	bigPlan := strings.Repeat("x", maxDiscordMessageChars+100)
	os.WriteFile(filepath.Join(tmp, ".engine", "plan.md"), []byte(bigPlan), 0o644) //nolint:errcheck

	svc := newTestServiceForControl(map[string]ProjectBinding{
		tmp: {ProjectPath: tmp, RepoName: "demo", ChannelID: "ch-1"},
	}, &sent)
	svc.handlePlanCommand(&discordgo.MessageCreate{Message: &discordgo.Message{ChannelID: "ch-1"}}, nil)
	joined := strings.Join(sent, "\n")
	if !strings.Contains(joined, "truncated") {
		t.Fatalf("expected truncation marker, got length=%d msg=%q", len(joined), joined[:min(100, len(joined))])
	}
}

func TestApplyPauseToOrchestrator_Pause(t *testing.T) {
	// Zero-value OrchestratorHandle is fine for Pause/Resume (uses only redirectMu).
	h := new(ai.OrchestratorHandle)
	t.Cleanup(swapOrch(t, map[string]*ai.OrchestratorHandle{"/proj": h}))

	svc := &Service{cfg: Config{CommandPrefix: "!"}}
	// Should not panic.
	svc.applyPauseToOrchestrator("/proj", true)
}

func TestApplyPauseToOrchestrator_Resume(t *testing.T) {
	h := new(ai.OrchestratorHandle)
	t.Cleanup(swapOrch(t, map[string]*ai.OrchestratorHandle{"/proj": h}))

	svc := &Service{cfg: Config{CommandPrefix: "!"}}
	svc.applyPauseToOrchestrator("/proj", false)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
