package graph_test

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/anas-domesticus/personal-agent/internal/domain"
	"github.com/anas-domesticus/personal-agent/internal/embed"
	"github.com/anas-domesticus/personal-agent/internal/graph"
	"github.com/anas-domesticus/personal-agent/internal/store"
	"github.com/anas-domesticus/personal-agent/internal/testdb"
)

type details struct {
	Title       string `json:"title"`
	Description string `json:"description"`
}

func (d details) NodeText() (string, string) { return d.Title, d.Description }

type person struct {
	details
	Name string `json:"name"`
}

type note struct {
	details
	Text string `json:"text"`
}

type relationship struct {
	Since int `json:"since"`
}

// jobRecorder is a stateful recorder rather than a script of mock calls.
type jobRecorder struct {
	mu         sync.Mutex
	jobs       []graph.EnrichmentArgs
	embeddings []embed.Args
	err        error
}

var _ graph.JobInserter = (*jobRecorder)(nil)

func (r *jobRecorder) InsertTx(_ context.Context, _ pgx.Tx, args river.JobArgs, _ *river.InsertOpts) (*rivertype.JobInsertResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err != nil {
		return nil, r.err
	}
	switch typed := args.(type) {
	case graph.EnrichmentArgs:
		r.jobs = append(r.jobs, typed)
	case embed.Args:
		r.embeddings = append(r.embeddings, typed)
	default:
		return nil, errors.New("unexpected job type")
	}
	return &rivertype.JobInsertResult{}, nil
}

func (r *jobRecorder) recorded() []graph.EnrichmentArgs {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]graph.EnrichmentArgs(nil), r.jobs...)
}

func (r *jobRecorder) recordedEmbeddings() []embed.Args {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]embed.Args(nil), r.embeddings...)
}

func newGraph(t *testing.T) (*store.Store, *graph.Graph, *jobRecorder) {
	t.Helper()
	st, _ := testdb.Open(t)
	jobs := &jobRecorder{}
	return st, graph.New(st, jobs), jobs
}

func TestOrdinaryNodeUpsert(t *testing.T) {
	t.Parallel()
	_, g, jobs := newGraph(t)
	repository, err := graph.RegisterNode(context.Background(), g, graph.NodeSpec[person]{
		Kind: "person",
		Validate: func(value person) error {
			if value.Name == "" {
				return errors.New("name is required")
			}
			return nil
		},
	})
	require.NoError(t, err)

	created, err := repository.Upsert(context.Background(), uuid.Nil, person{Name: "Ada"}, graph.NodeWriteOptions{})
	require.NoError(t, err)
	assert.NotEqual(t, uuid.Nil, created.ID)
	assert.Equal(t, int64(1), created.Revision)
	assert.Equal(t, []graph.EnrichmentArgs{{Entity: graph.EntityNode, Type: "person", ID: created.ID, Revision: 1}}, jobs.recorded())
	assert.Equal(t, []embed.Args{{Target: embed.TargetGraphNode, NodeKind: "person", ID: created.ID, Revision: 1}}, jobs.recordedEmbeddings())

	unchanged, err := repository.Upsert(context.Background(), created.ID, person{Name: "Ada"}, graph.NodeWriteOptions{
		WriteOptions: graph.WriteOptions{ExpectedRevision: &created.Revision},
	})
	require.NoError(t, err)
	assert.Equal(t, created, unchanged)
	assert.Len(t, jobs.recorded(), 1)
	assert.Len(t, jobs.recordedEmbeddings(), 1)

	updated, err := repository.Upsert(context.Background(), created.ID, person{Name: "Grace"}, graph.NodeWriteOptions{
		WriteOptions: graph.WriteOptions{ExpectedRevision: &created.Revision},
	})
	require.NoError(t, err)
	assert.Equal(t, person{Name: "Grace"}, updated.Data)
	assert.Equal(t, int64(2), updated.Revision)
	assert.Len(t, jobs.recorded(), 2)
	require.Len(t, jobs.recordedEmbeddings(), 2)
	assert.Equal(t, int64(2), jobs.recordedEmbeddings()[1].Revision)

	stale := int64(1)
	_, err = repository.Upsert(context.Background(), created.ID, person{Name: "Stale"}, graph.NodeWriteOptions{
		WriteOptions: graph.WriteOptions{ExpectedRevision: &stale},
	})
	require.ErrorIs(t, err, domain.ErrConflict)
	assert.Len(t, jobs.recorded(), 2)
	assert.Len(t, jobs.recordedEmbeddings(), 2)

	_, err = repository.Upsert(context.Background(), uuid.Nil, person{}, graph.NodeWriteOptions{})
	require.ErrorContains(t, err, "name is required")

	got, err := repository.Get(context.Background(), created.ID)
	require.NoError(t, err)
	assert.Equal(t, updated, got)
	require.NoError(t, repository.Delete(context.Background(), created.ID))
	_, err = repository.Get(context.Background(), created.ID)
	require.ErrorIs(t, err, domain.ErrNotFound)
}

