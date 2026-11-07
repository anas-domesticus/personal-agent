# Architecture

The product direction is a Postgres-backed knowledge and capability service that complements a personal agent. The current process is an MCP orchestration server: Postgres holds agent and workflow definitions and durable execution state, the process serves MCP and works background jobs, and there is no built-in conversational agent, user interface or Ask a Question operation. The former memory feature is removed.

## Product boundary

External consumer agents own human-facing LLM interactions. This service owns durable knowledge, deterministic question and evidence tooling, versioned workflow instruction sets and background capabilities suitable for cloud deployment. Question tooling returns structured nodes and evidence rather than conversational answers. Workflows are reusable instruction sets, not queries run against the knowledge base. External consumer agents may author, test and iterate workflows using immutable versions; this boundary does not propose or implement changes to workflows or their execution engine.

The current implementation stores and semantically searches structured knowledge nodes and runs explicitly requested DAG workflows. Deterministic question and evidence tooling, including Ask a Question, is future direction. In the target boundary, the existing internal agent loop is retained for enrichment jobs and scheduled tasks only; schedules themselves are future direction and no scheduling functionality exists. Cloud deployment and authentication also do not exist. These directions do not change the current behaviour documented below.

## Current implementation

All statements below describe current code. The graph database schema and `internal/graph` Go module are wired into `internal/app`, which registers versioned objective, workflow, claim, source and agent types and runs the graph-enrichment worker. MCP exposes node upsert and semantic search; no relationship kinds are registered by the base schema.

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
                             │                  (current_version, revision on graph_nodes)
graph_edge_types ──< graph_edge_type_endpoints
                  └──< graph_edges (typed, unversioned relationships; revision)
