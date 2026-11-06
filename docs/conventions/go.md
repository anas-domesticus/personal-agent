# Go conventions

How Go is written in this repo. It distils [Effective Go](https://go.dev/doc/effective_go), the [Go Code Review Comments](https://go.dev/wiki/CodeReviewComments) and [Google's Go style guide](https://google.github.io/styleguide/go/), plus decisions specific to this codebase. `golangci-lint` (`.golangci.yml`) enforces what a linter can. The rest is on reviewers, human or agent.

## Design

These rules keep the modules separable, so each can be tested alone and replaced without touching its callers.

- **Interfaces at module boundaries.** When package A uses a service from package B, A depends on an interface rather than B's concrete type.
  - Exceptions: `domain` types, small value types such as `kit.Spec` or `llm.Spec`, and the standard library.
  - The database layer is another exception. `store.Store` is used directly by the packages that own tables, and those packages are tested against real Postgres (see [testing.md](testing.md)).
- **The consumer defines the interface.** Declare it in the package that uses it, holding only the methods that package calls. Examples: `chat.Store`, `runs.Recorder`, `tools.MCPSource`.
  - Keep interfaces small; one to five methods is typical.
  - Document which concrete type implements it ("*catalog.Agents implements it").
  - Don't add an interface to the providing package just in case. The exception is a seam with several implementations, such as `workspace.Workspace` and `search.Searcher`.
- **Accept interfaces, return concrete types.** Constructors return `*T`, not an interface, so callers can see everything `T` offers.
- **Prove that a type satisfies an interface** with a compile-time assertion where the two are wired together, or in the implementation's package when the interface lives there: `var _ Workspace = (*Local)(nil)`.
- **There is one composition root.** `internal/app` builds everything in dependency order and passes dependencies through constructors.
  - No globals or `init()` side effects.
  - No service locators.
  - No late binding: no setters called after construction, and no `SetX` to break a cycle. A cycle is a design problem: split the type, or move the shared piece into the consumer.
- **Domain types cross boundaries; storage types don't.** Services return `domain.*`, never sqlc rows, so callers don't depend on the shape of a query.
- **Each table has one writer.** The package that owns a table is the only one that writes to it (see the ownership list in [architecture.md](../architecture.md)).
- **Packages are named for what they provide** (`workspace`, `search`, `runs`), never `util`, `common` or `helpers`. Don't repeat the package name in its identifiers: `runs.Service`, not `runs.RunService`.
- **Keep the API small.** Unexport by default. Export only what another package uses.

## Errors

- **Wrap with context** when crossing a boundary: `fmt.Errorf("load agent %q: %w", name, err)`. The message says what was being done, in lowercase, with no "failed to" and no trailing punctuation.
- **Errors from third-party packages are always wrapped** (`wrapcheck` checks this). Errors from our own packages can be returned as they are when they already say enough.
- **Callers branch on sentinel errors in `domain`** (`ErrNotFound`, `ErrExists`, `ErrBusy`, `ErrConflict`), which are wrapped with `%w` and checked with `errors.Is`. Never compare errors with `==`, and never match on error strings.
- **Handle an error once.** Either return it or log it, never both.
- **No `panic` in library code**, except for programmer errors that are impossible by construction. Recover only at goroutine and job boundaries, as the run worker does.
- **Tool errors go back to the model** as `{"ok": false, "error": ...}` (`kit.Result`), so it can correct itself. They are not Go errors that abort the turn.

## Context, concurrency and lifetimes

- **`ctx context.Context` comes first** in anything that does I/O or might block. Never store a context in a struct; the exception is a type's own lifetime context, created in `Start`.
- **Use `context.WithoutCancel`** for work that must finish even if the caller has gone away, such as saving a turn that has already been paid for.
- **Every goroutine has an owner and a way to stop.** Tie it to a context or a `Close`, and wait for it on shutdown. Don't fire and forget.
- **Guard shared state with a mutex next to the fields it protects.** Prefer to stop sharing state at all. Run `-race` in tests (`task test` does).
- **Bound timeouts and sizes on anything external:** HTTP calls, tool outputs, command runs and fetched bodies.

## Style

- **Formatting** is gofmt plus goimports, with this module's imports in their own group (`task fmt`).
- **Doc comments.** Every package has one, which `task docs:check` checks. Every exported identifier has a full-sentence comment that starts with its name. Comments say *why*; the code says *what*.
- **Spelling.** Prose in comments and docs uses British English. Identifiers follow the libraries they wrap (`Initialize`, `color`).
- **Names.** Short names for short scopes (`i`, `r`, `ctx`). Descriptive names for wide scopes. Initialisms stay upper case (`ID`, `URL`, `HTTP`).
- **Return early** and keep the happy path unindented. Avoid `else` after `return`.
- **Zero values are useful.** Design structs so the zero value works, or make it impossible to build one without its constructor.
- **Config structs** take optional settings. Use `With*` functional options only where a public API needs them to evolve.
- **No naked returns** in functions longer than a few lines. Only name results when that documents them.
- **Use generics** where they remove real duplication (`sortedKeys`). Don't use them for abstraction alone.
- **Embedded files.** Prompts and tool descriptions go in `.md` files, loaded with `//go:embed`, not in string literals.

## Data and SQL

- Schema changes are goose migrations (`task migration:new -- name`). Migrations are append-only; never edit one that has been committed.
- Queries live in `internal/store/queries/*.sql`, and `task sqlc` generates their Go code. Never hand-edit `internal/store/db`.
- Multi-statement writes go in `store.InTx`. Lock the parent row (`FOR UPDATE`) when numbering children.
- JSON stored in the database is strict on write and lenient on read. Validate with `DisallowUnknownFields` when saving, and tolerate removed fields when loading old rows.

## Dependencies

- **Add a dependency only when it removes real work.** Prefer the standard library.
- **Pin tool versions.** `mockery` is a `tool` directive in `go.mod`, run with `go tool mockery`. The other tools are pinned in `Taskfile.yml` and installed with `task tools`.
- **Keep the module tidy.** Run `go mod tidy` after any dependency change; `task check` fails on drift.
