# Discord Control Plane (Private Server)

Engine can expose a private Discord bot so you can control project sessions from anywhere.

## Source Of Truth

Discord no longer needs to be configured only through shell env vars.

Primary config lives in the project at:

- `.engine/discord.json`

A checked-in template lives at:

- `.engine/discord.example.json`

The live file `.engine/discord.json` is git-ignored because it can contain secrets.

## Security Model

- Private guild only: `guildId` is required.
- User allowlist only: `allowedUserIds` is required.
- Bot ignores all other guilds and users.
- High-risk approvals remain blocked in Discord command mode.

## Project Config File

Copy the example or edit the live file directly:

```json
{
  "enabled": true,
  "botToken": "your bot token",
  "guildId": "your private server id",
  "allowedUserIds": ["your discord user id"],
  "commandPrefix": "!",
  "controlChannelName": "engine-control"
}
```

How to find the IDs:

1. In Discord, enable Developer Mode.
2. Right-click your server and copy ID for `guildId`.
3. Right-click your Discord user and copy ID for `allowedUserIds`.

## Optional Env Overrides

Env vars still work, but now they override the project file instead of being the only setup path.

- `ENGINE_DISCORD`
- `ENGINE_DISCORD_BOT_TOKEN`
- `ENGINE_DISCORD_GUILD_ID`
- `ENGINE_DISCORD_ALLOWED_USER_IDS`
- `ENGINE_DISCORD_PREFIX`
- `ENGINE_DISCORD_CONTROL_CHANNEL`
- `ENGINE_STATE_DIR`

Use them only if you want temporary overrides.

## Discord Bot Setup

1. Create an application in the Discord Developer Portal.
2. Add a bot user.
3. In Bot settings, enable `Message Content Intent`.
4. Invite the bot to your private server with these permissions:
	- View Channels
	- Send Messages
	- Read Message History
	- Manage Channels
	- Create Public Threads
5. Paste the bot token into `.engine/discord.json`.

## Startup

Start Engine normally. The server auto-loads `.engine/discord.json`.

Examples:

- `pnpm dev`
- `pnpm dev:desktop`
- `pnpm build:go-dev`

If `enabled` is `true` and the config is valid, the Discord service starts automatically.

## How To Test It

Fast manual test:

1. Fill in `.engine/discord.json`.
2. Start the app with `pnpm dev` or `pnpm dev:desktop`.
3. In your private Discord server, confirm `engine-control` appears.
4. In that channel, run `!help`.
5. Enroll the current repo:
	- `!project add /Users/alexwaldmann/Desktop/MyEditor`
6. Open the new project channel and run:
	- `!status`
	- `!sessions`
	- `!lastcommit`
7. Ask the agent something small first:
	- `!ask summarize this project in 5 lines`
8. Pause and resume:
	- `!pause`
	- `!resume`

Expected results:

- Bot replies only in your configured guild.
- Bot ignores users not in `allowedUserIds`.
- Project channel is created once and reused.
- `!ask` returns output in message chunks.
- Pause state survives restart.

## Commands

General:

- `!help`
- `!project add <path>`
- `!project list`
- `!project remove <name>`

Project control:

- `!status [project]`
- `!sessions [project]`
- `!lastcommit [project]`
- `!pause [project]`
- `!resume [project]`
- `!ask <prompt>` (inside a project channel)
- `!ask <project> <prompt>` (from control channel)

## Behavior

- Each enrolled project gets one Discord text channel (`proj-<repo-name>`).
- Commands also work in threads under that project channel.
- `!ask` runs Engine AI against that project and posts streamed output in message chunks.
- One active `!ask` per project at a time.
- Project pause state is persisted.

## Persistent State

Stored at:

- `.engine/discord-state.json` by default
- or `${ENGINE_STATE_DIR}/discord-state.json` if you override the state dir

Includes:

- Control channel ID
- Project path to channel mapping
- Per-project paused state
