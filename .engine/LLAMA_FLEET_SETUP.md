# Local llama.cpp Fleet Setup

This project now includes a local multi-instance llama.cpp setup.

## What it does

- Runs multiple `llama-server` backends at once.
- Puts a local round-robin router in front of them.
- Exposes one stable endpoint for Engine (`LLAMACPP_BASE_URL`).
- Uses server-side team selection so `engine.team.set` applies the configured
	orchestrator model for that team instead of client-supplied overrides.

## Files

- `scripts/llama-fleet.sh` - manages backend processes and router.
- `scripts/llama-router.mjs` - OpenAI-compatible round-robin proxy.
- `.engine/llama-fleet.env` - local runtime config.
- `.engine/llama-fleet.env.example` - template config.

## Start / Stop

```bash
pnpm llama:fleet:start
pnpm llama:fleet:status
pnpm llama:fleet:logs
pnpm llama:fleet:stop
```

## Engine runtime env

Set these so Engine uses the fleet router:

```bash
export ENGINE_MODEL_PROVIDER=llamacpp
export LLAMACPP_BASE_URL=http://127.0.0.1:8080
export ENGINE_MODEL=qwen2.5-1.5b
```

## Scaling

Tune in `.engine/llama-fleet.env`:

- `LLAMA_PORTS` adds or removes backend instances.
- `LLAMA_PARALLEL` increases slots per backend.
- `LLAMA_CTX`, `LLAMA_THREADS`, `LLAMA_THREADS_HTTP` tune throughput/latency.
- `LLAMA_THREADS_BATCH`, `LLAMA_BATCH`, `LLAMA_UBATCH` tune batching efficiency.
- `LLAMA_N_GPU_LAYERS`, `LLAMA_DEFRAG_THOLD` tune memory behavior.
- `LLAMA_FLASH_ATTN`, `LLAMA_MLOCK`, `LLAMA_NO_MMAP`, `LLAMA_NUMA` are optional
	low-end hardware toggles.

Recommended scaling path:

1. Raise `LLAMA_PARALLEL` first.
2. Tune `LLAMA_BATCH`/`LLAMA_UBATCH` and `LLAMA_THREADS_BATCH` for your CPU.
3. Add more ports in `LLAMA_PORTS` if requests still queue.
4. Move to a larger model once the topology is stable.
