# AGENTS.md

Guidance for AI agents and people working in this repo. Read this first, then the documents it links to.

**personal-agent** is a personal AI agent for work and home, written in Go.
- It uses Eino agents over OpenRouter, with Postgres and pgvector for storage and River for jobs.
- Agents and workflows are versioned and stored in the database.
- An admin agent builds and maintains the others.

[docs/architecture.md](docs/architecture.md) describes the system as it is now.

## Hard rules

1. **Never read, print or commit `.env`.** It holds the user's API keys. Tasks load it through `dotenv`; you never need its contents. Use `.env.example` to learn the settings.
2. **A change is done only when `task check` passes.** It runs:
   - the generated-code and `go.mod` drift checks;
   - the docs check;
   - lint;
   - every test except live ones.
   Never mark work complete with failures. Never skip or weaken a check to get green.
3. **Never edit generated code:** `internal/store/db` (sqlc), `**/mocks_test.go` and `internal/mocks` (mockery). Change the source and run `task generate`.
4. **Never edit a committed migration.** Add a new one with `task migration:new -- name`.
5. **Agent and workflow versions are immutable.** Change them by adding a new version.
6. **Memory is on hold.** The user is designing the memory subsystem. Leave `memories`, `search_memories` and `remember` alone unless asked.
7. **Commit or push only when asked.**

## Commands

Everything runs through [Task](https://taskfile.dev). See [Taskfile.yml](Taskfile.yml) for the full list (`task` prints it).

| Command | What it does |
|---|---|
| `task tools` | Install pinned golangci-lint, sqlc and goose (mockery runs via `go tool`) |
| `task up` / `task down` | Start or stop the local Postgres |
| `task run` | Run agentd (it migrates on start) |
| `task generate` | Regenerate sqlc code and mocks |
| `task fmt` | Format code |
| `task lint` | golangci-lint |
| `task test:unit` | Unit tests only; no Docker or network |
| `task test` | Unit, database and integration tests (needs Docker) |
| `task vuln` | govulncheck; accepted findings go in `.govulncheck-ignore` with a reason |
| `task check:fast` | Lint issues new since HEAD, unit tests and the docs check; no Docker |
| `task test:live` | Tests against real OpenRouter (costs money; run only when asked) |
| `task docs:check` | Every package has a doc comment and appears in the architecture doc |
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
cmd/agentd/              entrypoint
internal/app/            composition root: builds and wires everything
internal/domain/         shared types and sentinel errors
internal/<service>/      catalog, chat, runs, agent, embed, ...
internal/tools/          tool registry; one subpackage per tool set
internal/store/          pool, migrations, queries (sqlc output in store/db)
internal/testdb, testenv test infrastructure (Postgres container, whole stack)
docs/                    architecture and conventions
```

## Keeping docs current

Docs are part of the change, not a follow-up. In the same change:

- **New or renamed package:**
  - give it a package comment;
  - add it to the package map in `docs/architecture.md`.
  `task docs:check` enforces both.
- **Behaviour, data model, tools, config or startup changes:** update the matching section of `docs/architecture.md`.
- **New setting:** add it to `.env.example` and to the configuration section of the architecture doc.
- **New or changed task:** update the command table above.
- **New convention or exception:**
  - write it into `docs/conventions/`;
  - add a lint rule to `.golangci.yml` if one can enforce it.
- **Admin agent capabilities change:** update `internal/agent/admin_prompt.md`.
- **These rules turn out to be wrong or incomplete:** fix this file.

Write docs in plain British English, in the present tense, describing what exists rather than plans.
