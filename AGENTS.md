# AGENTS.md

Guidance for AI agents and people working in this repo. Read this first, then the documents it links to.

**personal-agent** is a Postgres-backed knowledge and capability service that complements a personal agent. External consumer agents own human-facing LLM interactions; this service owns durable knowledge, deterministic question and evidence tooling, versioned workflow instruction sets and background capabilities suitable for cloud deployment. The current implementation is an orchestration and knowledge-graph MCP server, not a conversational or question-answering product.
- It serves 24 agent, workflow, run and knowledge-graph tools over unauthenticated, stateless Streamable HTTP and runs explicitly requested workflows in the background.
- Workflow agents use Eino over OpenRouter, Postgres and pgvector for storage, and River for jobs.
- Workflows are instruction sets, not queries run against the knowledge base. External consumer agents may author, test and iterate them using immutable versions; do not propose or implement workflow changes as part of the product boundary.
- Agent and workflow definitions, and graph-node snapshots, are immutable and stored in the database.
- `internal/graph` is wired into `internal/app`, which registers versioned objective, workflow, claim, source and agent types and the claim-to-source `supported_by` edge. MCP exposes node upsert, semantic search and support-relationship add, list and delete; enabled workflow agents expose node upsert and semantic search. Every changed node queues title and description embeddings.
- Deterministic question tooling will return structured nodes and evidence rather than conversational answers. Ask a Question is future direction. The existing internal agent loop is retained for enrichment jobs and scheduled tasks only in the target boundary; schedules are future and are not implemented. Cloud deployment and authentication are also not implemented. Memory has been removed; do not document or build on it as existing behaviour.

[docs/architecture.md](docs/architecture.md) describes the system as it is now.

## Hard rules

1. **Never read, print or commit `.env`.** It holds the user's API keys. Tasks load it through `dotenv`; you never need its contents. Use `.env.example` to learn the settings.
2. **A change is done only when `task check` passes.** It runs generated-code and `go.mod` drift checks, the docs check, lint, vulnerability checks and every test except live ones. Never mark work complete with failures. Never skip or weaken a check to get green.
3. **Never edit generated code:** `internal/store/db` (sqlc), `**/mocks_test.go` and `internal/mocks` (mockery). Change the source and run `task generate`.
4. **Never edit a committed migration.** Add a new one with `task migration:new -- name`.
5. **Agent, workflow and graph-node versions are immutable.** Change agent and workflow definitions by adding a new version; change versioned nodes by appending a snapshot or moving the current pointer with the graph repository.
6. **The MCP server deliberately has no auth.** Keep the default listener on loopback, preserve DNS-rebinding and cross-origin protection, and never store secrets in tool output.
7. **Make modular atomic commits for each completed task, including delegated subagent work. Push only when asked.**

## Commands

Everything runs through [Task](https://taskfile.dev). See [Taskfile.yml](Taskfile.yml) for the full list (`task` prints it).

| Command | What it does |
|---|---|
| `task tools` | Install pinned golangci-lint, sqlc, goose and govulncheck (mockery runs via `go tool`) |
| `task up` / `task down` | Start or stop the local Postgres |
| `task run` | Migrate, start background workers and serve MCP on `MCP_LISTEN` |
| `task install` / `task uninstall` | Put the `orchestrator` binary on your PATH (GOBIN, else GOPATH/bin), or remove it |
| `task generate` | Regenerate sqlc code and mocks |
| `task fmt` | Format code |
| `task lint` | golangci-lint |
| `task test:unit` | Unit tests only; no Docker or network |
| `task test` | Unit, database and whole-stack integration tests (needs Docker) |
| `task vuln` | govulncheck; accepted findings go in `.govulncheck-ignore` with a reason |
| `task docs:check` | Every package has a doc comment and appears in the architecture doc |
| `task check:fast` | Lint issues new since HEAD, unit tests and the docs check; no Docker |
| `task test:live` | Tests against real OpenRouter (costs money; run only when asked) |
| `task check` | **Everything above except live tests. Must pass before work is done.** |

Use `task check:fast` (or `task test:unit`) for a fast loop while working. Run `task check` before you finish. To target one package or test, pass arguments after `--`: `task test -- -run TestChat ./internal/chat`.

## Conventions

These are mandatory. Read the full documents before writing code or tests:

- [docs/conventions/go.md](docs/conventions/go.md) covers design (interfaces at module boundaries, the consumer defines them, one composition root, no late binding), errors, context and concurrency, style, SQL and dependencies.
- [docs/conventions/testing.md](docs/conventions/testing.md) covers the test tiers, table-driven tests, testify, mockery mocks and database tests on testcontainers.

For common changes, such as adding a tool, a migration, a service or a setting, follow the checklists in [docs/recipes.md](docs/recipes.md).

In short:
- **Modular through interfaces.** A package that uses another package's service depends on a small interface that it declares itself. `internal/app` wires everything up.
- **Table-driven tests by default.** Use `tests` / `tt` / `t.Run(tt.name, ...)`, with `want*` fields and testify assertions.
- **Generated mocks by default.** Run `task mocks` after changing an interface. Hand-written fakes are fine where a flow genuinely doesn't mock nicely (stateful, scripted, concurrent); give each one a comment saying why.
- **Real Postgres for the database layer.** Use `testdb.Open(t)` (testcontainers), never a mocked store.
- **Module boundaries are linted.** `depguard` rules in `.golangci.yml` stop packages importing services, sqlc queries or the composition root that they shouldn't.

## Review

Before a non-trivial change is committed, have it reviewed by a fresh reviewer: a separate agent session or subagent, or a person. Give them [docs/review.md](docs/review.md) and the diff, not your reasoning. Fix blockers, fix or justify `should`s, and re-run `task check`.

## Where things live

```
cmd/orchestrator/        MCP server and background-worker entrypoint
internal/mcpserver/      typed MCP tools and Streamable HTTP transport
internal/app/            composition root: builds and wires everything
internal/domain/         shared types and sentinel errors
internal/graph/          generic typed graph, immutable node history, enrichment and semantic search
internal/knowledge/      first-class objective, workflow, claim, source and agent types
internal/<service>/      catalog, chat, runs, agent, embed, ...
internal/tools/          workflow-agent tool registry; one subpackage per tool set
internal/store/          pool, migrations, queries (sqlc output in store/db)
internal/testdb/         isolated Postgres test databases in disposable containers
internal/testenv/        whole-stack integration environment with scripted models
docs/                    architecture and conventions
```

## Keeping docs current

Docs are part of the change, not a follow-up. In the same change:

- **New or renamed package:** give it a package comment and add it to the package map in `docs/architecture.md`. `task docs:check` enforces both.
- **Behaviour, data model, tools, config or startup changes:** update the matching section of `docs/architecture.md`.
- **New setting:** add it to `.env.example` and to the configuration section of the architecture doc.
- **New or changed task:** update the command table above.
- **New convention or exception:** write it into `docs/conventions/` and add a lint rule to `.golangci.yml` if one can enforce it.
- **MCP tools change:** document their semantics in `docs/architecture.md` and test them through an MCP client.
- **These rules turn out to be wrong or incomplete:** fix this file.

Write docs in plain British English and use the present tense for implemented behaviour. Keep future product direction clearly labelled and separate from current capabilities; never describe Ask a Question or removed memory APIs as available to users.
