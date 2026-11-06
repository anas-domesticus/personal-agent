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
agents ──< agent_versions          (prompt, model, config incl. tools, output_schema; immutable)
   └──< conversations ──< messages (one Eino schema.Message per row)
          └──< conversations        (subagents: parent_id, subagent spec)
workflows ──< workflow_versions    (DAG as JSONB; immutable)
                 └──< runs ──< run_steps (agent + version used, input, output, transcript, tokens)
memories
```

- **Versioning.** A change to an agent or workflow is a new version row. `current_version` on the parent points at the one in use, and a foreign key stops it pointing at a version that doesn't exist.
- **Workflow graphs** are JSONB: `{"nodes": [{"id", "agent", "instruction"?}], "edges": [{"from", "to"}]}`. Nodes refer to agents by name and run the agent's current version at the moment the node runs. `internal/workflow` rejects cycles, duplicate or reserved ids and dangling edges; the catalog also checks every agent exists.
- **Structured data between nodes.** A node's input is `{"input": <workflow input>, "<upstream id>": <its output>, ...}`. A node's output matches its agent version's `output_schema`, or is `{"text": "..."}` when there is none. The workflow's output is each sink node's output keyed by id. Outputs are validated against the schema; a node gets one correction attempt before the run fails.
- **Admin flag.** `agents.admin` is set only in code (`Agents.Ensure`), never by a tool.
- **Search.** Workflows (name + description only) and memories have an `embedding` and a generated full-text `search` column. Search queries combine both rankings with reciprocal rank fusion. Embeddings can be null, so rows are saved first and embedded afterwards by River jobs (see below). `embedding_model` records which model made each vector, so a model change can be re-embedded.

## Packages and seams

```
cmd/agentd ──> app (composition root; the TUI will use it too)
                │
   ┌────────────┼──────────────┬──────────────┬─────────────┐
 chat         runs           catalog        tools          agent
 Service      Service        Agents         Registry       Builder
 PGStore      Worker         Workflows      Toolset        Runnable
 Window       Reap           Memories       admin/ memory/ clock/ mcp/ kit/
   └────────────┴──────────────┴──── domain (shared types and errors) ────┘
                                     store (pool, migrations, sqlc), embed (jobs)
