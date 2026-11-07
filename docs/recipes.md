# Recipes

Checklists for the common changes. Each ends with `task check`. Conventions are in [conventions/go.md](conventions/go.md) and [conventions/testing.md](conventions/testing.md).

## Add a tool

1. **Pick or create the tool set** under `internal/tools/<set>/`.
   - A new set needs a package comment and a row in the package map in `docs/architecture.md`.
   - It reaches services only through interfaces it declares itself; `depguard` checks this.
2. **Write the tool as a `kit.Spec`:** `Name`, a one-line `Summary` and `New`.
   - If the tool takes settings, add `Settings` (a struct with a `Validate() error` method) and `SettingsHelp`.
   - Put the model-facing description in `desc/<name>.md` and load it with `//go:embed`.
   - Return failures through `kit.Result` so the model sees them. Keep output within `kit.MaxOutput`.
3. **Register the set** in `generalTools` in `internal/app/app.go`.
4. **Expose management operations over MCP** in `internal/mcpserver` when clients need them. Use typed inputs and outputs, accurate annotations and consumer-side interfaces.
5. **Write tests** (table-driven):
   - its input handling and its errors;
   - its settings decoding;
   - one test through the registry, if config rules apply;
   - MCP operations through an MCP client.
6. **Update the docs:** the MCP API or workflow-agent tool sections in `docs/architecture.md`.
7. Run `task check`.

## Change the schema or add queries

1. **Add a migration** with `task migration:new -- <name>`. Never edit one that has been committed. Write a `-- +goose Down` section when it can be undone.
2. **Write the queries** in `internal/store/queries/<table>.sql`, then run `task sqlc`.
3. **Call the queries only from the table's owning package.** The ownership list is in `docs/architecture.md`; `depguard` checks it. Return `domain` types, never `db.*` rows. Wrap multi-statement writes in `store.InTx`.
4. **Test against Postgres** with `testdb.Open(t)`. Cover the new constraints and triggers as well as the happy path.
5. **Update the docs:** the data model in `docs/architecture.md`, and `domain` types if callers need them.
6. Run `task check`.

## Add a service, or a dependency between packages

1. **Declare the interface in the consumer:** only the methods it calls, with a doc comment naming the implementation.
2. **Return a concrete `*T`** from the provider's constructor. Add a compile-time assertion, `var _ consumer.Iface = (*provider.T)(nil)`, where it's wired or in a test.
3. **Wire it in `internal/app/app.go`** in dependency order. Never with a setter after construction.
4. **Run `task mocks`.** The consumer's tests use `NewMockIface(t)`, or a small fake where that reads better (see [testing.md](conventions/testing.md#mocks)).
5. **If it's a new package,** add a package comment and a package-map row. Add a `depguard` rule to `.golangci.yml` if it has a boundary worth enforcing.
6. Run `task check`.

## Add a setting

1. **Add the field to `config.Config`,** with an env var, a default and validation in `Load`.
2. **Add it to `.env.example`** with a comment.
3. **Document it** in the Configuration section of `docs/architecture.md`.
4. **Add a table case** to `internal/config/config_test.go` for the default and for invalid values.
5. Run `task check`.

## Fix a bug

1. **Write a test that fails** because of the bug.
2. **Fix it,** and check the test now passes.
3. **Look for the same mistake nearby,** and fix it too or note it.
4. Run `task check`.
