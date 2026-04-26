package ws

import (
	"strings"

	"github.com/engine/server/discord"
)

// discordServiceStartFn is injectable for testing the Start() success/failure paths.
var discordServiceStartFn = func(s *discord.Service) error { return s.Start() }

// discordConfigPayload is the wire shape used by discord.config.get/set.
// Secrets are sent masked unless explicitly requested with reveal=true.
type discordConfigPayload struct {
	Enabled            bool     `json:"enabled"`
	BotToken           string   `json:"botToken"`
	BotTokenMasked     string   `json:"botTokenMasked,omitempty"`
	GuildID            string   `json:"guildId"`
	AllowedUserIDs     []string `json:"allowedUserIds"`
	CommandPrefix      string   `json:"commandPrefix"`
	ControlChannelName string   `json:"controlChannelName"`
	HasToken           bool     `json:"hasToken"`
}

func maskToken(raw string) string {
	t := strings.TrimSpace(raw)
	if t == "" {
		return ""
	}
	if len(t) <= 8 {
		return "•••"
	}
	return t[:4] + "…" + t[len(t)-4:]
}

func toPayload(cfg discord.Config) discordConfigPayload {
	allowed := make([]string, 0, len(cfg.AllowedUsers))
	for id := range cfg.AllowedUsers {
		allowed = append(allowed, id)
	}
	return discordConfigPayload{
		Enabled:            cfg.Enabled,
		BotToken:           "",
		BotTokenMasked:     maskToken(cfg.BotToken),
		GuildID:            cfg.GuildID,
		AllowedUserIDs:     allowed,
		CommandPrefix:      cfg.CommandPrefix,
		ControlChannelName: cfg.ControlChannelName,
		HasToken:           strings.TrimSpace(cfg.BotToken) != "",
	}
}

func fromPayload(p discordConfigPayload, existing discord.Config) discord.Config {
	out := existing
	out.Enabled = p.Enabled
	if strings.TrimSpace(p.BotToken) != "" {
		out.BotToken = strings.TrimSpace(p.BotToken)
	}
	if strings.TrimSpace(p.GuildID) != "" {
		out.GuildID = strings.TrimSpace(p.GuildID)
	}
	if p.AllowedUserIDs != nil {
		out.AllowedUsers = map[string]bool{}
		for _, id := range p.AllowedUserIDs {
			if v := strings.TrimSpace(id); v != "" {
				out.AllowedUsers[v] = true
			}
		}
	}
	if strings.TrimSpace(p.CommandPrefix) != "" {
		out.CommandPrefix = strings.TrimSpace(p.CommandPrefix)
	}
	if strings.TrimSpace(p.ControlChannelName) != "" {
		out.ControlChannelName = strings.TrimSpace(p.ControlChannelName)
	}
	return out
}

func sameStringSet(a map[string]bool, b map[string]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for key := range a {
		if !b[key] {
			return false
		}
	}
	return true
}

func sameDiscordRuntimeConfig(a discord.Config, b discord.Config) bool {
	if a.Enabled != b.Enabled {
		return false
	}
	if strings.TrimSpace(a.BotToken) != strings.TrimSpace(b.BotToken) {
		return false
	}
	if strings.TrimSpace(a.GuildID) != strings.TrimSpace(b.GuildID) {
		return false
	}
	if strings.TrimSpace(a.CommandPrefix) != strings.TrimSpace(b.CommandPrefix) {
		return false
	}
	if strings.TrimSpace(a.ControlChannelName) != strings.TrimSpace(b.ControlChannelName) {
		return false
	}
	return sameStringSet(a.AllowedUsers, b.AllowedUsers)
}

func (c *conn) handleDiscordConfigGet() {
	if discordBridge == nil {
		// Still return the on-disk config so the UI can show values even if
		// the service failed to start.
		cfg, _ := discord.LoadConfig(c.projectPath)
		c.send(map[string]interface{}{
			"type":   "discord.config",
			"config": toPayload(cfg),
			"active": false,
		})
		return
	}
	active := discordBridge.CurrentConfig().Enabled
	c.send(map[string]interface{}{
		"type":   "discord.config",
		"config": toPayload(discordBridge.CurrentConfig()),
		"active": active,
	})
}

