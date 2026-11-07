package graph_test

import (
	"context"
	"sort"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/anas-domesticus/personal-agent/internal/domain"
	"github.com/anas-domesticus/personal-agent/internal/graph"
)

func TestRawNodeReads(t *testing.T) {
	t.Parallel()
	_, g, _ := newGraph(t)
	ctx := context.Background()
	notes, err := graph.RegisterVersionedNode(ctx, g, graph.NodeSpec[note]{Kind: "note"})
	require.NoError(t, err)
	people, err := graph.RegisterNode(ctx, g, graph.NodeSpec[person]{Kind: "person"})
	require.NoError(t, err)

	first, err := notes.Upsert(ctx, uuid.Nil, note{
		details: details{Title: "First note", Description: "Original current data"}, Text: "one",
	}, graph.NodeWriteOptions{})
	require.NoError(t, err)
	second, err := notes.Upsert(ctx, uuid.Nil, note{
		details: details{Title: "Second note", Description: "Another versioned node"}, Text: "two",
	}, graph.NodeWriteOptions{})
	require.NoError(t, err)
	_, err = notes.Upsert(ctx, first.ID, note{
		details: details{Title: "Changed note", Description: "A newer historical snapshot"}, Text: "changed",
	}, graph.NodeWriteOptions{})
	require.NoError(t, err)
	_, err = notes.Rollback(ctx, first.ID, 1, graph.WriteOptions{})
	require.NoError(t, err)
	createdPerson, err := people.Upsert(ctx, uuid.Nil, person{
		details: details{Title: "Ada", Description: "Original person"}, Name: "Ada",
	}, graph.NodeWriteOptions{})
	require.NoError(t, err)
	_, err = people.Upsert(ctx, createdPerson.ID, person{
		details: details{Title: "Grace", Description: "Current person"}, Name: "Grace",
	}, graph.NodeWriteOptions{})
	require.NoError(t, err)

	current, err := g.GetNode(ctx, "note", first.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(3), current.Revision)
	assert.Equal(t, "First note", current.Title)
	assert.Equal(t, "Original current data", current.Description)
	assert.JSONEq(t, `{"title":"First note","description":"Original current data","text":"one"}`, string(current.Data))

	currentPerson, err := g.GetNode(ctx, "person", createdPerson.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(2), currentPerson.Revision)
	assert.Equal(t, "Grace", currentPerson.Title)
	assert.JSONEq(t, `{"title":"Grace","description":"Current person","name":"Grace"}`, string(currentPerson.Data))

	secondCurrent, err := g.GetNode(ctx, "note", second.ID)
	require.NoError(t, err)
	wantAll := []domain.KnowledgeNode{current, secondCurrent, currentPerson}
	sort.Slice(wantAll, func(i, j int) bool {
		if wantAll[i].CreatedAt.Equal(wantAll[j].CreatedAt) {
			return wantAll[i].ID.String() < wantAll[j].ID.String()
		}
		return wantAll[i].CreatedAt.Before(wantAll[j].CreatedAt)
	})
	all, err := g.ListNodes(ctx, "", 0)
	require.NoError(t, err)
	assert.Equal(t, wantAll, all)
	limited, err := g.ListNodes(ctx, "", 2)
	require.NoError(t, err)
	assert.Equal(t, wantAll[:2], limited)

	wantNotes := make([]domain.KnowledgeNode, 0, 2)
	for _, node := range wantAll {
		if node.Kind == "note" {
			wantNotes = append(wantNotes, node)
		}
	}
	filtered, err := g.ListNodes(ctx, "note", 10)
	require.NoError(t, err)
	assert.Equal(t, wantNotes, filtered)

	_, err = g.GetNode(ctx, "unknown", first.ID)
	require.ErrorIs(t, err, domain.ErrNotFound)
	_, err = g.GetNode(ctx, "note", uuid.New())
	require.ErrorIs(t, err, domain.ErrNotFound)
	_, err = g.ListNodes(ctx, "unknown", 10)
	require.ErrorIs(t, err, domain.ErrNotFound)
}

func TestListNodesDefaultsAndCapsLimit(t *testing.T) {
	t.Parallel()
	st, g, _ := newGraph(t)
	ctx := context.Background()
	_, err := graph.RegisterNode(ctx, g, graph.NodeSpec[person]{Kind: "person"})
	require.NoError(t, err)
	_, err = st.Pool.Exec(ctx, `
		insert into graph_nodes (id, kind, versioned, data)
		select gen_random_uuid(), 'person', false,
		       jsonb_build_object('title', 'Person ' || n, 'description', 'Read test', 'name', 'Person ' || n)
		from generate_series(1, 105) as n`)
	require.NoError(t, err)

	defaulted, err := g.ListNodes(ctx, "person", 0)
	require.NoError(t, err)
	require.Len(t, defaulted, 10)
	capped, err := g.ListNodes(ctx, "person", 1000)
	require.NoError(t, err)
	require.Len(t, capped, 100)
	assert.Equal(t, defaulted, capped[:10])

	repeated, err := g.ListNodes(ctx, "person", 100)
	require.NoError(t, err)
	assert.Equal(t, capped, repeated)
	limited, err := g.ListNodes(ctx, "person", 2)
	require.NoError(t, err)
	assert.Equal(t, capped[:2], limited)
}