```

### Package map

| Package | Responsibility |
|---|---|
| `cmd/agentd` | Entrypoint: config, signals, graceful shutdown |
| `internal/app` | Composition root: builds every service in dependency order |
| `internal/config` | Environment settings and the MCP YAML |
| `internal/domain` | Shared types (`Agent`, `Run`, `Conversation`, `AgentConfig`, ...) and sentinel errors |
| `internal/store` | Connection pool, embedded goose migrations, `InTx` |
| `internal/store/db` | sqlc-generated queries (never hand-edited) |
| `internal/catalog` | Agents, workflows and memories: validation, versioning, search |
| `internal/agent` | Builds runnable Eino agents from versions; the subagent tool; history windowing |
| `internal/chat` | Conversations and turns (`Service`, `PGStore`) |
| `internal/runs` | Workflow runs: service, River worker, reaper |
| `internal/workflow` | Graph validation, topological order, output schemas |
| `internal/embed` | Embedding jobs and sweeps |
| `internal/llm` | OpenRouter chat models, embedder, model catalogue |
| `internal/search` | `Searcher` seam and its OpenRouter implementation |
| `internal/workspace` | `Workspace` seam for files and commands: `Local`, `Sandbox` (unimplemented) |
| `internal/tools` | Tool registry, config checks and the `Toolset` agents are built from |
| `internal/tools/kit` | Tool contract (`Spec`), settings decoding, output capping |
| `internal/tools/admin` | Admin tools: agents, workflows, runs, `list_tools` |
| `internal/tools/clock` | `current_time` |
| `internal/tools/memory` | `search_memories`, `remember` |
| `internal/tools/files` | `read`, `write`, `edit`, `glob`, `grep`, `shell` over a `Workspace` |
| `internal/tools/web` | `web_search`, `web_fetch` |
| `internal/tools/mcp` | MCP server connections and their tools |
| `internal/mocks` | Generated mocks of third-party (Eino) interfaces |
| `internal/testdb` | Throwaway pgvector Postgres via testcontainers |
| `internal/testenv` | The whole stack on `testdb`, with scripted models, for integration tests |

### Seams

- **`domain`** holds the types every layer shares (`Agent`, `AgentVersion`, `Workflow`, `Run`, `Step`, `Memory`, `Conversation`, `AgentConfig`) and the errors `ErrNotFound`, `ErrExists`, `ErrBusy`, `ErrConflict`. Services return these, never sqlc rows, so tools and UIs don't depend on query shapes.
- **Interfaces live with their consumer** (see [conventions](conventions/go.md#design)). The admin tools depend on small `Agents`, `Workflows` and `Runs` interfaces. The tool registry reaches MCP through `tools.MCPSource`. The memory tools depend on `memory.Store`. `agent.Builder` depends on `Source` and `Toolset`. `runs.Worker` depends on `Recorder` and `Agents`. `chat.Service` depends on `chat.Store` and `Agents`. The catalog checks tools through `ToolPolicy` and queues jobs through `JobInserter`. Built agents are an `agent.Runnable`, so the run worker and chat can be tested without Eino or Postgres (see `runs/worker_test.go`).
- **No late binding.** `app.New` builds in dependency order: store, an insert-only River client, MCP; then memories and the general tool registry; then agents (which validate tools against the registry), workflows and runs; then the admin tools and the full toolset; and last the builder, chat and the River client that works jobs.
- Each aggregate has one owner that writes it: the catalog for agents, workflows and memories, `runs` for runs and steps, `chat` for conversations, and `embed` for embedding columns.

## Agents and tools

- `internal/catalog` validates input, wraps writes in transactions, locks the parent row when numbering versions, and queues embed jobs in the same transaction. Agent and workflow names are lowercase letters, digits and hyphens.
- A version's **config** is strict JSON, stored compact with tools in name order. Unknown keys are rejected.

  ```json
  {"max_iterations": 30, "temperature": 0.2, "max_tokens": 4000,
   "tools": {"web_search": {"max_results": 3}, "read": true, "shell": false,
             "subagent": {"models": ["deepseek/deepseek-v4.1-flash"], "max_depth": 1},
             "mcp:composio": true}}
  ```

  - Each entry under `tools` is `true` (on, with default settings), `false` (off), or that tool's settings object. Settings are decoded strictly and validated when the version is written.
  - Tools that aren't listed are off. The exception is admin tools on an admin agent: they're on unless set to `false`, and they can never be set to `true`.
  - Every tool is a `kit.Spec`: a name, a summary, an optional settings type and a constructor. `list_tools` shows each spec with the settings it takes.
- `internal/tools` holds the tools, one submodule per set: `clock`, `memory`, `files`, `web`, `mcp`, `admin` (agents, workflows, runs), with shared helpers in `kit`. **General tools** are opted into by listing their names on an agent version. **Admin tools** can't be listed by name; they are injected only when the agent has the admin flag.
  - Tool errors are returned to the model as `{"ok": false, "error": ...}` so it can correct itself.
  - Every tool's output is capped at 20,000 characters, with a note saying it was cut. Results are replayed every turn, so one huge result would otherwise exhaust the context for good.
  - A version that lists a tool which no longer exists, or belongs to a server that is down, still builds without it. A code change can't make an agent, least of all the admin, unrunnable.
- `internal/agent` builds an Eino `ChatModelAgent` from an agent's current version: its model and settings, its tools, plus the admin tools if flagged.
- The **admin agent** (`admin`) is the one the TUI will talk to. It is seeded on first start with the prompt in `internal/agent/admin_prompt.md` and the model from `ADMIN_MODEL` (default `deepseek/deepseek-v4.1-flash`). Later versions, including ones it writes itself, are left alone.

## Tools

| Tool | What it does | Settings |
|---|---|---|
| `current_time` | The date and time | none |
| `search_memories`, `remember` | Long-term memory (its design is on hold) | none |
| `web_search` | Searches the web through a `search.Searcher` | `max_results` |
| `web_fetch` | Fetches a URL as markdown, text or HTML (up to 5MB, 120s) | none |
| `read`, `write`, `edit`, `glob`, `grep` | File access, modelled on OpenCode's tools, through the workspace | none |
| `shell` | Runs `sh -c` in the workspace (default timeout 2 minutes, maximum 10) | none |
| `subagent` | Delegates a task to a stored agent or an ad-hoc worker | `models`, `max_depth` |
| `mcp:<server>`, `<server>__<tool>` | MCP tools | none |
| admin tools | Manage agents, workflows and runs | none |

**Workspace.** Everything that touches files or runs commands goes through `workspace.Workspace`, an interface of operations: read, write, stat, list, glob, grep and exec.
- `WORKSPACE=local` (the default) acts inside `WORKSPACE_DIR`. Paths that escape it, through `..` or symlinks, are refused.
- Commands get only `PATH`, `HOME`, `USER`, `LANG`, `TMPDIR` and `TERM` from the environment, never agentd's secrets. They run in their own process group, which is killed on timeout.
- `shell` can still do anything the process can, so run agentd in a container to isolate it.
- `WORKSPACE=sandbox` selects the remote-sandbox implementation, which for now returns "unimplemented" for every operation.

**Web search** goes through `search.Searcher`. The OpenRouter implementation uses OpenRouter's built-in `web` plugin on `SEARCH_MODEL`. It returns the model's short answer plus the cited results: title, URL and snippet. Without an API key, search reports that it is unavailable.

**Subagents.** `subagent(description, prompt, agent?, model?, tools?, conversation_id?)` builds either a stored agent (optionally on another model) or an ad-hoc worker on any OpenRouter model.
- **Models.** A model must be on the version's `models` list, if it has one. It must also be on OpenRouter with tool support; that list is fetched from `/models` and cached for an hour.
- **Tools.** An ad-hoc worker gets the caller's tools, or a subset of them, with the caller's settings.
- **Limits.** Subagents never get admin tools, and nesting stops at `max_depth`, which defaults to 1.
- **Storage.** Each subagent has its own conversation, linked to the one that started it and recording how it was built, so `conversation_id` continues it. Its transcript and token counts are stored there.

## Configuration

`internal/config` loads everything: settings from the environment (`.env` via `task`; see `.env.example` for `ADMIN_MODEL`, `SEARCH_MODEL`, `WORKSPACE` and `WORKSPACE_DIR`) and MCP servers from `mcp.yaml` (or `MCP_CONFIG`). See `mcp.example.yaml`. `${VAR}` references in the MCP file are filled from the environment, so secrets stay out of it.

## MCP

`internal/tools/mcp` connects to every enabled server in parallel at startup (streamable HTTP, SSE or stdio) using Eino's MCP adapter.

- **Naming.** Tools are named `<server>__<tool>`. Server names can't contain underscores, so the separator is unambiguous. An agent version lists tools by name, or lists `mcp:<server>` for all of a server's tools.
- **Availability.** A server that fails to connect, or references an unset variable, is skipped with a warning. Every minute, connected servers are pinged. A server is reconnected after three failed pings in a row; a server that doesn't implement ping counts as alive. Servers that never connected are retried. A replacement connection is made before the old one is dropped. Versions can name a configured server's tools while it is down.
- **Stdio servers** get only `PATH`, `HOME`, `USER`, `LANG`, `TMPDIR` and `TERM` from agentd's environment, plus their configured `env`. Secrets such as the OpenRouter key and the database URL are never passed to them.
- **Config only.** Servers come only from the config file; no tool can add one.

## Conversations

Eino's ADK runner is stateless: each turn takes the history. `internal/chat` stores it in `conversations`/`messages`, one Eino `schema.Message` (as JSON) per row so tool calls round-trip exactly. That ties stored history to Eino's message format, so check old rows still decode when upgrading Eino.

- **Turns are saved whole.** The user's message and the agent's messages are saved together at the end of a turn, so the history never holds a tool call without its result.
  - A turn that fails before any tool ran leaves nothing behind.
  - A turn that fails after tools ran keeps the part that completed, so the agent can see what was already done.
  - A turn whose caller goes away is still saved, because it has been paid for and its tools have had their effects.
- **One turn at a time.** A second message to a conversation that is mid-turn gets `ErrBusy`. Across processes, the save locks the conversation and returns `ErrConflict` if someone else added messages meanwhile.
- **History is windowed.** Each turn sees the most recent ~200,000 characters of history, starting at a user message so a tool result is never sent without its call. Older messages stay stored but aren't sent.

## Runs

`run_workflow` (admin tool) or `runs.Service.Start` writes a `runs` row and a River `run_workflow` job in one transaction. The worker (`internal/runs`) executes nodes one at a time in dependency order. It records each node as a `run_steps` row and stops at the first failed node. Runs are not retried automatically, because nodes may have side effects.

Every run ends `succeeded` or `failed`:

- A panic in a node fails the run.
- Recording the outcome is retried briefly.
- A periodic reaper fails runs that can no longer finish: those whose job is gone without an outcome (a crash, or a failed write), and those running well past the 30-minute limit.

## Embeddings

Embeddings are filled in by River jobs on the `embeddings` queue (`internal/embed`):

- **`embed`** computes one row's vector. It writes only if the row's text still matches what was embedded. If the text changed mid-job, the job snoozes and embeds the new text.
- **Duplicates.** A new `embed` job is a duplicate only of one that is queued or running, never of one that completed, so every edit is embedded.
- **Limits.** Text longer than about 8k tokens is embedded from its start. At startup, agentd checks the embedding model returns 1536 dimensions and refuses to start otherwise.
- **`embed_sweep`** runs on start and every 5 minutes. It queues `embed` jobs for rows with no embedding, or one from a different model.
- **Search** only compares vectors made by the current model. When embedding the query fails, search falls back to keywords.

A process works the `embeddings` queue only if it has an embedder, and the `runs` queue only if it has models. Jobs for a queue nobody is working wait until a process that can work it starts.

## Versions are immutable

A trigger rejects any update to `agent_versions` and `workflow_versions`. Deleting a whole agent or workflow would still cascade to its versions, but nothing does that today. Runs, steps and messages point at exact versions, and this keeps those pointers meaningful.

## Startup and shutdown

`agentd` applies pending migrations on start (embedded in the binary, guarded by an advisory lock). It then opens the connection pool, applies River's own migrations, seeds the admin agent if it is missing, and starts the job workers. It also builds the admin agent once to log its tools; a failure is logged but isn't fatal. On SIGINT or SIGTERM, running jobs get 30 seconds to finish before they are cancelled; a cancelled run is recorded as failed.

## Development

Requires Go, Docker and [Task](https://taskfile.dev). See [AGENTS.md](../AGENTS.md) for the full command list and rules, and [docs/conventions](conventions/) for code and test conventions.

```
cp .env.example .env    # add OPENROUTER_API_KEY
task tools              # pinned golangci-lint, sqlc, goose
task up                 # Postgres
task run                # migrates, then runs agentd
task check              # generated code, docs, lint and tests (Docker for testcontainers)
```
