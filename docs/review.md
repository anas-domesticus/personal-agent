# Review brief

A brief for reviewing a change before it is committed. Give it to a fresh reviewer: a person, or a separate agent session or subagent. It shouldn't be the author's own context, because the point is a reading that isn't anchored on the author's reasoning.

To use it, hand the reviewer this file and the diff (`git diff`, or `git diff main...HEAD` for a branch), and nothing else. Ask the reviewer to report findings and not to edit code.

---

You are reviewing a change to this repo. Read `AGENTS.md`, `docs/conventions/go.md`, `docs/conventions/testing.md` and the parts of `docs/architecture.md` the diff touches. Then review the diff against them. Read the surrounding code wherever the diff alone doesn't show enough.

Check each of these, in order of importance:

1. **Correctness.** Logic errors, unhandled errors, nil dereferences, races (shared state without a lock, goroutines without an owner), leaked resources (bodies, rows, goroutines, contexts), and missing limits or timeouts on external calls.
2. **Safety.** Secrets reaching logs, tool output or child processes. Paths escaping the workspace. Untrusted input (model output, web content, MCP results) treated as trusted. Anything that reads `.env`.
3. **Data.**
   - Migrations are append-only, and existing rows still load.
   - Multi-statement writes are in a transaction.
   - Only the owning package writes to a table.
   - Versions are never updated in place.
4. **Boundaries.**
   - Cross-package dependencies go through interfaces the consumer declares.
   - Concrete services are wired only in `internal/app`.
   - Domain types are returned, never sqlc rows.
   - There is no late binding.
5. **Tests.**
   - New behaviour and every fixed bug have tests.
   - Tests are table-driven where the cases share a shape, and use testify with the expected value first.
   - Mocks are generated, and any hand-written fake says why it isn't a mock.
   - Database code is tested against `testdb`.
   - There are no sleeps, and tests are parallel where safe.
   - Tests check behaviour, not implementation details.
6. **Docs.**
   - Changed behaviour, settings, tools or packages are reflected in `docs/architecture.md`, `.env.example`, the `AGENTS.md` command table, or `admin_prompt.md`.
   - New packages have a package comment.
7. **Simplicity.** Code that could be deleted or reuse something that exists. Abstractions with one caller and no reason to exist. Comments that say what rather than why.

Report each finding as:

```
[severity] path/to/file.go:123 — what is wrong
  why it matters / the failing scenario
  suggested fix
```

Severity is `blocker` (a bug, data loss, a security problem, or a broken convention the linter can't catch), `should` (fix before merging unless there's a reason not to) or `nit`.

Order findings by severity. Say "no findings" if there are none. Don't pad the report, don't restate the diff, and don't report what `task check` already enforces (formatting, lint rules, stale generated code).
