# Testing conventions

Every change comes with tests. `task check` runs them all, along with lint and the generated-code checks.

## Tiers

| Tier | What it covers | Needs | Run with |
|---|---|---|---|
| Unit | One package; collaborators replaced by mocks | Nothing | `task test:unit` (`go test -short`) |
| Database | Code that owns tables, against real Postgres | Docker | `task test` |
| Integration | The whole stack wired by `internal/app` (`testenv`) | Docker | `task test` |
| Live | Real OpenRouter calls | `OPENROUTER_API_KEY`, money | `task test:live` |

- **Unit tests** must not touch the network, Docker, the real clock or the shared filesystem. Use `t.TempDir()` for files and `httptest` for HTTP.
- **Anything that needs Docker** gets it through `testdb.Open` or `testenv.New`. Both skip under `-short`.
- **Live tests** sit behind the `live` build tag (`//go:build live`), and their names start with `TestLive`. They skip when the key is missing. They never run in `task test`.

## Table-driven tests

**Table-driven tests are the default.** When a function has more than one interesting case, write a table.

```go
func TestParseAgentConfig(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		in      string
		want    domain.AgentConfig
		wantErr string // substring; empty means no error
	}{
		{name: "empty object", in: `{}`, want: domain.AgentConfig{}},
		{name: "unknown key", in: `{"nope": 1}`, wantErr: "unknown field"},
		{name: "tool settings", in: `{"tools":{"web_search":{"max_results":3}}}`,
			want: domain.AgentConfig{Tools: domain.ToolConfig{"web_search": json.RawMessage(`{"max_results":3}`)}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := domain.ParseAgentConfig(json.RawMessage(tt.in))
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}
```

- **The table is called `tests`, and each case is `tt`.** Every case has a `name` that reads as a sentence fragment ("unknown key", "path escapes root").
- **Run each case in `t.Run(tt.name, ...)`.**
- **Expected values go in `want*` fields.** Use `wantErr string` (a substring) or `wantErr error` (checked with `errors.Is`), not a bare `bool`, so the test shows which error was expected.
- **Per-case setup goes in a field**, such as `setup func(m *MockStore)` for mock expectations, or `prepare func(t *testing.T, dir string)` for fixtures. Keep shared setup outside the loop.
- **Use `t.Parallel()`** at the top of the test and in each subtest, unless cases share mutable state such as a database row or a fixed port. Subtests that must run in order, such as a scenario over one database, are fine; say so in a comment.
- **Keep the table close to the test.** Large fixtures go in `testdata/`.
- **When not to use a table:** a scenario with ordered steps over shared state (like a conversation's turns), or a single case. Don't force a table where a few straight-line lines read better.

## Assertions

- **Use [testify](https://github.com/stretchr/testify):**
  - `require` when the test can't usefully continue (setup, errors);
  - `assert` for independent checks.
- **Put the expected value first:** `assert.Equal(t, want, got)`. `testifylint` checks this and other misuse.
- **Compare whole values** (structs, slices, JSON with `assert.JSONEq`) rather than field by field, so a failure shows the full diff.
- **Helpers call `t.Helper()` first** (`thelper` checks this) and take `t` as their first argument.

## Mocks

- **Generated mocks by default.** Use [mockery](https://vektra.github.io/mockery/) mocks for interaction-style tests, never a hand-written mock that does the same job. A hand-written fake is fine where it is genuinely simpler (see below). `.mockery.yml` generates a mock for every interface under `internal/`. Run `task mocks` after adding or changing an interface. `task check` fails if mocks are stale.
- **Mocks of our interfaces** go in each package's `mocks_test.go`, named `Mock<Interface>`. They're only compiled for tests, and both the package's internal tests and its external `_test` package can use them (for example `agent.NewMockSource(t)`).
- **Mocks of third-party interfaces we consume**, such as Eino's `Embedder`, `ToolCallingChatModel` and `InvokableTool`, live in `internal/mocks`. To mock another external interface, add it to `.mockery.yml`.
- **Build mocks with the generated constructor** (`NewMockX(t)`). It asserts at cleanup that every expectation was met.
- **Set expectations with the typed `EXPECT()` API:** `m.EXPECT().Get(mock.Anything, "admin").Return(detail, nil).Once()`.
  - Match context arguments with `mock.Anything`.
  - Match the other arguments exactly, or with `mock.MatchedBy` when exact matching isn't practical.
- **Don't mock what you don't own deeply.** Wrap a large third-party API behind a small interface in the consuming package, then mock that.
- **Hand-written fakes are the right choice when a flow doesn't mock nicely.** Some flows are easier to test with a small working implementation than with a script of expectations:
  - stateful collaborators, where later calls depend on earlier ones (an in-memory store, a recorder whose contents you assert on afterwards);
  - scripted playback, such as a fake model replaying a conversation;
  - callbacks, streams or concurrency, where expectations would be brittle or order-dependent;
  - one double shared by many tests, where each test would otherwise repeat the same setup.

  The test for it: use a fake if the mock version would be longer, harder to read, or would assert on how the code works rather than on what it does. Keep fakes minimal, put them in the package's test files (or `testenv` for whole-stack doubles), and add a one-line comment saying why it isn't a mock. Prove the fake satisfies the interface with `var _ Iface = (*fakeX)(nil)`, so it breaks at compile time when the interface changes.

## Database tests

- **Code that owns tables is tested against real Postgres with pgvector**, started by [testcontainers](https://golang.testcontainers.org/) through `testdb.Open(t)`. It runs every migration, so the tests also check the migrations.
  - Never mock `store.Store` or sqlc's `DBTX`.
  - Never point tests at the developer database from `task up`.
- **Each `testdb.Open` gets a fresh database of its own.** It is cloned in milliseconds from a migrated template in one container per test binary. Open one per test, or per independent case, and use `t.Parallel()` freely. A scenario can share one database across ordered subtests when the steps build on each other.
- **Test the behaviour the database enforces**, not only the happy path: constraints, triggers (immutable versions), locking, and cascades.
- **Integration tests use `testenv.New`** for the whole stack, with scripted models and fake search. Use them for cross-module behaviour such as chat turns, runs and subagents. Use unit tests for logic.

## What to test

- **Test behaviour through the exported API** (an external `package foo_test`). Use an internal test only for logic that is otherwise unreachable.
- **Every bug fix gets a test that fails without the fix.**
- **Cover error paths and edge cases:** empty input, limits, cancellation, concurrent access and partial failure. These are where agents and tools break.
- **No sleeps for synchronisation.** Poll with a deadline (`require.Eventually`), or use channels.
- **Tests must pass with `-race` and with `-count=N`.** Don't let them depend on ordering or on leftover state.
