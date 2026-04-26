---
applyTo: "**"
description: "Hard-stop completion gate: an agent must not end work until 100% coverage, clean diagnostics, CS 3500 verification, and full request/chat completion are all verified."
---

# Mandatory Completion Gate

This workspace uses a hard-stop completion gate enforced by hook.

## Non-negotiable rule

Do not complete or end a request unless the completion gate passes.

Gate command:

- `pnpm verify:agent-completion`

The hook executes this gate at `Stop`.

## Required evidence file

Before finishing, update `.github/session-memory/agent-completion-report.json` with all required fields set to true and a fresh timestamp.

This file is an internal agent/hook artifact for enforcement and telemetry. It is not user-facing documentation and should not be presented as a deliverable to the user.

Required booleans:

- `requestFullyCompleted`
- `chatHistoryReviewed`
- `cs3500PrinciplesVerified`
- `diagnosticsClean`
- `coverage100`
- `behavioralGatePassed` — set to `true` if the behavioral check passed or was skipped (Playwright not installed); set to `false` only if the script ran and detected real failures.

## Required validation sequence

1. Run lint and fix all failures.
2. Run typecheck and fix all failures.
3. Run client coverage with 100% thresholds.
4. Run Go coverage and confirm total is 100.0%.
5. Run behavioral completion check (`node scripts/behavioral-completion-check.mjs`). If Playwright is not installed the check is skipped and still counts as passed.
6. Verify CS 3500 principles were followed in the implementation.
7. Review the full request and relevant chat history for completeness.
8. Update the completion report file with accurate attestation and timestamp.

If any step fails, keep working. Do not return completion claims.

## Truthfulness rule

Never mark attestation fields true unless they were actually verified in the current request.
