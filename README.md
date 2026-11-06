# personal-agent

My personal AI agent: one assistant I hand everything to, at work and at home.

You talk to one **admin agent**. It does the work itself, or it builds the specialists and workflows it needs to do it: stored agents with their own prompts, models and tools, and multi-step workflows that run in the background. Over time the system grows into whatever my life needs it to be, without me writing code for each new job.

## What it can do today

- **Talk and act.** The admin agent has:
  - web search and fetch;
  - file read, write, edit, glob and grep;
  - a shell;
  - long-term memory;
  - any MCP server I configure.
- **Build agents.** It creates and versions agents, choosing each one's prompt, any model on [OpenRouter](https://openrouter.ai), tools and structured output schema. Every change is a new immutable version, so anything can be rolled back.
- **Build workflows.** Agents compose into DAG workflows that pass structured JSON between nodes. Workflows run as durable background jobs, and every step is recorded.
- **Delegate.** It hands self-contained tasks to subagents, either stored agents or ad-hoc workers on any model, to keep its own context focused.
- **Remember.** It saves facts and preferences, searchable by meaning and keywords.

## Where it's going

- **A terminal UI** to talk to it.
- **Connections to my accounts:** email, calendar and the rest, through MCP.
- **Triggers:** schedules and an inbox, so it acts without being asked.
- **Confirmation before risky actions.**
- **Cost tracking.**
- **A sandboxed workspace.**
- **A purpose-built memory design,** which is in progress.

## Running it

Requires Go, Docker and [Task](https://taskfile.dev).

```sh
cp .env.example .env   # add OPENROUTER_API_KEY
task tools             # pinned dev tools
task up                # Postgres with pgvector
task run               # migrates the database, then runs agentd
```

`shell` and `write` can do anything the process can, so run agentd in a container if you want it isolated. MCP servers are configured in `mcp.yaml`; see `mcp.example.yaml`.

## Built with

Go, [Eino](https://github.com/cloudwego/eino) for agents, OpenRouter for every model, embedding and search call, Postgres with pgvector, [River](https://riverqueue.com) for jobs, sqlc and goose.

## Docs

- [docs/architecture.md](docs/architecture.md): how the system works, what each package does, and why.
- [AGENTS.md](AGENTS.md): rules and commands for working on the code, for coding agents and people alike.
- [docs/conventions/](docs/conventions/): Go and testing conventions.
- [docs/recipes.md](docs/recipes.md): checklists for common changes.
