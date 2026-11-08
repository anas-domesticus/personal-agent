package graph_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"sync"
	"testing"

	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/riverqueue/river"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/anas-domesticus/personal-agent/internal/domain"
	"github.com/anas-domesticus/personal-agent/internal/graph"
	"github.com/anas-domesticus/personal-agent/internal/testdb"
)

var dynamicNodeSchema = json.RawMessage(`{
  "type":"object",
  "properties":{"title":{"type":"string"},"description":{"type":"string"},"name":{"type":"string"}},
  "required":["title","description","name"],
  "additionalProperties":false
}`)

func TestDynamicTypesNodesEdgesAndWorkers(t *testing.T) {
	t.Parallel()
	st, first, jobs := newGraph(t)
	ctx := context.Background()
	secondJobs := &jobRecorder{}
	second := graph.New(st, secondJobs)

	personType, err := first.CreateNodeType(ctx, "dynamic_person", dynamicNodeSchema)
	require.NoError(t, err)
	assert.True(t, personType.Versioned)
	assert.NotZero(t, personType.CreatedAt)
	_, err = first.CreateNodeType(ctx, "dynamic_person", dynamicNodeSchema)
	require.ErrorIs(t, err, domain.ErrExists)
	_, err = first.CreateNodeType(ctx, "Bad Kind", dynamicNodeSchema)
	require.Error(t, err)
	_, err = first.CreateNodeType(ctx, "bad_schema", json.RawMessage(`[]`))
	require.Error(t, err)

	// Dynamic administration and use do not depend on this process's frozen
	// static registry.
	graph.RegisterRiver(&river.Config{}, first)
	_, err = first.CreateNodeType(ctx, "dynamic_place", dynamicNodeSchema)
	require.NoError(t, err)
	visible, err := second.GetNodeType(ctx, "dynamic_person")
	require.NoError(t, err)
	assert.Equal(t, personType, visible)
	types, err := second.ListNodeTypes(ctx)
	require.NoError(t, err)
	assert.Len(t, types, 2)

	ada, err := first.UpsertNode(ctx, "dynamic_person", uuid.Nil,
		json.RawMessage(`{"title":"Ada","description":"Mathematician","name":"Ada"}`),
		graph.NodeWriteOptions{Metadata: json.RawMessage(`{"source":"test"}`)})
	require.NoError(t, err)
	assert.Equal(t, int64(1), ada.CurrentVersion)
	grace, err := second.UpsertNode(ctx, "dynamic_person", uuid.Nil,
		json.RawMessage(`{"title":"Grace","description":"Computer scientist","name":"Grace"}`), graph.NodeWriteOptions{})
	require.NoError(t, err)
	loaded, err := second.GetNode(ctx, "dynamic_person", ada.ID)
	require.NoError(t, err)
	assert.Equal(t, ada, loaded)
	listed, err := second.ListNodes(ctx, "dynamic_person", 10)
	require.NoError(t, err)
	assert.Len(t, listed, 2)

	_, err = second.UpsertNode(ctx, "dynamic_person", uuid.Nil,
		json.RawMessage(`{"title":"Missing name","description":"Invalid"}`), graph.NodeWriteOptions{})
	require.ErrorContains(t, err, "validate graph data")
	_, err = second.UpsertNode(ctx, "dynamic_person", uuid.Nil,
		json.RawMessage(`{"title":"Unknown","description":"Invalid","name":"Unknown","extra":true}`), graph.NodeWriteOptions{})
	require.Error(t, err)
	_, err = second.UpsertNode(ctx, "dynamic_person", uuid.Nil,
		json.RawMessage(`{"description":"Missing title","name":"Unknown"}`), graph.NodeWriteOptions{})
	require.ErrorContains(t, err, "validate graph data")

	unchanged, err := second.UpsertNode(ctx, "dynamic_person", ada.ID, ada.Data, graph.NodeWriteOptions{
		WriteOptions: graph.WriteOptions{ExpectedRevision: &ada.Revision},
		Metadata:     json.RawMessage(`{"ignored":true}`),
	})
	require.NoError(t, err)
	assert.Equal(t, ada, unchanged)
	updated, err := second.UpsertNode(ctx, "dynamic_person", ada.ID,
		json.RawMessage(`{"title":"Ada Lovelace","description":"First programmer","name":"Ada"}`),
		graph.NodeWriteOptions{Metadata: json.RawMessage(`{"reason":"correction"}`)})
	require.NoError(t, err)
	assert.Equal(t, int64(2), updated.CurrentVersion)
	assert.Equal(t, int64(2), updated.Revision)
	stale := int64(1)
	_, err = second.UpsertNode(ctx, "dynamic_person", ada.ID, updated.Data,
		graph.NodeWriteOptions{WriteOptions: graph.WriteOptions{ExpectedRevision: &stale}})
	require.ErrorIs(t, err, domain.ErrConflict)
	history, err := second.NodeHistory(ctx, "dynamic_person", ada.ID, graph.HistoryOptions{Limit: 1})
	require.NoError(t, err)
	require.Len(t, history.Versions, 1)
	assert.Equal(t, int64(2), history.Versions[0].Version)
	assert.Equal(t, int64(2), history.NextBefore)
	older, err := second.NodeHistory(ctx, "dynamic_person", ada.ID, graph.HistoryOptions{Before: history.NextBefore})
	require.NoError(t, err)
	require.Len(t, older.Versions, 1)
	assert.JSONEq(t, `{"source":"test"}`, string(older.Versions[0].Metadata))

	edgeType, err := first.CreateEdgeType(ctx, "dynamic_knows", json.RawMessage(`{
	  "type":"object","properties":{"since":{"type":"integer"}},"required":["since"],"additionalProperties":false
	}`), []graph.Endpoint{{Source: "dynamic_person", Target: "dynamic_person"}})
	require.NoError(t, err)
	assert.Len(t, edgeType.Endpoints, 1)
	_, err = first.CreateEdgeType(ctx, "dynamic_knows", edgeType.DataSchema, []graph.Endpoint{{Source: "dynamic_person", Target: "dynamic_person"}})
	require.ErrorIs(t, err, domain.ErrExists)
	_, err = first.CreateEdgeType(ctx, "bad_endpoints", json.RawMessage(`{}`), nil)
	require.Error(t, err)
	_, err = first.CreateEdgeType(ctx, "bad_endpoints", json.RawMessage(`{}`), []graph.Endpoint{{Source: "missing", Target: "dynamic_person"}})
	require.ErrorIs(t, err, domain.ErrConflict)
	_, err = second.GetEdgeType(ctx, "dynamic_knows")
	require.NoError(t, err)
	edgeTypes, err := second.ListEdgeTypes(ctx)
	require.NoError(t, err)
	assert.Len(t, edgeTypes, 1)

	edge, err := first.UpsertEdge(ctx, "dynamic_knows",
		graph.Ref{ID: ada.ID, Kind: "dynamic_person"}, graph.Ref{ID: grace.ID, Kind: "dynamic_person"},
		json.RawMessage(`{"since":1840}`), graph.EdgeWriteOptions{Metadata: json.RawMessage(`{"source":"test"}`)})
	require.NoError(t, err)
	assert.Equal(t, int64(1), edge.CurrentVersion)
	unchangedEdge, err := second.UpsertEdge(ctx, "dynamic_knows",
		graph.Ref{ID: ada.ID, Kind: "dynamic_person"}, graph.Ref{ID: grace.ID, Kind: "dynamic_person"},
		json.RawMessage(`{"since":1840}`), graph.EdgeWriteOptions{Metadata: json.RawMessage(`{"ignored":true}`)})
	require.NoError(t, err)
	assert.Equal(t, edge, unchangedEdge)
	updatedEdge, err := second.UpsertEdge(ctx, "dynamic_knows",
		graph.Ref{ID: ada.ID, Kind: "dynamic_person"}, graph.Ref{ID: grace.ID, Kind: "dynamic_person"},
		json.RawMessage(`{"since":1843}`), graph.EdgeWriteOptions{Metadata: json.RawMessage(`{"reason":"research"}`)})
	require.NoError(t, err)
	assert.Equal(t, int64(2), updatedEdge.CurrentVersion)
	assert.Equal(t, int64(2), updatedEdge.Revision)
	_, err = second.UpsertEdge(ctx, "dynamic_knows",
		graph.Ref{ID: grace.ID, Kind: "dynamic_person"}, graph.Ref{ID: ada.ID, Kind: "dynamic_place"},
		json.RawMessage(`{"since":1843}`), graph.EdgeWriteOptions{})
	require.ErrorIs(t, err, domain.ErrConflict)
	_, err = second.UpsertEdge(ctx, "dynamic_knows",
		graph.Ref{ID: ada.ID, Kind: "dynamic_person"}, graph.Ref{ID: grace.ID, Kind: "dynamic_person"},
		json.RawMessage(`[]`), graph.EdgeWriteOptions{})
	require.ErrorContains(t, err, "JSON object")

	edges, err := second.ListEdges(ctx, "", ada.ID, uuid.Nil, 10)
	require.NoError(t, err)
	require.Len(t, edges, 1)
	assert.Equal(t, updatedEdge, edges[0])
	edgeHistory, err := second.EdgeHistory(ctx, "dynamic_knows", edge.ID, graph.HistoryOptions{Limit: 1})
	require.NoError(t, err)
	require.Len(t, edgeHistory.Versions, 1)
	assert.Equal(t, int64(2), edgeHistory.NextBefore)
	_, err = st.Pool.Exec(ctx, `update graph_edge_versions set data = '{"since":1900}' where edge_id = $1 and version = 1`, edge.ID)
	require.ErrorContains(t, err, "immutable")

	// A separate process with no static registration acknowledges dynamic jobs.
	worker := &graph.Worker{Graph: second}
	for _, args := range jobs.recorded() {
		require.NoError(t, worker.Work(ctx, &river.Job[graph.EnrichmentArgs]{Args: args}))
	}
	assert.NotEmpty(t, secondJobs.recorded())

	require.ErrorIs(t, second.DeleteNode(ctx, "dynamic_person", ada.ID, graph.WriteOptions{ExpectedRevision: &updated.Revision}), domain.ErrConflict)
	require.ErrorIs(t, second.DeleteEdge(ctx, "dynamic_knows", edge.ID, graph.WriteOptions{ExpectedRevision: &stale}), domain.ErrConflict)
	require.NoError(t, second.DeleteEdge(ctx, "dynamic_knows", edge.ID, graph.WriteOptions{ExpectedRevision: &updatedEdge.Revision}))
	require.NoError(t, second.DeleteNode(ctx, "dynamic_person", ada.ID, graph.WriteOptions{ExpectedRevision: &updated.Revision}))
	_, err = second.GetNode(ctx, "dynamic_person", ada.ID)
	require.ErrorIs(t, err, domain.ErrNotFound)
}

