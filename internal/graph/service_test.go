package graph_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/pgvector/pgvector-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/anas-domesticus/personal-agent/internal/domain"
	"github.com/anas-domesticus/personal-agent/internal/graph"
	"github.com/anas-domesticus/personal-agent/internal/store/db"
)

func TestUpsertNodeUsesTypedRegistration(t *testing.T) {
	t.Parallel()
	_, g, jobs := newGraph(t)
	_, err := graph.RegisterNode(context.Background(), g, graph.NodeSpec[person]{Kind: "person"})
	require.NoError(t, err)

	created, err := g.UpsertNode(context.Background(), "person", uuid.Nil, []byte(`{"title":"Ada","description":"Mathematician","name":"Ada"}`), graph.NodeWriteOptions{})
	require.NoError(t, err)
	assert.Equal(t, "Ada", created.Title)
	assert.Equal(t, int64(1), created.Revision)
	assert.Len(t, jobs.recordedEmbeddings(), 1)

	updated, err := g.UpsertNode(context.Background(), "person", created.ID, []byte(`{"title":"Ada Lovelace","description":"First programmer","name":"Ada"}`), graph.NodeWriteOptions{
		WriteOptions: graph.WriteOptions{ExpectedRevision: &created.Revision},
	})
	require.NoError(t, err)
	assert.Equal(t, "Ada Lovelace", updated.Title)
	assert.Equal(t, int64(2), updated.Revision)

	_, err = g.UpsertNode(context.Background(), "person", created.ID, []byte(`{"title":"Ada","description":"Mathematician","name":"Ada","unknown":true}`), graph.NodeWriteOptions{})
	require.ErrorContains(t, err, "unknown field")
	_, err = g.UpsertNode(context.Background(), "unknown", created.ID, []byte(`{}`), graph.NodeWriteOptions{})
	require.ErrorIs(t, err, domain.ErrNotFound)
}

func TestUpsertEdgeUsesTypedRegistration(t *testing.T) {
	t.Parallel()
	_, g, _ := newGraph(t)
	ctx := context.Background()
	people, err := graph.RegisterNode(ctx, g, graph.NodeSpec[person]{Kind: "person"})
	require.NoError(t, err)
	_, err = graph.RegisterEdge(ctx, g, graph.EdgeSpec[relationship]{
		Kind: "knows", Endpoints: []graph.Endpoint{{Source: "person", Target: "person"}},
		Validate: func(value relationship) error {
			if value.Since <= 0 {
				return errors.New("since must be positive")
			}
			return nil
		},
	})
	require.NoError(t, err)
	ada, err := people.Upsert(ctx, uuid.Nil, person{Name: "Ada"}, graph.NodeWriteOptions{})
	require.NoError(t, err)
	grace, err := people.Upsert(ctx, uuid.Nil, person{Name: "Grace"}, graph.NodeWriteOptions{})
	require.NoError(t, err)

	// This payload satisfies the reflected schema but violates EdgeSpec.Validate.
	_, err = g.UpsertEdge(ctx, "knows", ada.Ref, grace.Ref, []byte(`{"since":0}`), graph.EdgeWriteOptions{})
	require.ErrorContains(t, err, "since must be positive")
	edges, err := g.ListEdges(ctx, "knows", uuid.Nil, uuid.Nil, 10)
	require.NoError(t, err)
	assert.Empty(t, edges)

	created, err := g.UpsertEdge(ctx, "knows", ada.Ref, grace.Ref, []byte(`{"since":1840}`), graph.EdgeWriteOptions{
		Metadata: []byte(`{"source":"generic"}`),
	})
	require.NoError(t, err)
	assert.Equal(t, int64(1), created.CurrentVersion)
	assert.JSONEq(t, `{"since":1840}`, string(created.Data))
	_, err = g.UpsertEdge(ctx, "knows", ada.Ref, grace.Ref, []byte(`{"since":1841,"unknown":true}`), graph.EdgeWriteOptions{})
	require.ErrorContains(t, err, "unknown field")
}