func TestVersionedNodeHistoryAndRollback(t *testing.T) {
	t.Parallel()
	st, g, jobs := newGraph(t)
	repository, err := graph.RegisterVersionedNode(context.Background(), g, graph.NodeSpec[note]{Kind: "note"})
	require.NoError(t, err)

	created, err := repository.Upsert(context.Background(), uuid.Nil, note{Text: "one"}, graph.NodeWriteOptions{})
	require.NoError(t, err)
	sameData, err := repository.Upsert(context.Background(), created.ID, note{Text: "one"}, graph.NodeWriteOptions{})
	require.NoError(t, err)
	assert.Equal(t, created, sameData)
	assert.Len(t, jobs.recorded(), 1)
	assert.Len(t, jobs.recordedEmbeddings(), 1)
	_, err = repository.Upsert(context.Background(), created.ID, note{Text: "two"}, graph.NodeWriteOptions{
		Metadata: []byte(`{"reason":"correction"}`),
	})
	require.NoError(t, err)
	third, err := repository.Upsert(context.Background(), created.ID, note{Text: "three"}, graph.NodeWriteOptions{})
	require.NoError(t, err)
	assert.Equal(t, int64(3), third.Revision)

	current, err := repository.Current(context.Background(), created.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(3), current.CurrentVersion)
	assert.Equal(t, note{Text: "three"}, current.Data)

	page, err := repository.Versions(context.Background(), created.ID, graph.HistoryOptions{Limit: 2})
	require.NoError(t, err)
	require.Len(t, page.Versions, 2)
	assert.Equal(t, int64(3), page.Versions[0].Version)
	assert.Equal(t, int64(2), page.Versions[1].Version)
	assert.JSONEq(t, `{"reason":"correction"}`, string(page.Versions[1].Metadata))
	assert.Equal(t, int64(2), page.NextBefore)

	older, err := repository.Versions(context.Background(), created.ID, graph.HistoryOptions{Before: page.NextBefore, Limit: 2})
	require.NoError(t, err)
	require.Len(t, older.Versions, 1)
	assert.Equal(t, int64(1), older.Versions[0].Version)
	assert.Zero(t, older.NextBefore)

	version, err := repository.Version(context.Background(), created.ID, 2)
	require.NoError(t, err)
	assert.Equal(t, note{Text: "two"}, version.Data)

	rolledBack, err := repository.Rollback(context.Background(), created.ID, 1, graph.WriteOptions{ExpectedRevision: &third.Revision})
	require.NoError(t, err)
	assert.Equal(t, int64(1), rolledBack.CurrentVersion)
	assert.Equal(t, int64(4), rolledBack.Revision)
	assert.Equal(t, note{Text: "one"}, rolledBack.Data)
	assert.Len(t, jobs.recorded(), 4)
	assert.Len(t, jobs.recordedEmbeddings(), 4)

	history, err := repository.Versions(context.Background(), created.ID, graph.HistoryOptions{})
	require.NoError(t, err)
	assert.Len(t, history.Versions, 3)

	unchanged, err := repository.Rollback(context.Background(), created.ID, 1, graph.WriteOptions{ExpectedRevision: &rolledBack.Revision})
	require.NoError(t, err)
	assert.Equal(t, rolledBack, unchanged)
	assert.Len(t, jobs.recorded(), 4)
	assert.Len(t, jobs.recordedEmbeddings(), 4)

	appended, err := repository.Upsert(context.Background(), created.ID, note{Text: "four"}, graph.NodeWriteOptions{
		WriteOptions: graph.WriteOptions{ExpectedRevision: &rolledBack.Revision},
	})
	require.NoError(t, err)
	assert.Equal(t, int64(5), appended.Revision)
	afterAppend, err := repository.Current(context.Background(), created.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(4), afterAppend.CurrentVersion)
	assert.Len(t, jobs.recorded(), 5)
	assert.Len(t, jobs.recordedEmbeddings(), 5)

	_, err = st.Pool.Exec(context.Background(), `update graph_node_versions set data = '{"text":"changed"}' where node_id = $1 and version = 1`, created.ID)
	require.ErrorContains(t, err, "immutable")
	_, err = st.Pool.Exec(context.Background(), `
		insert into graph_node_versions (node_id, version, data)
		values ($1, 99, '{"text":"legacy","removed":true}')`, created.ID)
	require.NoError(t, err)
	legacy, err := repository.Version(context.Background(), created.ID, 99)
	require.NoError(t, err)
	assert.Equal(t, note{Text: "legacy"}, legacy.Data)

	people, err := graph.RegisterNode(context.Background(), g, graph.NodeSpec[person]{Kind: "person"})
	require.NoError(t, err)
	other, err := people.Upsert(context.Background(), uuid.Nil, person{Name: "Ada"}, graph.NodeWriteOptions{})
	require.NoError(t, err)
	_, err = repository.Version(context.Background(), other.ID, 1)
	require.ErrorIs(t, err, domain.ErrNotFound)
	_, err = repository.Version(context.Background(), created.ID, 1000)
	require.ErrorIs(t, err, domain.ErrNotFound)
	require.NoError(t, repository.Delete(context.Background(), created.ID))
	_, err = repository.Current(context.Background(), created.ID)
	require.ErrorIs(t, err, domain.ErrNotFound)
}

