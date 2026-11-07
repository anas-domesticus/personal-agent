# personal-agent

A Postgres-backed orchestration server for agentic work. It is an MCP server, not a conversational assistant: connect any MCP-capable client to manage agent definitions and DAG workflows, start durable background runs and inspect results.

## What it does

- Exposes agent, workflow and run management through typed MCP tools.
- Runs agentic DAG workflows in the background with River and records every step in Postgres.
- Stores immutable agent and workflow versions, including promotion and rollback.
- Provides a generic Postgres graph module with typed nodes, constrained edges, immutable history, rollback and externally defined enrichment handlers.
- Searches workflows by meaning and keywords with pgvector.
- Gives workflow agents configurable file, shell, web, clock and external MCP tools.

The server has no authentication and only accepts loopback listen addresses. Do not expose it to an untrusted network. Its file and shell tools inherit the server's operating-system permissions, so use a container when stronger isolation is needed.

## Running it

Requires Go, Docker and [Task](https://taskfile.dev).

```sh
cp .env.example .env   # add OPENROUTER_API_KEY
task tools             # pinned development tools
task up                # Postgres with pgvector
task run               # migrate, start workers and serve MCP
```

The endpoint is `http://127.0.0.1:7433/mcp`, using stateless Streamable HTTP. Configure an MCP client with that URL; no token or other credentials are needed. `MCP_LISTEN` changes the address.

`task install` puts the `orchestrator` binary on your PATH (in `GOBIN`, or `GOPATH/bin`). An installed binary reads settings from its environment, not `.env`. Set `MCP_CONFIG` and `WORKSPACE_DIR` to absolute paths because their defaults are relative to the working directory. `task uninstall` removes it.

## MCP tools

- **Agents:** list, search configuration tools, get, create, append immutable versions and promote versions.
- **Workflows:** list, search, get, list versions, create, append versions, promote versions, update descriptions and delete.
- **Runs:** start a workflow, optionally wait briefly, list recent runs and inspect durable node results.

Deleting a workflow with recorded runs returns a conflict rather than destroying execution history.

## Built with

Go, the official [Go MCP SDK](https://github.com/modelcontextprotocol/go-sdk), [Eino](https://github.com/cloudwego/eino) for workflow agents, OpenRouter, Postgres with pgvector, [River](https://riverqueue.com), sqlc and goose.

## Docs

- [docs/architecture.md](docs/architecture.md): system design and package map.
- [AGENTS.md](AGENTS.md): contributor rules and commands.
- [docs/conventions/](docs/conventions/): Go and testing conventions.
- [docs/recipes.md](docs/recipes.md): checklists for common changes.
