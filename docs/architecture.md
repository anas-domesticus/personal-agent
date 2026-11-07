# Architecture

An MCP orchestration server for agentic work, written in Go. Postgres holds definitions and durable execution state. The process serves MCP and works background jobs; there is no built-in conversational agent or user interface.

## Technology

| Concern | Choice |
|---|---|
| MCP | Official [Go MCP SDK](https://github.com/modelcontextprotocol/go-sdk), stateless Streamable HTTP |
| Workflow agents | [Eino](https://github.com/cloudwego/eino), models through OpenRouter |
| Storage | Postgres, pgvector, sqlc and goose |
| Jobs | [River](https://riverqueue.com), using the same Postgres database |
| Outbound MCP tools | [mcp-go](https://github.com/mark3labs/mcp-go) clients |

## Data model

```text
agents ──< agent_versions          (prompt, model, config incl. tools, output_schema; immutable)
     └──< conversations ──< messages (subagent history)
workflows ──< workflow_versions    (DAG as JSONB; immutable)
        └──< runs ──< run_steps   (agent + version used, input, output, transcript, tokens)
graph_node_types ──< graph_nodes ──< graph_node_versions (immutable snapshots)
graph_edge_types ──< graph_edge_type_endpoints
                  └──< graph_edges (typed, unversioned relationships)
```

- **Versioning.** A change to an agent or workflow is a new version row. `current_version` on the parent points at the one in use. A trigger rejects updates to either version table. Runs and messages point at exact versions.
- **Workflow graphs** are JSONB: `{"nodes": [{"id", "agent", "instruction"?}], "edges": [{"from", "to"}]}`. Nodes refer to agents by name and run the agent's current version when the node starts. `internal/workflow` rejects cycles, duplicate or reserved ids and dangling edges; the catalog checks every agent exists.
- **Structured data between nodes.** A node's input is `{"input": <workflow input>, "<upstream id>": <its output>, ...}`. Its output matches the agent version's `output_schema`, or is `{"text": "..."}` when there is none. The workflow output contains each sink node's output keyed by id. A node gets one correction attempt for invalid output.
- **Deletion.** A workflow without runs can be deleted, which cascades to its versions. Postgres rejects deletion when runs reference it, and the catalog returns `ErrConflict` so execution history remains intact.
- **Search.** Workflows (name and description) have an embedding and generated full-text column. Search combines both rankings with reciprocal rank fusion. Rows are saved first and embedded afterwards by River jobs. `embedding_model` identifies which model made each vector.

## Knowledge graph

`internal/graph` is a reusable typed graph module; it is not yet exposed through MCP or wired into the application. Generic registration functions return node and edge repositories for application-defined Go types. Registration persists a JSON Schema in `graph_node_types` or `graph_edge_types`, verifies that a repeated registration matches the stored definition and freezes when `RegisterRiver` installs the enrichment worker.

Ordinary nodes store current JSON directly on `graph_nodes`. Versioned nodes keep JSON in immutable `graph_node_versions` rows and point `current_version` at the current snapshot. Their composed repositories add current-version reads, individual historical reads, newest-first pagination and rollback. Rollback moves the pointer without creating a version and increments the separate optimistic-concurrency revision.

Edges carry source and target kinds. Composite foreign keys check those kinds against the actual nodes and against `graph_edge_type_endpoints`, so Postgres enforces both endpoint existence and permitted relationship types. One edge of a kind may connect a pair of nodes; different kinds may connect the same pair. Self-loops and deletion of connected nodes are rejected.

Every changed node or edge upsert and every rollback queues a `graph_enrich` River job in the same transaction. Unchanged upserts do not create versions, increment revisions or queue jobs. The worker dispatches to handlers supplied during registration and skips stale revisions. Handlers are external functions and must be idempotent because River jobs run at least once.

## Process and package map

```text
MCP client ══ Streamable HTTP ══> mcpserver ──> catalog, runs (consumer interfaces)
                                        app ──> River workers ──> agent builder
```

| Package | Responsibility |
|---|---|
| `cmd/orchestrator` | Entrypoint, signals, HTTP listener and graceful shutdown |
| `internal/mcpserver` | Typed MCP tools, schemas, annotations and the Streamable HTTP handler |
| `internal/app` | Composition root; constructs services, model clients and River workers |
| `internal/domain` | Shared agent, workflow, run and subagent types; sentinel errors |
| `internal/config` | Environment and outbound MCP configuration |
| `internal/store` | Postgres pool, migrations, transaction helper and River migrations |
| `internal/store/db` | sqlc-generated queries, never hand-edited |
| `internal/catalog` | Agents and workflows: validation, versioning, deletion and search |
| `internal/graph` | Typed graph registry, node and edge persistence, immutable history, rollback and enrichment |
| `internal/agent` | Builds runnable Eino agents from versions and provides subagents |
| `internal/chat` | Conversation and message storage used by subagents |
| `internal/runs` | Workflow runs: service, River worker and reaper |
| `internal/workflow` | Graph validation, topological order and output schemas |
| `internal/llm` | OpenRouter models, embeddings and model catalogue |
| `internal/embed` | River jobs that maintain embedding columns |
| `internal/search` | Web-search interface and OpenRouter implementation |
| `internal/tools` | Registry of tools workflow agents can use |
| `internal/tools/kit` | Tool construction, result limits and configuration helpers |
| `internal/tools/clock` | Current-time tool |
| `internal/tools/files` | File and shell tools through `workspace.Workspace` |
| `internal/tools/web` | Web search and fetch |
| `internal/tools/mcp` | Outbound MCP clients and tool discovery |
| `internal/workspace` | Local or remote-sandbox workspace boundary |
| `internal/testdb` | Disposable Postgres containers for database tests |
| `internal/testenv` | Whole-stack integration environment with scripted models |
| `internal/mocks` | Generated mocks of third-party interfaces |

## MCP API

`internal/mcpserver` registers tools with typed input and output schemas. The SDK validates arguments, returns `structuredContent` plus JSON text compatibility content, and turns handler errors into tool execution errors. Read-only, additive and idempotent operations carry annotations; every tool is marked closed-world. Lists accept bounded limits, defaulting to 10 and capped at 100.

- **Agents:** `list_agents`, `get_agent`, `list_agent_versions`, `create_agent`, `add_agent_version`, `set_agent_version`, `list_tools`.
- **Workflows:** `list_workflows`, `search_workflows`, `get_workflow`, `list_workflow_versions`, `create_workflow`, `add_workflow_version`, `set_workflow_version`, `update_workflow_description`, `delete_workflow`.
- **Runs:** `run_workflow`, `get_run`, `list_runs`.

`get_workflow` optionally selects a version instead of the current one. `run_workflow` starts the current version asynchronously and returns a run id; `wait_seconds` optionally polls for up to five minutes. `get_run` omits transcripts by default and can include them for debugging. Workflow deletion never removes recorded runs.

The transport is stateless and returns JSON responses. Durable state lives in Postgres, not MCP sessions. The official SDK supplies localhost DNS-rebinding protection and request-size limits; Go's cross-origin protection wraps the handler. There is deliberately no authentication. `MCP_LISTEN` defaults to `127.0.0.1:7433` and configuration rejects non-loopback addresses. The endpoint is `/mcp`. Do not expose this server to an untrusted network.

## Composition and ownership

- **Interfaces live with their consumer.** The MCP server depends on small service interfaces. The agent builder depends on `Source` and `Toolset`; the run worker depends on `Recorder` and `Agents`. `internal/app` wires concrete implementations.
- **No late binding.** Construction proceeds from store and clients through catalogues and the agent builder to workers.
- **Each aggregate has one writer:** the catalog owns agents and workflows; runs owns runs and steps; chat owns conversations and messages; embed owns embedding columns; graph owns the graph meta-schema, nodes, versions and edges.
- **Database writes are transactional where needed.** Version numbering locks the parent. Run creation and its River job are committed together, as are content changes and embedding or graph-enrichment jobs.

## Workflow agents and tools

An agent version holds its system prompt, OpenRouter model, settings, tools and optional output JSON Schema. Models, embeddings and search all use `OPENROUTER_API_KEY`; without it the MCP management API still works, but model runs and embeddings wait unavailable.

An agent version's `config.tools` enables native tools, settings objects and tools from configured outbound MCP servers. `list_tools` describes the available tools and their settings. Tools that disappear or are temporarily unavailable are skipped when an agent builds rather than making its stored version unreadable.

The workspace controls all file and command operations. Local commands receive a small environment rather than server secrets, run in their own process group and are killed on timeout. They still have the server's operating-system permissions, so use a container for isolation. `WORKSPACE=sandbox` selects the remote-sandbox boundary, whose operations currently return unimplemented.

Subagents have their own linked conversations, never receive administrative capabilities, and respect the configured nesting limit.

## Runs

`run_workflow` or `runs.Service.Start` writes a `runs` row and River `run_workflow` job in one transaction. The worker executes nodes one at a time in dependency order, records each as a `run_steps` row and stops at the first failure. Runs are not automatically retried because nodes can have side effects.

Every run ends `succeeded` or `failed`. Panics fail the run. Recording its outcome is retried briefly. A periodic reaper fails runs whose job vanished without an outcome and runs beyond the 30-minute limit plus a grace period.

A process works the embeddings queue only with an embedder and the runs queue only with model access. Jobs for an unworked queue remain in Postgres until a capable process starts.

## Configuration and lifecycle

`internal/config` loads environment settings (Task loads `.env`; an installed binary reads its process environment):

- `DATABASE_URL` is required.
- `OPENROUTER_API_KEY`, `EMBEDDING_MODEL` and `SEARCH_MODEL` control model access.
- `WORKSPACE` and `WORKSPACE_DIR` select the execution workspace.
- `MCP_CONFIG` loads outbound MCP servers, defaulting to `mcp.yaml`; see `mcp.example.yaml`.
- `MCP_LISTEN` sets the unauthenticated MCP listener, defaulting to `127.0.0.1:7433`.

At startup the server applies app and River migrations under their respective protections, opens Postgres, connects outbound MCP clients and starts available workers. It no longer seeds or runs an administrative agent. On SIGINT or SIGTERM it stops accepting MCP requests and gives running jobs 30 seconds to finish before cancelling them.