func TestEdgesAndDatabaseConstraints(t *testing.T) {
	t.Parallel()
	st, g, jobs := newGraph(t)
	people, err := graph.RegisterNode(context.Background(), g, graph.NodeSpec[person]{Kind: "person"})
	require.NoError(t, err)
	tags, err := graph.RegisterNode(context.Background(), g, graph.NodeSpec[note]{Kind: "tag"})
	require.NoError(t, err)
	knows, err := graph.RegisterEdge(context.Background(), g, graph.EdgeSpec[relationship]{
		Kind: "knows", Endpoints: []graph.Endpoint{{Source: "person", Target: "person"}},
	})
	require.NoError(t, err)
	likes, err := graph.RegisterEdge(context.Background(), g, graph.EdgeSpec[relationship]{
		Kind: "likes", Endpoints: []graph.Endpoint{{Source: "person", Target: "person"}},
	})
	require.NoError(t, err)

	ada, err := people.Upsert(context.Background(), uuid.Nil, person{Name: "Ada"}, graph.NodeWriteOptions{})
	require.NoError(t, err)
	grace, err := people.Upsert(context.Background(), uuid.Nil, person{Name: "Grace"}, graph.NodeWriteOptions{})
	require.NoError(t, err)
	tag, err := tags.Upsert(context.Background(), uuid.Nil, note{Text: "pioneer"}, graph.NodeWriteOptions{})
	require.NoError(t, err)

	edge, err := knows.Upsert(context.Background(), ada.Ref, grace.Ref, relationship{Since: 1840}, graph.WriteOptions{})
	require.NoError(t, err)
	assert.Equal(t, int64(1), edge.Revision)
	otherKind, err := likes.Upsert(context.Background(), ada.Ref, grace.Ref, relationship{Since: 1842}, graph.WriteOptions{})
	require.NoError(t, err)
	assert.NotEqual(t, edge.ID, otherKind.ID)

	_, err = knows.Upsert(context.Background(), tag.Ref, grace.Ref, relationship{}, graph.WriteOptions{})
	require.ErrorIs(t, err, domain.ErrConflict)
	_, err = knows.Upsert(context.Background(), ada.Ref, ada.Ref, relationship{}, graph.WriteOptions{})
	require.ErrorIs(t, err, domain.ErrConflict)

	_, err = st.Pool.Exec(context.Background(), `
		insert into graph_edges (kind, source_id, source_kind, target_id, target_kind, data)
		values ('knows', $1, 'person', $2, 'person', '{}')`, tag.ID, grace.ID)
	require.Error(t, err)
	var pgErr interface{ SQLState() string }
	if errors.As(err, &pgErr) {
		assert.Equal(t, "23503", pgErr.SQLState())
	}

	outgoing, err := knows.From(context.Background(), ada.ID)
	require.NoError(t, err)
	require.Len(t, outgoing, 1)
	assert.Equal(t, edge, outgoing[0])
	incoming, err := knows.To(context.Background(), grace.ID)
	require.NoError(t, err)
	assert.Equal(t, outgoing, incoming)

	require.ErrorIs(t, people.Delete(context.Background(), ada.ID), domain.ErrConflict)
	require.NoError(t, knows.Delete(context.Background(), edge.ID))
	require.NoError(t, likes.Delete(context.Background(), otherKind.ID))
	require.NoError(t, people.Delete(context.Background(), ada.ID))
	assert.Len(t, jobs.recorded(), 5)
	assert.Len(t, jobs.recordedEmbeddings(), 3)
}

