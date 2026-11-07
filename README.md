# personal-agent

A Postgres-backed knowledge base in the making, with agents supporting the work of collecting, organising and using knowledge.

**Today, the implemented product is an orchestration MCP server, not a conversational assistant or question-answering interface.** Its 19 MCP tools manage agent definitions, DAG workflows and durable background runs. The application also registers an internal knowledge graph with versioned objectives, workflows, claims, sources and agents, but does not expose graph operations through MCP.

## Direction

The long-term centre of the product is a durable, queryable knowledge base in Postgres. Agents will help curate and use that knowledge, including an **Ask a Question** experience grounded in what is stored. Ask a Question and public knowledge-base APIs are future direction, not current behaviour. The former memory feature has been removed.

## What it does now

- Exposes 19 agent, workflow and run management operations through typed MCP tools.
- Runs agentic DAG workflows in the background with River and records every step in Postgres.
- Stores immutable agent and workflow versions, including promotion and rollback.
- Registers versioned objective, workflow, claim, source and agent types in the internal Postgres graph. The graph module also supports constrained edges, rollback and externally defined enrichment handlers; the base schema registers no relationships or handlers.
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

These are management tools: there is currently no MCP tool for storing or querying knowledge-graph entities and no Ask a Question operation.

Deleting a workflow with recorded runs returns a conflict rather than destroying execution history.

## Built with

Go, the official [Go MCP SDK](https://github.com/modelcontextprotocol/go-sdk), [Eino](https://github.com/cloudwego/eino) for workflow agents, OpenRouter, Postgres with pgvector, [River](https://riverqueue.com), sqlc and goose.

## Docs

- [docs/architecture.md](docs/architecture.md): system design and package map.
- [AGENTS.md](AGENTS.md): contributor rules and commands.
- [docs/conventions/](docs/conventions/): Go and testing conventions.
- [docs/recipes.md](docs/recipes.md): checklists for common changes.
