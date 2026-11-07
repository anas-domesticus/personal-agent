# personal-agent

A Postgres-backed knowledge and capability service that complements a personal agent.

**Today, the implemented product is an orchestration and knowledge-graph MCP server, not a conversational assistant or question-answering interface.** Its 21 MCP tools manage agent definitions, DAG workflows, durable background runs and predefined knowledge nodes. The graph stores versioned objectives, workflows, claims, sources and agents, embeds every node's title and description, and supports semantic node search.

## Product boundary and direction

This service complements a personal agent rather than replacing it. External consumer agents own human-facing LLM interactions; this service owns durable knowledge, deterministic question and evidence tooling, versioned workflow instruction sets and background capabilities suitable for cloud deployment. Question tooling returns structured nodes and evidence, not conversational answers. Workflows are reusable instruction sets, not queries to run against the knowledge base; external consumer agents can author, test and iterate them through immutable versions.

That is the target boundary, not a claim that every capability exists today. The current implementation stores and searches knowledge nodes and runs explicitly requested DAG workflows. Deterministic question and evidence tooling, including **Ask a Question**, is future direction. The existing internal agent loop is retained for enrichment jobs and scheduled tasks only in that target boundary; scheduling itself is future direction and is not implemented here. Cloud deployment, scheduling and authentication do not currently exist. The former memory feature has been removed.

## What it does now

- Exposes 21 agent, workflow, run and knowledge-node operations through typed MCP tools.
- Runs agentic DAG workflows in the background with River and records every step in Postgres.
- Stores immutable agent and workflow versions, including promotion and rollback.
- Registers versioned objective, workflow, claim, source and agent types in the Postgres graph. The graph module also supports constrained edges, rollback and externally defined enrichment handlers; the base schema registers no relationships or handlers.
- Queues separate title and description embeddings whenever a knowledge node changes and searches current nodes by semantic similarity.
- Searches workflows by meaning and keywords with pgvector.
- Gives workflow agents configurable file, shell, web, clock and external MCP tools.

The server has no authentication and only accepts loopback listen addresses. Do not expose it to an untrusted network. Its file and shell tools inherit the server's operating-system permissions, so use a container when stronger isolation is needed.

## Running it

Requires Go, Docker with Docker Compose, and [Task](https://taskfile.dev).

```sh
cp .env.example .env   # add OPENROUTER_API_KEY
task tools             # pinned development tools
task up                # Postgres with pgvector
task run               # migrate, start workers and serve MCP
```

The endpoint is `http://127.0.0.1:7433/mcp`, using stateless Streamable HTTP. Configure an MCP client with that URL; no token or other credentials are needed. `MCP_LISTEN` changes the address.

`task install` puts the `orchestrator` binary on your PATH (in `GOBIN`, or `GOPATH/bin`). An installed binary reads settings from its environment, not `.env`. Set `MCP_CONFIG` and `WORKSPACE_DIR` to absolute paths because their defaults are relative to the working directory. `task uninstall` removes it.

## MCP tools

- **Agents:** list and inspect agents, list their immutable versions newest first, create agents, append versions and promote or roll back versions. `list_tools` lists tools and configuration available to workflow-agent definitions.
- **Workflows:** list, search, get, list versions, create, append versions, promote or roll back versions, update descriptions and delete.
- **Runs:** start a workflow, optionally wait briefly, list recent runs and inspect durable node results.

- **Knowledge nodes:** create or update any predefined objective, workflow, claim, source or agent with `upsert_knowledge_node`; search current nodes by title or description meaning with `search_knowledge_nodes`.

Ask a Question is not implemented.

Deleting a workflow with recorded runs returns a conflict rather than destroying execution history.

## Built with

Go, the official [Go MCP SDK](https://github.com/modelcontextprotocol/go-sdk), [Eino](https://github.com/cloudwego/eino) for workflow agents, OpenRouter, Postgres with pgvector, [River](https://riverqueue.com), sqlc and goose.

## Docs

- [docs/architecture.md](docs/architecture.md): system design and package map.
- [AGENTS.md](AGENTS.md): contributor rules and commands.
- [docs/conventions/](docs/conventions/): Go and testing conventions.
- [docs/recipes.md](docs/recipes.md): checklists for common changes.