func TestEnrichmentHandlersAndStaleJobs(t *testing.T) {
	t.Parallel()
	_, g, jobs := newGraph(t)
	var enriched []string
	repository, err := graph.RegisterNode(context.Background(), g, graph.NodeSpec[person]{
		Kind: "person",
		Enrichers: []graph.NodeEnricher[person]{func(_ context.Context, node graph.Node[person]) error {
			enriched = append(enriched, node.Data.Name)
			return nil
		}},
	})
	require.NoError(t, err)
	created, err := repository.Upsert(context.Background(), uuid.Nil, person{Name: "Ada"}, graph.NodeWriteOptions{})
	require.NoError(t, err)
	_, err = repository.Upsert(context.Background(), created.ID, person{Name: "Grace"}, graph.NodeWriteOptions{})
	require.NoError(t, err)

	worker := &graph.Worker{Graph: g}
	queued := jobs.recorded()
	require.Len(t, queued, 2)
	require.NoError(t, worker.Work(context.Background(), &river.Job[graph.EnrichmentArgs]{Args: queued[0]}))
	assert.Empty(t, enriched, "the stale revision must not run")
	require.NoError(t, worker.Work(context.Background(), &river.Job[graph.EnrichmentArgs]{Args: queued[1]}))
	assert.Equal(t, []string{"Grace"}, enriched)

	config := &river.Config{}
	graph.RegisterRiver(config, g)
	assert.Contains(t, config.Queues, graph.Queue)
	_, err = graph.RegisterNode(context.Background(), g, graph.NodeSpec[note]{Kind: "late"})
	require.ErrorContains(t, err, "frozen")
}

func TestFailedEnrichmentRollsBackWrite(t *testing.T) {
	t.Parallel()
	_, g, jobs := newGraph(t)
	jobs.err = errors.New("queue unavailable")
	repository, err := graph.RegisterNode(context.Background(), g, graph.NodeSpec[person]{Kind: "person"})
	require.NoError(t, err)
	id := uuid.New()
	_, err = repository.Upsert(context.Background(), id, person{Name: "Ada"}, graph.NodeWriteOptions{})
	require.ErrorContains(t, err, "queue unavailable")
	_, err = repository.Get(context.Background(), id)
	require.ErrorIs(t, err, domain.ErrNotFound)
}