func TestSearchNodesUsesCurrentEmbeddings(t *testing.T) {
	t.Parallel()
	st, g, _ := newGraph(t)
	repository, err := graph.RegisterNode(context.Background(), g, graph.NodeSpec[person]{Kind: "person"})
	require.NoError(t, err)
	alpha, err := repository.Upsert(context.Background(), uuid.Nil, person{details: details{Title: "Alpha", Description: "Culinary herbs"}, Name: "alpha"}, graph.NodeWriteOptions{})
	require.NoError(t, err)
	beta, err := repository.Upsert(context.Background(), uuid.Nil, person{details: details{Title: "Beta", Description: "Bicycle gears"}, Name: "beta"}, graph.NodeWriteOptions{})
	require.NoError(t, err)

	model := "test-model"
	missing, err := st.ListGraphNodesMissingEmbeddings(context.Background(), db.ListGraphNodesMissingEmbeddingsParams{Model: model, Lim: 10})
	require.NoError(t, err)
	assert.Len(t, missing, 2)
	matching := searchVector(0)
	other := searchVector(1)
	setNodeVectors(t, st, alpha.ID, alpha.Revision, model, other, matching)
	setNodeVectors(t, st, beta.ID, beta.Revision, model, other, other)
	missing, err = st.ListGraphNodesMissingEmbeddings(context.Background(), db.ListGraphNodesMissingEmbeddingsParams{Model: model, Lim: 10})
	require.NoError(t, err)
	assert.Empty(t, missing)

	matches, err := g.SearchNodes(context.Background(), "person", matching, model, 10)
	require.NoError(t, err)
	require.Len(t, matches, 2)
	assert.Equal(t, alpha.ID, matches[0].ID)
	assert.Equal(t, "Culinary herbs", matches[0].Description)
	assert.InDelta(t, 1, matches[0].Score, 0.0001)

	_, err = g.SearchNodes(context.Background(), "unknown", matching, model, 10)
	require.ErrorIs(t, err, domain.ErrNotFound)
}

func TestGraphNodeEmbeddingWriteGuardsAndInvalidation(t *testing.T) {
	t.Parallel()
	st, g, _ := newGraph(t)
	repository, err := graph.RegisterNode(context.Background(), g, graph.NodeSpec[person]{Kind: "person"})
	require.NoError(t, err)
	created, err := repository.Upsert(context.Background(), uuid.Nil, person{details: details{Title: "First", Description: "Initial text"}, Name: "first"}, graph.NodeWriteOptions{})
	require.NoError(t, err)
	model := "test-model"
	setNodeVectors(t, st, created.ID, created.Revision, model, searchVector(0), searchVector(1))

	missing, err := st.ListGraphNodesMissingEmbeddings(context.Background(), db.ListGraphNodesMissingEmbeddingsParams{Model: "other-model", Lim: 10})
	require.NoError(t, err)
	assert.Len(t, missing, 1)

	staleRevision := created.Revision - 1
	title, description := searchVector(0), searchVector(1)
	written, err := st.SetGraphNodeEmbeddings(context.Background(), db.SetGraphNodeEmbeddingsParams{
		ID: created.ID, Kind: "person", Revision: staleRevision, EmbeddingRevision: &staleRevision,
		TitleEmbedding: &title, DescriptionEmbedding: &description, EmbeddingModel: &model,
	})
	require.NoError(t, err)
	assert.Zero(t, written)

	updated, err := repository.Upsert(context.Background(), created.ID, person{details: details{Title: "Second", Description: "Replacement text"}, Name: "first"}, graph.NodeWriteOptions{})
	require.NoError(t, err)
	row, err := st.GetGraphNode(context.Background(), db.GetGraphNodeParams{ID: updated.ID, Kind: "person"})
	require.NoError(t, err)
	assert.Nil(t, row.TitleEmbedding)
	assert.Nil(t, row.DescriptionEmbedding)
	assert.Nil(t, row.EmbeddingModel)
	assert.Nil(t, row.EmbeddingRevision)

	matches, err := g.SearchNodes(context.Background(), "person", title, model, 10)
	require.NoError(t, err)
	assert.Empty(t, matches)
	missing, err = st.ListGraphNodesMissingEmbeddings(context.Background(), db.ListGraphNodesMissingEmbeddingsParams{Model: model, Lim: 10})
	require.NoError(t, err)
	require.Len(t, missing, 1)
	assert.Equal(t, updated.Revision, missing[0].Revision)
}

func searchVector(index int) pgvector.Vector {
	values := make([]float32, 1536)
	values[index] = 1
	return pgvector.NewVector(values)
}

func setNodeVectors(t *testing.T, st interface {
	SetGraphNodeEmbeddings(context.Context, db.SetGraphNodeEmbeddingsParams) (int64, error)
}, id uuid.UUID, revision int64, model string, title, description pgvector.Vector) {
	t.Helper()
	written, err := st.SetGraphNodeEmbeddings(context.Background(), db.SetGraphNodeEmbeddingsParams{
		ID: id, Kind: "person", Revision: revision, EmbeddingRevision: &revision,
		TitleEmbedding: &title, DescriptionEmbedding: &description, EmbeddingModel: &model,
	})
	require.NoError(t, err)
	require.Equal(t, int64(1), written)
}
