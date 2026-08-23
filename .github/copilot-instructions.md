# Engine — Project Instructions

## Core Rules

- Follow CS2420 and CS3500 principles on every change: clear responsibilities, validated inputs, deterministic behavior, no swallowed errors, no dead code, no warning debt, and tests for reachable behavior.
- Keep documentation current. If code behavior changes, update the user-facing docs and any supporting test map in the same change set.
- Prefer the smallest correct edit. Remove outdated, duplicate, or implementation-only documentation instead of layering more text onto it.
- Validate with the real project checks, not guesswork. If a command or test exists for the change, run it.

## Project Contract

- Read [.github/WORKING_BEHAVIORS.md](.github/WORKING_BEHAVIORS.md) before making behavior changes.
- Keep [.github/WORKING_BEHAVIORS.md](.github/WORKING_BEHAVIORS.md) user-facing and implementation-free.
- Keep [.github/working-behaviors-test-map.json](.github/working-behaviors-test-map.json) aligned with every non-`(IN PROGRESS)` heading in WORKING_BEHAVIORS.md.
- If WORKING_BEHAVIORS.md changes, report that change to the user and sync Obsidian when required by the repo instructions.

## Engineering Constraints

- Use `gsh strict lint` for linting in this repo. `pnpm lint` is a no-op stub (exits 0 always) and must not be used as evidence of clean lint.
- Coverage thresholds: Client Vitest 100%; Go total 100.0%. Both are enforced by the mandatory gate.
- Type reference: [.github/CLIENT_MODULE_REFERENCE.md](.github/CLIENT_MODULE_REFERENCE.md) (generated — do not edit by hand).
- Use `message:` on checkpoint calls when a checkpoint is needed.
- Keep autonomous clone paths outside the repo unless the user explicitly asks otherwise.
- Keep screenshot artifacts outside the repository tree.

## Architecture Pointers

- Client: React + TypeScript + Vite + Tailwind in `packages/client`.
- Server: Go WebSocket service in `packages/server-go`.
- Desktop shell: Tauri in `packages/desktop-tauri`.
- Shared types: TypeScript in `packages/shared`.
- The living architecture reference is [obsidian-vault/Engine/Architecture.md](obsidian-vault/Engine/Architecture.md).

<!-- BEGIN GENERATED:MODULE_MAP -->
| Package | Location | Language |
|---------|----------|----------|
| `@engine/client` | `packages/client` | TypeScript |
| `engine (crate)` | `packages/desktop-tauri` | Rust |
| `github.com/engine/server` | `packages/server-go` | Go |
| `@engine/shared` | `packages/shared` | TypeScript |
<!-- END GENERATED:MODULE_MAP -->

## Commands

<!-- BEGIN GENERATED:COMMANDS -->
| Script | Command |
|--------|---------|
| `build` | `pnpm --filter @engine/shared build && pnpm --filter @engine/client build` |
| `build:desktop-debug` | `node ./scripts/run-cargo.mjs build --bin engine` |
| `build:go` | `node ./scripts/build-go.mjs` |
| `build:go-dev` | `node ./scripts/build-go.mjs --dev --run` |
| `build:go-watch` | `node ./scripts/build-go.mjs --dev --run --watch` |
| `build:tauri` | `pnpm --filter @engine/shared build && pnpm build:go && pnpm --filter @engine/client build && pnpm tauri:build` |
| `check:context-freshness` | `node ./scripts/gen-context-docs.mjs --check` |
| `check:desktop` | `node ./scripts/run-cargo.mjs check` |
| `coverage:all` | `node ./scripts/coverage-all.mjs` |
| `desktop:install` | `bash ./scripts/engine-desktop.sh install` |
| `desktop:reinstall` | `bash ./scripts/engine-desktop.sh reinstall` |
| `dev` | `concurrently -k -n server,client -c blue,green "pnpm build:go-watch" "pnpm --filter @engine/client dev"` |
| `dev:desktop` | `pnpm dev:tauri` |
| `dev:tauri` | `pnpm tauri:dev` |
| `gen:context-docs` | `node ./scripts/gen-context-docs.mjs` |
| `install:all` | `pnpm install` |
| `lint` | `node -e "console.log('Lint disabled in npm scripts. Use gsh strict lint.')"` |
| `llama:fleet:logs` | `bash ./scripts/llama-fleet.sh logs` |
| `llama:fleet:start` | `bash ./scripts/llama-fleet.sh start` |
| `llama:fleet:status` | `bash ./scripts/llama-fleet.sh status` |
| `llama:fleet:stop` | `bash ./scripts/llama-fleet.sh stop` |
| `smoke:system` | `node ./scripts/ci-system-smoke.mjs` |
| `sync:obsidian` | `node ./scripts/sync-obsidian-memory.mjs` |
| `tauri:build` | `node ./scripts/run-cargo.mjs tauri build` |
| `tauri:dev` | `node ./scripts/run-cargo.mjs tauri dev` |
| `test:all` | `pnpm test:client && pnpm test:go && pnpm test:rust` |
| `test:all-parallel` | `concurrently -k -n client,go,rust -c green,blue,red "pnpm test:client" "pnpm test:go" "pnpm test:rust"` |
| `test:client` | `pnpm --filter @engine/client test:coverage` |
| `test:go` | `mkdir -p ./.cache/coverage/go && cd packages/server-go && go test ./... -coverprofile=../../.cache/coverage/go/coverage.out -covermode=atomic` |
| `test:go-changed` | `node ./scripts/test-go-changed.mjs` |
| `test:model-capabilities` | `node ./scripts/model-capability-suite.mjs` |
| `test:rust` | `node ./scripts/run-cargo.mjs test` |
| `typecheck` | `pnpm -r typecheck` |
| `verify:agent-completion` | `node ./scripts/agent-completion-gate.mjs` |
<!-- END GENERATED:COMMANDS -->

## Documentation Maintenance

- Update [obsidian-vault/Engine/Knowledge.md](obsidian-vault/Engine/Knowledge.md) when a real design or architecture decision needs to be preserved.
- Update [obsidian-vault/Engine/Progress Log.md](obsidian-vault/Engine/Progress Log.md) when a meaningful milestone lands.
- Prefer deleting obsolete docs over preserving contradictory history in the repo.