func TestConcurrentVersionWrites(t *testing.T) {
	t.Parallel()
	_, g, _ := newGraph(t)
	repository, err := graph.RegisterVersionedNode(context.Background(), g, graph.NodeSpec[note]{Kind: "note"})
	require.NoError(t, err)
	created, err := repository.Upsert(context.Background(), uuid.Nil, note{Text: "one"}, graph.NodeWriteOptions{})
	require.NoError(t, err)

	var wait sync.WaitGroup
	errs := make(chan error, 2)
	for _, text := range []string{"two", "three"} {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, err := repository.Upsert(context.Background(), created.ID, note{Text: text}, graph.NodeWriteOptions{})
			errs <- err
		}()
	}
	wait.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	current, err := repository.Current(context.Background(), created.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(3), current.CurrentVersion)
	assert.Equal(t, int64(3), current.Revision)
}

func TestEdgeEnrichment(t *testing.T) {
	t.Parallel()
	_, g, jobs := newGraph(t)
	people, err := graph.RegisterNode(context.Background(), g, graph.NodeSpec[person]{Kind: "person"})
	require.NoError(t, err)
	var enriched []int
	edges, err := graph.RegisterEdge(context.Background(), g, graph.EdgeSpec[relationship]{
		Kind: "knows", Endpoints: []graph.Endpoint{{Source: "person", Target: "person"}},
		Enrichers: []graph.EdgeEnricher[relationship]{func(_ context.Context, edge graph.Edge[relationship]) error {
			enriched = append(enriched, edge.Data.Since)
			return nil
		}},
	})
	require.NoError(t, err)
	ada, err := people.Upsert(context.Background(), uuid.Nil, person{Name: "Ada"}, graph.NodeWriteOptions{})
	require.NoError(t, err)
	grace, err := people.Upsert(context.Background(), uuid.Nil, person{Name: "Grace"}, graph.NodeWriteOptions{})
	require.NoError(t, err)
	_, err = edges.Upsert(context.Background(), ada.Ref, grace.Ref, relationship{Since: 1840}, graph.WriteOptions{})
	require.NoError(t, err)

	queued := jobs.recorded()
	worker := &graph.Worker{Graph: g}
	require.NoError(t, worker.Work(context.Background(), &river.Job[graph.EnrichmentArgs]{Args: queued[2]}))
	assert.Equal(t, []int{1840}, enriched)
}

func TestGraphMigrationDown(t *testing.T) {
	t.Parallel()
	_, url := testdb.Open(t)
	database, err := sql.Open("pgx", url)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, database.Close()) })
	provider, err := goose.NewProvider(goose.DialectPostgres, database, os.DirFS("../store/migrations"))
	require.NoError(t, err)
	for range 2 {
		_, err = provider.Down(context.Background())
		require.NoError(t, err)
	}
	var table *string
	require.NoError(t, database.QueryRowContext(context.Background(), `select to_regclass('graph_nodes')`).Scan(&table))
	assert.Nil(t, table)
}

func TestPersistedTypeDefinitionsAreVerified(t *testing.T) {
	t.Parallel()
	st, g, _ := newGraph(t)
	_, err := graph.RegisterNode(context.Background(), g, graph.NodeSpec[person]{Kind: "person"})
	require.NoError(t, err)

	definitions := g.NodeTypes()
	require.Len(t, definitions, 1)
	assert.Equal(t, graph.NodeType{Kind: "person", Versioned: false, DataSchema: definitions[0].DataSchema}, definitions[0])
	assert.Contains(t, string(definitions[0].DataSchema), `"type":"object"`)

	second := graph.New(st, &jobRecorder{})
	_, err = graph.RegisterNode(context.Background(), second, graph.NodeSpec[person]{Kind: "person"})
	require.NoError(t, err)
	_, err = graph.RegisterVersionedNode(context.Background(), second, graph.NodeSpec[person]{Kind: "versioned_person"})
	require.NoError(t, err)
	conflicting := graph.New(st, &jobRecorder{})
	_, err = graph.RegisterVersionedNode(context.Background(), conflicting, graph.NodeSpec[person]{Kind: "person"})
	require.ErrorContains(t, err, "differs from its persisted definition")
}