func TestEdgeRowsMigrateToVersionOne(t *testing.T) {
	t.Parallel()
	_, url := testdb.Open(t)
	ctx := context.Background()
	database, err := sql.Open("pgx", url)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, database.Close()) })
	provider, err := goose.NewProvider(goose.DialectPostgres, database, os.DirFS("../store/migrations"))
	require.NoError(t, err)
	for range 2 {
		_, err = provider.Down(ctx)
		require.NoError(t, err)
	}
	_, err = database.ExecContext(ctx, `
		insert into graph_node_types (kind, versioned, data_schema) values ('legacy', false, '{}');
		insert into graph_nodes (id, kind, versioned, data) values
		  ('00000000-0000-0000-0000-000000000001', 'legacy', false, '{"title":"One","description":"One"}'),
		  ('00000000-0000-0000-0000-000000000002', 'legacy', false, '{"title":"Two","description":"Two"}');
		insert into graph_edge_types (kind, data_schema) values ('legacy_edge', '{}');
		insert into graph_edge_type_endpoints values ('legacy_edge', 'legacy', 'legacy');
		insert into graph_edges (id, kind, source_id, source_kind, target_id, target_kind, data, revision)
		values ('00000000-0000-0000-0000-000000000003', 'legacy_edge',
		  '00000000-0000-0000-0000-000000000001', 'legacy',
		  '00000000-0000-0000-0000-000000000002', 'legacy', '{"weight":7}', 4);`)
	require.NoError(t, err)
	_, err = provider.Up(ctx)
	require.NoError(t, err)
	var version, revision int64
	var data json.RawMessage
	require.NoError(t, database.QueryRowContext(ctx, `
		select e.current_version, e.revision, v.data from graph_edges e
		join graph_edge_versions v on v.edge_id = e.id and v.version = e.current_version
		where e.id = '00000000-0000-0000-0000-000000000003'`).Scan(&version, &revision, &data))
	assert.Equal(t, int64(1), version)
	assert.Equal(t, int64(4), revision)
	assert.JSONEq(t, `{"weight":7}`, string(data))
}

