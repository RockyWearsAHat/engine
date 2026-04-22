# Discord Control Plane (Private Server)

Engine can expose a private Discord bot so you can control project sessions from anywhere.

## Security Model

- Private guild only: `ENGINE_DISCORD_GUILD_ID` is required.
- User allowlist only: `ENGINE_DISCORD_ALLOWED_USER_IDS` is required.
- Bot ignores all other guilds and users.
- High-risk approvals remain blocked in Discord command mode.

## Environment Variables

Set these before starting `packages/server-go`:

- `ENGINE_DISCORD=1`
- `ENGINE_DISCORD_BOT_TOKEN=<discord-bot-token>`
- `ENGINE_DISCORD_GUILD_ID=<your-private-server-id>`
- `ENGINE_DISCORD_ALLOWED_USER_IDS=<id1,id2>`

Optional:

- `ENGINE_DISCORD_PREFIX=!`
- `ENGINE_DISCORD_CONTROL_CHANNEL=engine-control`
- `ENGINE_STATE_DIR=<custom-state-dir>`

## Startup

Start Engine server normally. The Discord service auto-starts when `ENGINE_DISCORD=1`.

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
- `!ask` runs Engine AI against that project and posts streamed output in message chunks.
- One active `!ask` per project at a time.
- Project pause state is persisted.

## Persistent State

Stored at:

- `${ENGINE_STATE_DIR:-$HOME/.config/Engine}/discord/control-plane.json`

Includes:

- Control channel ID
- Project path to channel mapping
- Per-project paused state