```

- **Versioning.** A change to an agent or workflow is a new version row. `current_version` on the parent points at the one in use. A trigger rejects updates to agent, workflow and graph-node version tables. Runs and messages point at exact agent versions.
- **Workflow graphs** are JSONB: `{"nodes": [{"id", "agent", "instruction"?}], "edges": [{"from", "to"}]}`. Nodes refer to agents by name and run the agent's current version when the node starts. `internal/workflow` rejects cycles, duplicate or reserved ids and dangling edges; the catalog checks every agent exists.
- **Structured data between nodes.** A node's input is `{"input": <workflow input>, "<upstream id>": <its output>, ...}`. Its output matches the agent version's `output_schema`, or is `{"text": "..."}` when there is none. The workflow output contains each sink node's output keyed by id. A node gets one correction attempt for invalid output.
- **Deletion.** A workflow without runs can be deleted, which cascades to its versions. Postgres rejects deletion when runs reference it, and the catalog returns `ErrConflict` so execution history remains intact.
- **Search.** Workflows (name and description) have an embedding and generated full-text column. Search combines both rankings with reciprocal rank fusion. Graph nodes have separate title and description embeddings; semantic search uses the closest field. Rows are saved first and embedded afterwards by River jobs. `embedding_model` identifies which model made each vector.

## Knowledge graph

`internal/graph` is a reusable typed graph module wired into `internal/app`. The application registers five versioned knowledge types from `internal/knowledge`: objectives describe desired outcomes, workflows hold executable DAGs, claims hold statements of knowledge, sources hold provenance, and agents hold prompt/model/tool definitions. Every node data type embeds common details or otherwise implements `graph.TextData`, exposing matching `title` and `description` JSON fields. The graph-enrichment worker runs with the application. MCP exposes generic upsert and semantic search across these predefined kinds. There are no base relationship kinds and Ask a Question is not implemented.

`graph.New` requires a store and a non-nil `JobInserter` for writes: a changed write or rollback fails rather than committing without its enrichment job. Generic registration functions return node and edge repositories for application-defined Go types. Registration persists a JSON Schema in `graph_node_types` or `graph_edge_types`, verifies that a later process registering the same kind matches the stored definition, and rejects a repeated kind in the same registry even when it matches. Node versioned-ness is fixed at registration and persisted with the type; a later registration cannot change it. Edge registration also verifies the complete endpoint set. `RegisterRiver` freezes registration and installs the shared worker and its `graph_enrichment` queue.

Changing a registered Go type does not migrate its persisted definition or data. A schema change requires an explicit migration that updates the affected `data_schema` and transforms both `graph_nodes.data` and `graph_node_versions.data`, including historical snapshots. Never edit a committed migration. Registrations have no type-level version number and no automatic migration mechanism, so breaking changes should use a new kind unless a deliberate migration covers every snapshot.

Ordinary nodes store current JSON directly on `graph_nodes`; they are not versioned. Versioned nodes keep JSON in immutable `graph_node_versions` rows and point `current_version` at the current snapshot. Their composed repositories add current-version reads, individual historical reads, newest-first pagination and rollback. Rollback to a different version moves the pointer without creating a version and increments the separate optimistic-concurrency revision; rolling back to the current version is unchanged.

History pages default to 50 versions and are capped at 100. `Before` is an exclusive version cursor; pass the returned `NextBefore` to fetch the next older page. A zero `NextBefore` means no older versions remain.

Node upserts, edge upserts and rollbacks accept optional `ExpectedRevision` optimistic-concurrency checks. A nil pointer skips the check and is required when creating an entity. A mismatch returns `domain.ErrConflict`.

Repository lookups map missing rows to `domain.ErrNotFound`. Persistence helpers map Postgres unique violations (`23505`) to `domain.ErrExists`, and foreign-key or check violations (`23503`, `23514`) to `domain.ErrConflict`. Revision mismatches and disallowed edge endpoint combinations also return `ErrConflict`.

Edges carry source and target kinds. Composite foreign keys check those kinds against the actual nodes and against `graph_edge_type_endpoints`, so Postgres enforces both endpoint existence and permitted relationship types. One edge of a kind may connect a pair of nodes; different kinds may connect the same pair. Self-loops and deletion of connected nodes are rejected.

Every changed node or edge upsert and every rollback to a different version queues a `graph_enrich` River job on the `graph_enrichment` queue in the same transaction, including types with no registered handlers. A changed node also queues an `embed` job on the `embeddings` queue; this is implicit and independent of the per-type handlers. Unchanged upserts and no-op rollbacks do not create versions, increment revisions or queue jobs. The enrichment worker dispatches to handlers supplied during registration and skips stale revisions. Handlers are external functions and must be idempotent because River jobs run at least once.

`graph_nodes` stores separate title and description vectors for the current revision, never for historical snapshots. Content changes clear the previous vectors. The embedding worker reads current ordinary or versioned JSON, embeds both fields in one provider batch, and conditionally writes only if the revision is still current. `embedding_revision` records that compare-and-set token. A periodic sweep requeues missing vectors, revision mismatches and model changes. Semantic search only considers fresh vectors from the configured model and ranks each node by its closer title or description cosine distance.## Process and package map

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
| `internal/graph` | Typed graph registry, node and edge persistence, immutable history, rollback, enrichment and semantic search |
| `internal/knowledge` | First-class objective, workflow, claim, source and agent domain types and validation |
| `internal/agent` | Builds runnable Eino agents from versions and provides subagents |
| `internal/chat` | Conversation and message storage used by subagents |
| `internal/runs` | Workflow runs: service, River worker and reaper |
| `internal/workflow` | Graph validation, topological order and output schemas |
| `internal/llm` | OpenRouter models, embeddings and model catalogue |
| `internal/embed` | River jobs that maintain workflow and graph-node embedding columns |
| `internal/search` | Web-search interface and OpenRouter implementation |
| `internal/tools` | Registry of tools workflow agents can use |
| `internal/tools/kit` | Tool construction, result limits and configuration helpers |
| `internal/tools/clock` | Current-time tool |
| `internal/tools/files` | File and shell tools through `workspace.Workspace` |
| `internal/tools/knowledge` | Native upsert and semantic-search tools for predefined knowledge nodes |
| `internal/tools/web` | Web search and fetch |
| `internal/tools/mcp` | Outbound MCP clients and tool discovery |
| `internal/workspace` | Local or remote-sandbox workspace boundary |
| `internal/testdb` | Disposable Postgres containers for database tests |
| `internal/testenv` | Whole-stack integration environment with scripted models |
| `internal/mocks` | Generated mocks of third-party interfaces |

## MCP API

`internal/mcpserver` registers 21 tools with typed input and output schemas. The SDK validates arguments, returns `structuredContent` plus JSON text compatibility content, and turns handler errors into tool execution errors. Read-only, additive and idempotent operations carry annotations; every tool is marked closed-world. MCP workflow-search, knowledge-search and run-list limits default to 10 and are capped at 100; these are separate from the graph history defaults of 50 and 100 described above.

- **Agents:** `list_agents`, `get_agent`, `list_agent_versions` (immutable versions, newest first), `create_agent`, `add_agent_version`, `set_agent_version`, and `list_tools` (tools and settings available to workflow-agent definitions).
- **Workflows:** `list_workflows`, `search_workflows`, `get_workflow`, `list_workflow_versions`, `create_workflow`, `add_workflow_version`, `set_workflow_version`, `update_workflow_description`, `delete_workflow`.
- **Runs:** `run_workflow`, `get_run`, `list_runs`.
- **Knowledge nodes:** `upsert_knowledge_node`, `search_knowledge_nodes`. Upsert accepts a predefined kind and raw data object, with an optional existing id and expected revision. Semantic search optionally restricts one kind and ranks current nodes by their closer title or description vector.

`get_workflow` optionally selects a version instead of the current one. `run_workflow` starts the current version asynchronously and returns a run id; `wait_seconds` optionally polls for up to five minutes. `get_run` omits transcripts by default and can include them for debugging. Workflow deletion never removes recorded runs.

The transport is stateless and returns JSON responses. Durable state lives in Postgres, not MCP sessions. The official SDK supplies localhost DNS-rebinding protection and request-size limits; Go's cross-origin protection wraps the handler. There is deliberately no authentication. `MCP_LISTEN` defaults to `127.0.0.1:7433` and configuration rejects non-loopback addresses. The endpoint is `/mcp`. Do not expose this server to an untrusted network.

## Composition and ownership

- **Interfaces live with their consumer.** The MCP server depends on small service interfaces. The agent builder depends on `Source` and `Toolset`; the run worker depends on `Recorder` and `Agents`. `internal/app` wires concrete implementations.
- **No late binding.** Construction proceeds from store and clients through catalogues and the agent builder to workers.
- **Each aggregate has one writer:** the catalog owns agents and workflows; runs owns runs and steps; chat owns conversations and messages; embed owns embedding columns; graph owns the graph meta-schema, nodes, versions and edges.
- **Database writes are transactional where needed.** Version numbering locks the parent. Run creation and its River job are committed together, as are content changes and embedding or graph-enrichment jobs.

## Workflow agents and tools

An agent version holds its system prompt, OpenRouter model, settings, tools and optional output JSON Schema. Models, embeddings and search all use `OPENROUTER_API_KEY`; without it the MCP management API still works, but model runs and embeddings wait unavailable and semantic knowledge-node search returns `ErrUnavailable`.

An agent version's `config.tools` enables native tools, settings objects and tools from configured outbound MCP servers. `list_tools` describes the available tools and their settings. Native `upsert_knowledge_node` and `search_knowledge_nodes` tools let enabled agents write and search the predefined knowledge schema through `app.Knowledge`, using the same UUID parsing, new-node expected-revision validation and semantic-search behaviour as the MCP operations. Native search limits default to 10 and are capped at 100. Tool failures are returned to the model as `kit.Result` values. Tools that disappear or are temporarily unavailable are skipped when an agent builds rather than making its stored version unreadable.

The workspace controls all file and command operations. Local commands receive a small environment rather than server secrets, run in their own process group and are killed on timeout. They still have the server's operating-system permissions, so use a container for isolation. `WORKSPACE=sandbox` selects the remote-sandbox boundary, whose operations currently return unimplemented.

Subagents have their own linked conversations, never receive administrative capabilities, and respect the configured nesting limit. The application does not seed or run an administrative agent.

## Runs

`run_workflow` or `runs.Service.Start` writes a `runs` row and River `run_workflow` job in one transaction. The worker executes nodes one at a time in dependency order, records each as a `run_steps` row and stops at the first failure. Runs are not automatically retried because nodes can have side effects.

Every run ends `succeeded` or `failed`. Panics fail the run. Recording its outcome is retried briefly. A periodic reaper fails runs whose job vanished without an outcome and runs beyond the 30-minute limit plus a grace period.

A process always works the graph-enrichment queue. It works the embeddings queue only with an embedder and the runs queue only with model access. Jobs for an unworked queue remain in Postgres until a capable process starts.

## Configuration and lifecycle

`internal/config` loads environment settings (Task loads `.env`; an installed binary reads its process environment):

- `DATABASE_URL` is required.
- `OPENROUTER_API_KEY`, `EMBEDDING_MODEL` and `SEARCH_MODEL` control model access.
- `WORKSPACE` and `WORKSPACE_DIR` select the execution workspace.
- `MCP_CONFIG` loads outbound MCP servers, defaulting to `mcp.yaml`; see `mcp.example.yaml`.
- `MCP_LISTEN` sets the unauthenticated MCP listener, defaulting to `127.0.0.1:7433`.

At startup the server applies app and River migrations under their respective protections, opens Postgres, connects outbound MCP clients and starts available workers. It does not seed or run an administrative agent. On SIGINT or SIGTERM it stops accepting MCP requests and gives running jobs 30 seconds to finish before cancelling them.
