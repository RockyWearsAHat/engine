package discord

import (
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
)

func TestOnMessage_EarlyReturnsAndUnknownCommand(t *testing.T) {
	svc, _ := newDisabledSvc(t)
	var sent []string
	svc.testSendHook = func(_ string, msg string) { sent = append(sent, msg) }
	svc.cfg.GuildID = "guild-1"
	svc.cfg.AllowedUsers = map[string]bool{"u-1": true}
	svc.cfg.CommandPrefix = "!"

	// nil message should return safely.
	svc.onMessage(nil, nil)

	// nil author.
	svc.onMessage(nil, &discordgo.MessageCreate{Message: &discordgo.Message{GuildID: "guild-1", ChannelID: "ch", Content: "!help"}})

	// bot author ignored.
	svc.onMessage(nil, &discordgo.MessageCreate{Message: &discordgo.Message{
		GuildID: "guild-1", ChannelID: "ch", Content: "!help",
		Author: &discordgo.User{ID: "u-1", Bot: true},
	}})

	// wrong guild ignored.
	svc.onMessage(nil, &discordgo.MessageCreate{Message: &discordgo.Message{
		GuildID: "other", ChannelID: "ch", Content: "!help",
		Author: &discordgo.User{ID: "u-1"},
	}})

	// disallowed user ignored.
	svc.onMessage(nil, &discordgo.MessageCreate{Message: &discordgo.Message{
		GuildID: "guild-1", ChannelID: "ch", Content: "!help",
		Author: &discordgo.User{ID: "u-other"},
	}})

	// unknown command should send help hint.
	svc.onMessage(nil, &discordgo.MessageCreate{Message: &discordgo.Message{
		GuildID: "guild-1", ChannelID: "ch", Content: "!mystery",
		Author: &discordgo.User{ID: "u-1"},
	}})

	joined := strings.Join(sent, "\n")
	if !strings.Contains(joined, "Unknown command") {
		t.Fatalf("expected unknown command response, got: %q", joined)
	}
}

func TestOnMessage_NonCommandStatusLikeDirectMessage(t *testing.T) {
	svc, _ := newDisabledSvc(t)
	var sent []string
	svc.testSendHook = func(_ string, msg string) { sent = append(sent, msg) }
	svc.cfg.GuildID = "guild-1"
	svc.cfg.AllowedUsers = map[string]bool{"u-1": true}
	svc.cfg.CommandPrefix = "!"

	addProject(svc, "/tmp/proj", "proj-ch", "demo")

	// Non-command message in bound project channel triggers direct-chat branch.
	// Status-like text should route to status handler and emit a status message.
	svc.onMessage(nil, &discordgo.MessageCreate{Message: &discordgo.Message{
		GuildID: "guild-1", ChannelID: "proj-ch", Content: "status?",
		Author: &discordgo.User{ID: "u-1", Username: "tester"},
	}})

	joined := strings.Join(sent, "\n")
	if !strings.Contains(joined, "status") {
		t.Fatalf("expected status output, got: %q", joined)
	}
}

func TestOnMessage_CommandAliasesCoverage(t *testing.T) {
	svc, _ := newDisabledSvc(t)
	var sent []string
	svc.testSendHook = func(_ string, msg string) { sent = append(sent, msg) }
	svc.cfg.GuildID = "guild-1"
	svc.cfg.AllowedUsers = map[string]bool{"u-1": true}
	svc.cfg.CommandPrefix = "!"

	oldLogin := githubEngineLoginFn
	oldList := githubListAssignedFn
	defer func() {
		githubEngineLoginFn = oldLogin
		githubListAssignedFn = oldList
	}()
	githubEngineLoginFn = func() string { return "engine-bot" }
	githubListAssignedFn = func(owner, repo, login string) ([]githubIssue, error) { return nil, nil }

	addProject(svc, "/tmp/proj", "proj-ch", "r")
	for _, cmd := range []string{"!stop", "!redirect add tests", "!plan", "!orch", "!issues", "!assigned", "!identity"} {
		svc.onMessage(nil, &discordgo.MessageCreate{Message: &discordgo.Message{
			GuildID: "guild-1", ChannelID: "proj-ch", Content: cmd,
			Author: &discordgo.User{ID: "u-1", Username: "tester"},
		}})
	}

	if len(sent) == 0 {
		t.Fatal("expected command responses to be sent")
	}
}
