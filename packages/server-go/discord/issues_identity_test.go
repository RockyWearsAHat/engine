package discord

import (
	"fmt"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
)

func captureDiscordSends(s *Service) *[]string {
	out := []string{}
	s.testSendHook = func(_ string, msg string) {
		out = append(out, msg)
	}
	return &out
}

func TestHandleIdentityCommand_ShowsResolvedIdentity(t *testing.T) {
	svc, _ := newDisabledSvc(t)
	sent := captureDiscordSends(svc)

	oldLogin := githubEngineLoginFn
	oldToken := githubEngineTokenFn
	oldProject := githubProjectNumberFn
	defer func() {
		githubEngineLoginFn = oldLogin
		githubEngineTokenFn = oldToken
		githubProjectNumberFn = oldProject
	}()

	githubEngineLoginFn = func() string { return "engine-bot" }
	githubEngineTokenFn = func() string { return "tok123" }
	githubProjectNumberFn = func() int { return 12 }

	svc.handleIdentityCommand(&discordgo.MessageCreate{Message: &discordgo.Message{ChannelID: "ch"}})
	if len(*sent) == 0 {
		t.Fatal("expected identity message")
	}
	msg := (*sent)[0]
	if !strings.Contains(msg, "@engine-bot") || !strings.Contains(msg, "Project board") {
		t.Fatalf("unexpected identity payload: %q", msg)
	}
}

func TestHandleIssuesCommand_NoIdentity(t *testing.T) {
	svc, _ := newDisabledSvc(t)
	sent := captureDiscordSends(svc)

	oldLogin := githubEngineLoginFn
	defer func() { githubEngineLoginFn = oldLogin }()
	githubEngineLoginFn = func() string { return "" }

	svc.handleIssuesCommand(&discordgo.MessageCreate{Message: &discordgo.Message{ChannelID: "ch"}}, nil)
	if len(*sent) == 0 || !strings.Contains((*sent)[0], "identity not configured") {
		t.Fatalf("unexpected message: %#v", *sent)
	}
}

func TestHandleIssuesCommand_ListsAssignedIssues(t *testing.T) {
	svc, _ := newDisabledSvc(t)
	sent := captureDiscordSends(svc)

	svc.stateMu.Lock()
	svc.state.Projects["/tmp/proj"] = ProjectBinding{ProjectPath: "/tmp/proj", RepoName: "https://github.com/octo/demo.git"}
	svc.stateMu.Unlock()

	oldLogin := githubEngineLoginFn
	oldList := githubListAssignedFn
	oldRemote := gitRemoteOriginFn
	defer func() {
		githubEngineLoginFn = oldLogin
		githubListAssignedFn = oldList
		gitRemoteOriginFn = oldRemote
	}()

	githubEngineLoginFn = func() string { return "engine-bot" }
	gitRemoteOriginFn = func(repoPath string) (string, error) {
		return "https://github.com/octo/demo.git", nil
	}
	githubListAssignedFn = func(owner, repo, login string) ([]githubIssue, error) {
		if owner != "octo" || repo != "demo" || login != "engine-bot" {
			return nil, fmt.Errorf("unexpected args: %s/%s %s", owner, repo, login)
		}
		return []githubIssue{{
			Number:  7,
			Title:   "Fix issue",
			HTMLURL: "https://github.com/octo/demo/issues/7",
			Labels:  []struct{ Name string }{{Name: "bug"}},
		}}, nil
	}

	svc.handleIssuesCommand(&discordgo.MessageCreate{Message: &discordgo.Message{ChannelID: "ch"}}, nil)
	if len(*sent) == 0 {
		t.Fatal("expected issues message")
	}
	msg := strings.Join(*sent, "\n")
	if !strings.Contains(msg, "#7") || !strings.Contains(msg, "Fix issue") {
		t.Fatalf("unexpected issues output: %q", msg)
	}
}
