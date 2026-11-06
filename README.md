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
- **Chat in the terminal.** A TUI streams the agent's replies and tool calls as they happen, keeps every conversation, and can attach to a server running elsewhere. Its navigation follows [Crush](https://github.com/charmbracelet/crush): a command palette (`ctrl+p`), `tab` into the transcript to select, expand and copy items, and a sidebar on wide terminals.

## Where it's going

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
task run               # migrates the database, starts the server and opens the TUI
```

To use it from anywhere, `task install` puts the `agent` binary on your PATH (in `GOBIN`, or `GOPATH/bin`). The installed binary reads its settings from the environment, not from `.env`, so export them in your shell (see `.env.example`). Set `MCP_CONFIG` and `WORKSPACE_DIR` to absolute paths too: they default to `mcp.yaml` and `workspace` in the current directory, so a binary run from elsewhere would load no MCP servers and create a `workspace/` wherever you are. `task uninstall` removes it.

`agent` runs the server and the TUI in one process. To run them apart, start `agent serve` (set `AGENT_TOKEN`) and connect with `agent attach http://host:7433`.

`shell` and `write` can do anything the process can, so run the server in a container if you want it isolated. MCP servers are configured in `mcp.yaml`; see `mcp.example.yaml`.

## Built with

Go, [Eino](https://github.com/cloudwego/eino) for agents, OpenRouter for every model, embedding and search call, Postgres with pgvector, [River](https://riverqueue.com) for jobs, sqlc and goose, a protobuf API on [Connect](https://connectrpc.com), and [Bubble Tea](https://github.com/charmbracelet/bubbletea) for the TUI. The TUI's scrolling list and spinner are copied from Crush, under its licence in `internal/tui/LICENSE-crush.md`.

## Docs

- [docs/architecture.md](docs/architecture.md): how the system works, what each package does, and why.
- [AGENTS.md](AGENTS.md): rules and commands for working on the code, for coding agents and people alike.
- [docs/conventions/](docs/conventions/): Go and testing conventions.
- [docs/recipes.md](docs/recipes.md): checklists for common changes.
