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
4. **Expose management operations at `/mcp/admin`** in `internal/mcpserver` when clients need them. Consumer graph capabilities belong at `/mcp`. Both endpoints are unauthenticated loopback services; keep capability exposure separate from authentication. Use typed inputs and outputs, accurate annotations and consumer-side interfaces.
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

## Register graph types

The application registers the five versioned base knowledge types and `supported_by` during construction. All consumer and native data operations are generic across these base types and database-defined dynamic types. Use this recipe when changing static registrations; dynamic administration uses `create_node_type` and `create_edge_type` at `/mcp/admin` rather than Go registration.

1. **Choose persistence:** use `RegisterNode[T]` for current-state-only static nodes or `RegisterVersionedNode[T]` for immutable snapshots, history and rollback. Static node versioned-ness is fixed by the persisted registration and cannot be changed later. Dynamic nodes are always versioned. All dynamic nodes and all static or dynamic edges have immutable versions, current-version pointers, revisions and optional version metadata.
2. **Define the kind and data:** use a stable kind and a Go type implementing `graph.TextData`. Embed a common details type where that removes repetition, and keep its `NodeText` values identical to the required `title` and `description` JSON fields. Add optional domain validation and idempotent enrichment handlers in the spec. Title and description embeddings are always queued by the shared write path; never add an embedding handler to a node spec.
3. **Construct with dependencies:** call `graph.New(store, jobs)` with a non-nil River `JobInserter`. Changed writes require transactional enrichment insertion and fail if the inserter is nil.
4. **Register node kinds before edge kinds, then freeze:** register every static node and edge exactly once in each process, including the edge's complete permitted endpoint set. Finish with `graph.RegisterRiver(config, graph)` to freeze static registration and install the shared worker and `graph_enrichment` queue. Repeating a kind in one registry is an error; a later process must reproduce the persisted schema, node versioned-ness and edge endpoint set exactly. Dynamic type creation remains available after the freeze.
   - Registration does not migrate type definitions or snapshots. Put any `data_schema` change and corresponding current/historical JSON transformation in a new migration; do not rely on registering a changed Go type to rewrite data. Dynamic schemas have no definition history or mutation API, and incompatible schema evolution is unsupported.
5. **Use write and history semantics deliberately:** pass `ExpectedRevision` only for optimistic concurrency on existing entities. Treat `Before` as exclusive when paging node or edge version history, and pass back `NextBefore`. Node and edge lists default to 10 and are capped at 100; history defaults to 50 and is capped at 100. Generic node deletion fails while any edge connects the node. There is no edge semantic search.
6. **Test against Postgres** with `testdb.Open(t)`. Cover schema mismatch, fixed versioned-ness, repeated registration, endpoint constraints, no-op writes, revision conflicts, exclusive history cursors, metadata, edge history, connected-node deletion, enrichment insertion and stale jobs. Test dynamic administration through `CreateNodeType` and `CreateEdgeType` followed by generic operations. Node writes must also queue revision-bearing title/description embedding jobs, with compare-and-set persistence and sweep recovery. Assert `errors.Is` for `ErrNotFound`, `ErrExists` and `ErrConflict` where applicable.
7. **Update the graph section and package map** in `docs/architecture.md`. Keep internal infrastructure clearly distinct from user-facing knowledge-base direction.
8. Run `task check`.

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
