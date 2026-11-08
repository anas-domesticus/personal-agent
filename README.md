# personal-agent

A Postgres-backed knowledge and capability service that complements a personal agent.

**Today, the implemented product is an orchestration and knowledge-graph MCP server, not a conversational assistant or question-answering interface.** `/mcp` exposes 15 generic graph tools, while `/mcp/admin` provides 19 agent, workflow, run and tool-listing management operations plus graph schema discovery and dynamic type creation. The graph stores versioned objectives, workflows, claims, sources and agents, supports database-defined types and versioned edges, embeds every node's title and description, and supports deterministic graph reads and semantic node search.

## Product boundary and direction

This service complements a personal agent rather than replacing it. External consumer agents own human-facing LLM interactions; this service owns durable knowledge, deterministic question and evidence tooling, versioned workflow instruction sets and background capabilities suitable for cloud deployment. Question tooling returns structured nodes and evidence, not conversational answers. Workflows are reusable instruction sets, not queries to run against the knowledge base; external consumer agents can author, test and iterate them through immutable versions.

That is the target boundary, not a claim that every capability exists today. The current implementation stores and searches knowledge nodes and runs explicitly requested DAG workflows. Deterministic question and evidence tooling, including **Ask a Question**, is future direction. The existing internal agent loop is retained for enrichment jobs and scheduled tasks only in that target boundary; scheduling itself is future direction and is not implemented here. Cloud deployment, scheduling and authentication do not currently exist. The former memory feature has been removed.

## What it does now

- Exposes a 15-tool consumer graph API at `/mcp` and management, schema-discovery and schema-creation operations at `/mcp/admin`.
- Runs agentic DAG workflows in the background with River and records every step in Postgres.
- Stores immutable agent and workflow versions, including promotion and rollback.
- Registers versioned objective, workflow, claim, source and agent types plus the `supported_by` claim-to-source relationship in the Postgres graph. The graph module also supports constrained versioned edges, rollback and externally defined enrichment handlers; the base schema registers no handlers.
- Reads current nodes and edges, lists them with optional type or endpoint filters, and returns their immutable histories. Searches nodes by semantic similarity; changed nodes queue separate title and description embeddings.
- Lets administrators create dynamic versioned node types and edge types with JSON Schemas and permitted endpoints, then use the same generic data tools for base and dynamic types.
- Searches workflows by meaning and keywords with pgvector.
- Gives workflow agents configurable file, shell, web, clock, generic graph and external MCP tools.

Both servers have no authentication and only accept loopback listen addresses. `/mcp` and `/mcp/admin` separate capability exposure, not authentication or authorisation. Do not expose either endpoint to an untrusted network. File and shell tools inherit the server's operating-system permissions, so use a container when stronger isolation is needed.

## Running it

Requires Go, Docker with Docker Compose, and [Task](https://taskfile.dev).

```sh
cp .env.example .env   # add OPENROUTER_API_KEY
task tools             # pinned development tools
task up                # Postgres with pgvector
task run               # migrate, start workers and serve MCP
```

The consumer endpoint is `http://127.0.0.1:7433/mcp`; administration is at `http://127.0.0.1:7433/mcp/admin`. Both use unauthenticated, stateless Streamable HTTP over loopback. Configure MCP clients with the URL matching the capabilities they need; no token or other credentials are needed. `MCP_LISTEN` changes the address.

`task install` puts the `orchestrator` binary on your PATH (in `GOBIN`, or `GOPATH/bin`). An installed binary reads settings from its environment, not `.env`. Set `MCP_CONFIG` and `WORKSPACE_DIR` to absolute paths because their defaults are relative to the working directory. `task uninstall` removes it.

## MCP tools

- **Agents (`/mcp/admin`):** list and inspect agents, list their immutable versions newest first, create agents, append versions and promote or roll back versions. `list_tools` lists tools and configuration available to workflow-agent definitions.
- **Workflows (`/mcp/admin`):** list, search, get, list versions, create, append versions, promote or roll back versions, update descriptions and delete.
- **Runs (`/mcp/admin`):** start a workflow, optionally wait briefly, list recent runs and inspect durable node results.

- **Consumer graph (`/mcp`):** use `get_node`, `list_nodes`, `upsert_node`, `delete_node`, `search_nodes` and `get_node_history` for nodes, and `get_edge`, `list_edges`, `upsert_edge`, `delete_edge` and `get_edge_history` for edges. `get_node_type`, `list_node_types`, `get_edge_type` and `list_edge_types` provide read-only schema discovery. All operations work across base and dynamic database-defined types. Nodes and edges have immutable histories, current versions, revisions and optional version metadata. Node and edge lists default to 10 and are capped at 100; histories default to 50 and are capped at 100. Deleting a connected node returns a conflict. There is no edge semantic search.
- **Schema administration (`/mcp/admin`):** `create_node_type` and `create_edge_type` add dynamic types with JSON Schemas and permitted edge endpoints. The four schema-discovery tools are also available here. Schema definitions have no history or mutation API, and incompatible schema evolution is unsupported.
- **Native tools:** every consumer graph tool has a same-named native equivalent for workflow agents. Native schema discovery is read-only; native schema mutation does not exist. Knowledge agent and workflow records remain descriptive graph data, separate from the runnable catalogues.

Ask a Question is not implemented.

Deleting a workflow with recorded runs returns a conflict rather than destroying execution history.

## Built with

Go, the official [Go MCP SDK](https://github.com/modelcontextprotocol/go-sdk), [Eino](https://github.com/cloudwego/eino) for workflow agents, OpenRouter, Postgres with pgvector, [River](https://riverqueue.com), sqlc and goose.

## Docs

- [docs/architecture.md](docs/architecture.md): system design and package map.
- [AGENTS.md](AGENTS.md): contributor rules and commands.
- [docs/conventions/](docs/conventions/): Go and testing conventions.
- [docs/recipes.md](docs/recipes.md): checklists for common changes.
