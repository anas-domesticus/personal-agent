# Architecture

A personal agent for work and home, written in Go. Postgres is its long-term memory.

This document describes what exists now. It grows as the system does.

## Stack

| Concern | Choice |
|---|---|
| Storage | Postgres 17 + pgvector, `pgx`, `sqlc`, `goose` |
| LLM | [Eino](https://github.com/cloudwego/eino) components over [OpenRouter](https://openrouter.ai) |
| Jobs | [River](https://riverqueue.com) (Postgres-backed queue) |
| Embeddings | `openai/text-embedding-3-small` via OpenRouter, 1536 dimensions |

## Data model

```
agents ──< agent_versions          (prompt, model, tools, config, output_schema; immutable)
   └──< conversations ──< messages (one Eino schema.Message per row)
workflows ──< workflow_versions    (DAG as JSONB; immutable)
                 └──< runs ──< run_steps (agent + version used, input, output, transcript, tokens)
memories
```

- **Versioning.** A change to an agent or workflow is a new version row. `current_version` on the parent points at the one in use, and a foreign key stops it pointing at a version that doesn't exist.
- **Workflow graphs** are JSONB: `{"nodes": [{"id", "agent", "instruction"?}], "edges": [{"from", "to"}]}`. Nodes refer to agents by name and run the agent's current version at the moment the node runs. `internal/workflow` rejects cycles, duplicate or reserved ids and dangling edges; the catalog also checks every agent exists.
- **Structured data between nodes.** A node's input is `{"input": <workflow input>, "<upstream id>": <its output>, ...}`. A node's output matches its agent version's `output_schema`, or is `{"text": "..."}` when there is none. The workflow's output is each sink node's output keyed by id. Outputs are validated against the schema; a node gets one correction attempt before the run fails.
- **Admin flag.** `agents.admin` is set only in code (`EnsureAgent`), never by a tool.
- **Search.** Workflows (name + description only) and memories have an `embedding` and a generated full-text `search` column. Search queries combine both rankings with reciprocal rank fusion. Embeddings can be null, so rows are saved first and embedded afterwards by River jobs (see below). `embedding_model` records which model made each vector, so a model change can be re-embedded.

## Agents and tools

- `internal/catalog` is the write and read path. It validates input, wraps writes in transactions and queues embed jobs in the same transaction.
- `internal/tools` is the registry; each set of tools is a submodule: `clock`, `memory`, `admin` (agents, workflows, runs), with shared helpers in `kit`. **General tools** (`current_time`, `search_memories`, `remember`) are opted into by listing their names on an agent version. **Admin tools** cannot be listed by name; they are injected only when the agent has the admin flag. Tool errors are returned to the model as `{"ok": false, "error": ...}` so it can correct itself.
- `internal/agent` builds an Eino `ChatModelAgent` from an agent's current version: its OpenRouter model, its tools, plus the admin tools if flagged.
- The **admin agent** (`admin`) is the one the TUI will talk to. It is seeded on first start with the prompt in `internal/agent/admin_prompt.md` and the model from `ADMIN_MODEL` (default `deepseek/deepseek-v4.1-flash`). Later versions, including ones it writes itself, are left alone.

## Conversations

Eino's ADK runner is stateless: each turn takes the full history. `internal/chat` stores it in `conversations`/`messages`, one `schema.Message` per row so tool calls round-trip exactly. The user message is saved first; the agent's messages are saved together only when the turn succeeds, so a failed turn never leaves a dangling tool call.

## Runs

`run_workflow` (admin tool) or `Catalog.StartRun` writes a `runs` row and a River `run_workflow` job in one transaction. The runner (`internal/runner`) executes nodes one at a time in dependency order, records each as a `run_steps` row, and stops at the first failed node. Runs are not retried automatically, because nodes may have side effects.

## Embeddings

Embeddings are filled in by River jobs on the `embeddings` queue (`internal/embed`):

- `embed` computes one row's vector. It writes only if the row's text still matches what was embedded, so an edit made mid-job is never overwritten by a stale vector.
- `embed_sweep` runs on start and every 5 minutes. It queues `embed` jobs for rows with no embedding, or one from a different model.

Only processes with `OPENROUTER_API_KEY` set work this queue.

## Startup

`agentd` applies pending migrations on start (embedded in the binary, guarded by an advisory lock), then opens the connection pool, applies River's own migrations, seeds the admin agent if missing and starts the job workers. With an OpenRouter key it also builds the admin agent once to catch a broken version early.

## Development

Requires Go, Docker and `task`.

```
cp .env.example .env    # add OPENROUTER_API_KEY
task up                 # Postgres
task run                # migrates, then runs agentd
task test               # integration tests use testcontainers
```
