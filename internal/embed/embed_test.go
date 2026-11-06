package embed_test

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/embedding"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/anas-domesticus/personal-agent/internal/embed"
	"github.com/anas-domesticus/personal-agent/internal/mocks"
	"github.com/anas-domesticus/personal-agent/internal/store/db"
	"github.com/anas-domesticus/personal-agent/internal/testdb"
)

// vectors returns a fixed vector of the stored size per text.
func vectors(_ context.Context, texts []string, _ ...embedding.Option) ([][]float64, error) {
	out := make([][]float64, len(texts))
	for i := range texts {
		v := make([]float64, embed.Dimensions)
		v[0] = 1
		out[i] = v
	}
	return out, nil
}

// embedder is an Embedder that may be called any number of times, as River
// decides, and returns a fixed vector per text.
func embedder(t *testing.T) *mocks.MockEmbedder {
	t.Helper()
	m := mocks.NewMockEmbedder(t)
	m.EXPECT().EmbedStrings(mock.Anything, mock.Anything).RunAndReturn(vectors)
	return m
}

// startRiver starts a River client with the embedding workers registered.
func startRiver(t *testing.T, pool *pgxpool.Pool, store embed.Store, e embedding.Embedder) *river.Client[pgx.Tx] {
	t.Helper()
	cfg := &river.Config{}
	embed.Register(cfg, store, e, "test-model")
	client, err := river.NewClient[pgx.Tx](riverpgxv5.New(pool), cfg)
	require.NoError(t, err)
	require.NoError(t, client.Start(context.Background()))
	t.Cleanup(func() { _ = client.Stop(context.Background()) })
	return client
}

// TestSweepEmbedsMissingRows is an ordered scenario over one database and
// River client: the re-embed step depends on the first embed completing.
func TestSweepEmbedsMissingRows(t *testing.T) {
	st, _ := testdb.Open(t)
	ctx := context.Background()

	w, err := st.CreateWorkflow(ctx, db.CreateWorkflowParams{Name: "evening-lights", Description: "Turn on the hall lights at sunset"})
	require.NoError(t, err)
	m, err := st.CreateMemory(ctx, db.CreateMemoryParams{Content: "The boiler struggles below -2C", Tags: []string{}, Metadata: json.RawMessage(`{}`)})
	require.NoError(t, err)

	client := startRiver(t, st.Pool, st.Queries, embedder(t))

	// The sweep runs on start, queues one job per row, and both get embedded.
	// The condition runs on another goroutine, so a read error stops the
	// wait and is checked afterwards.
	var readErr error
	waitFor(t, func() bool {
		var (
			wf  db.GetWorkflowByNameRow
			mem db.GetMemoryRow
		)
		if wf, readErr = st.GetWorkflowByName(ctx, w.Name); readErr != nil {
			return true
		}
		if mem, readErr = st.GetMemory(ctx, m.ID); readErr != nil {
			return true
		}
		return wf.EmbeddingModel != nil && *wf.EmbeddingModel == "test-model" &&
			mem.EmbeddingModel != nil && *mem.EmbeddingModel == "test-model"
	}, "rows not embedded in time")
	require.NoError(t, readErr)

	missing, err := st.ListMemoriesMissingEmbedding(ctx, db.ListMemoriesMissingEmbeddingParams{Model: "test-model", Lim: 10})
	require.NoError(t, err)
	assert.Empty(t, missing, "still missing")

	// An edit after a completed embed job must be embedded again, even
	// though the completed job is still in the queue table. (River records
	// completion asynchronously, so wait for it.)
	waitFor(t, func() bool {
		var n int
		err := st.Pool.QueryRow(ctx, `select count(*) from river_job where kind = 'embed' and args->>'id' = $1 and state = 'completed'`, w.ID.String()).Scan(&n)
		return err == nil && n == 1
	}, "embed job not completed")
	require.NoError(t, st.UpdateWorkflowDescription(ctx, db.UpdateWorkflowDescriptionParams{ID: w.ID, Description: "Dim the hall lights at bedtime"}))
	res, err := client.Insert(ctx, embed.Args{Target: embed.TargetWorkflow, ID: w.ID}, nil)
	require.NoError(t, err)
	assert.False(t, res.UniqueSkippedAsDuplicate, "re-embed job was deduplicated against the completed one")
	waitFor(t, func() bool {
		wf, err := st.GetWorkflowByName(ctx, w.Name)
		return err == nil && wf.EmbeddingModel != nil
	}, "workflow not re-embedded")
}

func waitFor(t *testing.T, cond func() bool, msg string) {
	t.Helper()
	require.Eventually(t, cond, 30*time.Second, 100*time.Millisecond, msg)
}

// changingStore changes a workflow's description the first time it is
// embedded, as a concurrent edit would. It is a fake rather than a mock
// because it wraps the real Postgres store and injects a race into it.
type changingStore struct {
	embed.Store
	once sync.Once
	edit func()
}

var _ embed.Store = (*changingStore)(nil)

func (c *changingStore) SetWorkflowEmbedding(ctx context.Context, p db.SetWorkflowEmbeddingParams) (int64, error) {
	c.once.Do(c.edit)
	return c.Store.SetWorkflowEmbedding(ctx, p)
}

func TestEditDuringEmbedIsEmbedded(t *testing.T) {
	st, _ := testdb.Open(t)
	ctx := context.Background()
	w, err := st.CreateWorkflow(ctx, db.CreateWorkflowParams{Name: "plants", Description: "Water the plants"})
	require.NoError(t, err)
	store := &changingStore{Store: st.Queries, edit: func() {
		// Runs on a River worker goroutine, so it reports rather than stops.
		assert.NoError(t, st.UpdateWorkflowDescription(ctx, db.UpdateWorkflowDescriptionParams{ID: w.ID, Description: "Water the plants twice a week"}))
	}}

	startRiver(t, st.Pool, store, embedder(t))

	// The first attempt embeds the old text, loses the race, and snoozes;
	// the retry embeds the new text.
	waitFor(t, func() bool {
		var desc string
		var model *string
		err := st.Pool.QueryRow(ctx, `select description, embedding_model from workflows where id = $1`, w.ID).Scan(&desc, &model)
		return err == nil && model != nil && desc == "Water the plants twice a week"
	}, "edit during embed was not embedded")
}