func TestConcurrentFirstEdgeUpsertsReturnSameEdge(t *testing.T) {
	t.Parallel()
	st, g, jobs := newGraph(t)
	ctx := context.Background()
	_, err := g.CreateNodeType(ctx, "concurrent_node", dynamicNodeSchema)
	require.NoError(t, err)
	_, err = g.CreateEdgeType(ctx, "concurrent_edge", json.RawMessage(`{"type":"object"}`),
		[]graph.Endpoint{{Source: "concurrent_node", Target: "concurrent_node"}})
	require.NoError(t, err)
	source, err := g.UpsertNode(ctx, "concurrent_node", uuid.Nil,
		json.RawMessage(`{"title":"Source","description":"Source","name":"source"}`), graph.NodeWriteOptions{})
	require.NoError(t, err)
	target, err := g.UpsertNode(ctx, "concurrent_node", uuid.Nil,
		json.RawMessage(`{"title":"Target","description":"Target","name":"target"}`), graph.NodeWriteOptions{})
	require.NoError(t, err)

	const writers = 16
	start := make(chan struct{})
	results := make(chan domain.KnowledgeEdge, writers)
	errors := make(chan error, writers)
	var wait sync.WaitGroup
	for range writers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			edge, err := g.UpsertEdge(ctx, "concurrent_edge",
				graph.Ref{ID: source.ID, Kind: source.Kind}, graph.Ref{ID: target.ID, Kind: target.Kind},
				json.RawMessage(`{"weight":7}`), graph.EdgeWriteOptions{})
			results <- edge
			errors <- err
		}()
	}
	close(start)
	wait.Wait()
	close(results)
	close(errors)
	for err := range errors {
		require.NoError(t, err)
	}
	var first domain.KnowledgeEdge
	for edge := range results {
		if first.ID == uuid.Nil {
			first = edge
		}
		assert.Equal(t, first, edge)
	}
	assert.NotEqual(t, uuid.Nil, first.ID)
	listed, err := g.ListEdges(ctx, "concurrent_edge", source.ID, target.ID, 100)
	require.NoError(t, err)
	require.Len(t, listed, 1)
	assert.Equal(t, int64(1), listed[0].Revision)
	assert.Equal(t, int64(1), listed[0].CurrentVersion)
	var count int
	require.NoError(t, st.Pool.QueryRow(ctx, `select count(*) from graph_edges where kind = 'concurrent_edge'`).Scan(&count))
	assert.Equal(t, 1, count)
	assert.Len(t, jobs.recorded(), 3, "two nodes and only one edge creation should enqueue enrichment")
}

func TestDynamicWriteRollsBackWhenQueueFails(t *testing.T) {
	t.Parallel()
	_, g, jobs := newGraph(t)
	ctx := context.Background()
	_, err := g.CreateNodeType(ctx, "dynamic", dynamicNodeSchema)
	require.NoError(t, err)
	jobs.err = errors.New("queue unavailable")
	id := uuid.New()
	_, err = g.UpsertNode(ctx, "dynamic", id,
		json.RawMessage(`{"title":"Title","description":"Description","name":"Name"}`), graph.NodeWriteOptions{})
	require.ErrorContains(t, err, "queue unavailable")
	_, err = g.GetNode(ctx, "dynamic", id)
	require.ErrorIs(t, err, domain.ErrNotFound)
}
