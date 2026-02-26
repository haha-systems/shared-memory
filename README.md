# memory-mcp

Shared-memory MCP server for multi-agent workflows (Codex, Claude, Gemini).

## Features (v1)
- MCP stdio server with tools:
  - `memory_write`
  - `memory_search`
  - `memory_get_context_pack`
  - `memory_promote`
- SQLite persistence with WAL mode.
- FTS5 lexical retrieval (fallback to LIKE when unavailable).
- Short/long memory scopes with TTL cleanup for short-term memory.
- One-command CLI bootstrap for Codex/Claude/Gemini MCP registration.
- Optional local admin TUI powered by Bubble Tea.

## Quickstart
```bash
go run ./cmd/memory-mcp serve --config ./config/memory-mcp.yaml
```

In another terminal, bootstrap agent CLIs:
```bash
./scripts/bootstrap-clis.sh --dry-run
./scripts/bootstrap-clis.sh
```
This registers `scripts/serve-stdio.sh` as the MCP launch command so setup works even before installing `memory-mcp` globally.

## Commands
- `memory-mcp serve --config <path>`
- `memory-mcp bootstrap-clis --config <path> --all --scope user|project [--serve-command \"...\"] [--dry-run]`
- `memory-mcp admin --config <path>`
- `memory-mcp version`

## Prompt Templates
Use the built-in prompt templates to make agents consistently read/write shared memory:
- `prompts/agent_system_prompt.txt`
- `prompts/agent_task_prompt_template.txt`

Recommended pattern:
1. Load `prompts/agent_system_prompt.txt` as your system instruction.
2. Fill in `prompts/agent_task_prompt_template.txt` values (`{{namespace}}`, `{{goal}}`, etc.).
3. Require the agent to call:
   - `memory_get_context_pack` + `memory_search` at task start
   - `memory_write` at task end
   - `memory_promote` for durable decisions

Namespace convention:
- `<org>/<repo>/<branch>/<workstream>`
- Example: `acme/ghost/main/build-loop`

## Config
Default config file: `config/memory-mcp.yaml`

Important fields:
- `db_path`: SQLite DB location (supports `~/...`)
- `namespace_pattern`: required namespace regex
- `default_short_ttl_hours`
- `ttl_check_interval_seconds`
- `max_context_pack_items`
- `default_search_k`

## Notes
- v1 defers vector embeddings/reranking to v2.
- Shared context works across agents through a shared SQLite database path.
