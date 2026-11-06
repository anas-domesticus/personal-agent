# Architecture

A personal agent for work and home, written in Go. Postgres is its long-term memory.

This document describes what exists now. It grows as the system does.

## Stack

| Concern | Choice |
|---|---|
| Storage | Postgres 17 + pgvector, `pgx`, `sqlc`, `goose` |
| LLM | [Eino](https://github.com/cloudwego/eino) components over [OpenRouter](https://openrouter.ai) |
| Jobs | [River](https://riverqueue.com) (Postgres-backed queue) |
| Home | Zigbee2MQTT over MQTT (broker running; not yet wired in) |
| Embeddings | `openai/text-embedding-3-small` via OpenRouter, 1536 dimensions |

## Data model

```
agents ──< agent_versions          (prompt, model, tools, config; immutable)
workflows ──< workflow_versions    (DAG as JSONB; immutable)
memories
```

- **Versioning.** A change to an agent or workflow is a new version row. `current_version` on the parent points at the one in use, and a foreign key stops it pointing at a version that doesn't exist.
- **Workflow graphs** are JSONB: `{"nodes": [{"id", "agent", ...}], "edges": [["from", "to"]]}`. Nodes refer to agents by name and run the agent's current version. Go will check that the graph has no cycles when a version is saved; Postgres only checks that it is an object.
- **Search.** Workflows (name + description only) and memories have an `embedding` and a generated full-text `search` column. Search queries combine both rankings with reciprocal rank fusion. Embeddings can be null, so rows are saved first and embedded afterwards by River jobs (see below). `embedding_model` records which model made each vector, so a model change can be re-embedded.

## Embeddings

Embeddings are filled in by River jobs on the `embeddings` queue (`internal/embed`):

- `embed` computes one row's vector. It writes only if the row's text still matches what was embedded, so an edit made mid-job is never overwritten by a stale vector.
- `embed_sweep` runs on start and every 5 minutes. It queues `embed` jobs for rows with no embedding, or one from a different model.

Only processes with `OPENROUTER_API_KEY` set work this queue.

## Startup

`agentd` applies pending migrations on start (embedded in the binary, guarded by an advisory lock), then opens the connection pool, applies River's own migrations and starts the job workers.

## Development

Requires Go, Docker and `task`.

```
cp .env.example .env    # add OPENROUTER_API_KEY
task up                 # Postgres + Mosquitto
task run                # migrates, then runs agentd
task test               # integration tests use testcontainers
```