func (c *conn) handleDiscordConfigSet(payload discordConfigPayload) {
	var existing discord.Config
	hadBridge := discordBridge != nil
	if discordBridge != nil {
		existing = discordBridge.CurrentConfig()
	} else {
		existing, _ = discord.LoadConfig(c.projectPath)
	}
	cfg := fromPayload(payload, existing)

	if err := discord.WriteConfig(c.projectPath, cfg); err != nil {
		c.sendErr("Write config failed: "+err.Error(), "DISCORD_WRITE")
		return
	}

	active := false
	if hadBridge && sameDiscordRuntimeConfig(existing, cfg) {
		active = discordBridge.CurrentConfig().Enabled
		c.send(map[string]interface{}{
			"type":   "discord.config.saved",
			"config": toPayload(cfg),
			"active": active,
		})
		return
	}

	if discordBridge != nil {
		if err := discordBridge.Reload(cfg); err != nil {
			c.send(map[string]interface{}{
				"type":    "discord.config.saved",
				"config":  toPayload(cfg),
				"active":  false,
				"warning": "Saved, but reload failed: " + err.Error(),
			})
			return
		}
		active = discordBridge.CurrentConfig().Enabled
	} else if cfg.Enabled {
		service, err := discord.NewService(cfg, c.projectPath)
		if err != nil {
			c.send(map[string]interface{}{
				"type":    "discord.config.saved",
				"config":  toPayload(cfg),
				"active":  false,
				"warning": "Saved, but service init failed: " + err.Error(),
			})
			return
		}
		if err := discordServiceStartFn(service); err != nil {
			c.send(map[string]interface{}{
				"type":    "discord.config.saved",
				"config":  toPayload(cfg),
				"active":  false,
				"warning": "Saved, but service start failed: " + err.Error(),
			})
			return
		}
		SetDiscordBridge(service)
		active = true
	}
	c.send(map[string]interface{}{
		"type":   "discord.config.saved",
		"config": toPayload(cfg),
		"active": active,
	})
}

func (c *conn) handleDiscordUnlink(leaveGuild bool) {
	var cfg discord.Config
	if discordBridge != nil {
		cfg = discordBridge.CurrentConfig()
	} else {
		cfg, _ = discord.LoadConfig(c.projectPath)
	}

	warning := ""
	if leaveGuild && discordBridge != nil {
		if leaver, ok := discordBridge.(interface{ LeaveGuild(string) error }); ok {
			if err := leaver.LeaveGuild(cfg.GuildID); err != nil {
				warning = "Unlinked, but guild leave failed: " + err.Error()
			}
		}
	}

	cfg.Enabled = false
	cfg.GuildID = ""

	if err := discord.WriteConfig(c.projectPath, cfg); err != nil {
		c.sendErr("Write config failed: "+err.Error(), "DISCORD_WRITE")
		return
	}

	active := false
	if discordBridge != nil {
		if err := discordBridge.Reload(cfg); err != nil {
			if warning == "" {
				warning = "Unlinked, but reload failed: " + err.Error()
			}
		} else {
			active = discordBridge.CurrentConfig().Enabled
		}
	}

	out := map[string]interface{}{
		"type":   "discord.config.saved",
		"config": toPayload(cfg),
		"active": active,
	}
	if warning != "" {
		out["warning"] = warning
	}
	c.send(out)
}

func (c *conn) handleDiscordValidate(override *discordConfigPayload) {
	var cfg discord.Config
	if override != nil {
		var base discord.Config
		if discordBridge != nil {
			base = discordBridge.CurrentConfig()
		} else {
			base, _ = discord.LoadConfig(c.projectPath)
		}
		cfg = fromPayload(*override, base)
	} else if discordBridge != nil {
		cfg = discordBridge.CurrentConfig()
	} else {
		cfg, _ = discord.LoadConfig(c.projectPath)
	}
	result := discord.Validate(cfg)
	c.send(map[string]interface{}{
		"type":   "discord.validate.result",
		"result": result,
	})
}

func (c *conn) handleDiscordHistorySearch(projectPath, query, since string, limit int) {
	if discordBridge == nil {
		c.sendErr("Discord service not running", "DISCORD_UNAVAILABLE")
		return
	}
	pp := strings.TrimSpace(projectPath)
	if pp == "" {
		pp = c.projectPath
	}
	hits, err := discordBridge.SearchHistory(pp, query, since, limit)
	if err != nil {
		c.sendErr(err.Error(), "DISCORD_SEARCH")
		return
	}
	c.send(map[string]interface{}{
		"type":  "discord.history.results",
		"query": query,
		"hits":  hits,
	})
}

func (c *conn) handleDiscordHistoryRecent(projectPath, threadID, since string, limit int) {
	if discordBridge == nil {
		c.sendErr("Discord service not running", "DISCORD_UNAVAILABLE")
		return
	}
	pp := strings.TrimSpace(projectPath)
	if pp == "" {
		pp = c.projectPath
	}
	rows, err := discordBridge.RecentHistory(pp, threadID, since, limit)
	if err != nil {
		c.sendErr(err.Error(), "DISCORD_RECENT")
		return
	}
	c.send(map[string]interface{}{
		"type":     "discord.history.recent",
		"threadId": threadID,
		"rows":     rows,
	})
}
