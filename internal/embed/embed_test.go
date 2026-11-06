package embed_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/embedding"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"

	"github.com/anas-domesticus/personal-agent/internal/embed"
	"github.com/anas-domesticus/personal-agent/internal/store/db"
	"github.com/anas-domesticus/personal-agent/internal/testdb"
)

// fakeEmbedder returns a fixed 1536-dimension vector per text.
type fakeEmbedder struct{}

func (fakeEmbedder) EmbedStrings(_ context.Context, texts []string, _ ...embedding.Option) ([][]float64, error) {
	out := make([][]float64, len(texts))
	for i := range texts {
		v := make([]float64, 1536)
		v[0] = 1
		out[i] = v
	}
	return out, nil
}

func TestSweepEmbedsMissingRows(t *testing.T) {
	st, _ := testdb.Open(t)
	ctx := context.Background()

	w, err := st.CreateWorkflow(ctx, db.CreateWorkflowParams{Name: "evening-lights", Description: "Turn on the hall lights at sunset"})
	if err != nil {
		t.Fatal(err)
	}
	m, err := st.CreateMemory(ctx, db.CreateMemoryParams{Content: "The boiler struggles below -2C", Tags: []string{}, Metadata: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}

	cfg := &river.Config{}
	embed.Register(cfg, st.Queries, fakeEmbedder{}, "test-model")
	client, err := river.NewClient[pgx.Tx](riverpgxv5.New(st.Pool), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Stop(context.Background()) })

	// The sweep runs on start, queues one job per row, and both get embedded.
	deadline := time.Now().Add(30 * time.Second)
	for {
		wf, err := st.GetWorkflowByName(ctx, w.Name)
		if err != nil {
			t.Fatal(err)
		}
		mem, err := st.GetMemory(ctx, m.ID)
		if err != nil {
			t.Fatal(err)
		}
		if wf.EmbeddingModel != nil && *wf.EmbeddingModel == "test-model" &&
			mem.EmbeddingModel != nil && *mem.EmbeddingModel == "test-model" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("rows not embedded in time: workflow=%v memory=%v", wf.EmbeddingModel, mem.EmbeddingModel)
		}
		time.Sleep(200 * time.Millisecond)
	}

	missing, err := st.ListMemoriesMissingEmbedding(ctx, db.ListMemoriesMissingEmbeddingParams{Model: "test-model", Lim: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(missing) != 0 {
		t.Fatalf("still missing: %+v", missing)
	}

	// An edit after a completed embed job must be embedded again, even
	// though the completed job is still in the queue table. (River records
	// completion asynchronously, so wait for it.)
	waitFor(t, func() bool {
		var n int
		err := st.Pool.QueryRow(ctx, `select count(*) from river_job where kind = 'embed' and args->>'id' = $1 and state = 'completed'`, w.ID.String()).Scan(&n)
		return err == nil && n == 1
	})
	if err := st.UpdateWorkflowDescription(ctx, db.UpdateWorkflowDescriptionParams{ID: w.ID, Description: "Dim the hall lights at bedtime"}); err != nil {
		t.Fatal(err)
	}
	res, err := client.Insert(ctx, embed.Args{Target: embed.TargetWorkflow, ID: w.ID}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.UniqueSkippedAsDuplicate {
		t.Fatal("re-embed job was deduplicated against the completed one")
	}
	waitFor(t, func() bool {
		wf, err := st.GetWorkflowByName(ctx, w.Name)
		return err == nil && wf.EmbeddingModel != nil
	})
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met in time")
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// changingStore changes a workflow's description the first time it is
// embedded, as a concurrent edit would.
type changingStore struct {
	embed.Store
	once func()
}

func (c *changingStore) SetWorkflowEmbedding(ctx context.Context, p db.SetWorkflowEmbeddingParams) (int64, error) {
	if c.once != nil {
		c.once()
		c.once = nil
	}
	return c.Store.SetWorkflowEmbedding(ctx, p)
}

func TestEditDuringEmbedIsEmbedded(t *testing.T) {
	st, _ := testdb.Open(t)
	ctx := context.Background()
	w, err := st.CreateWorkflow(ctx, db.CreateWorkflowParams{Name: "plants", Description: "Water the plants"})
	if err != nil {
		t.Fatal(err)
	}
	store := &changingStore{Store: st.Queries, once: func() {
		if err := st.UpdateWorkflowDescription(ctx, db.UpdateWorkflowDescriptionParams{ID: w.ID, Description: "Water the plants twice a week"}); err != nil {
			t.Error(err)
		}
	}}

	cfg := &river.Config{}
	embed.Register(cfg, store, fakeEmbedder{}, "test-model")
	client, err := river.NewClient[pgx.Tx](riverpgxv5.New(st.Pool), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Stop(context.Background()) })

	// The first attempt embeds the old text, loses the race, and snoozes;
	// the retry embeds the new text.
	waitFor(t, func() bool {
		var desc string
		var model *string
		err := st.Pool.QueryRow(ctx, `select description, embedding_model from workflows where id = $1`, w.ID).Scan(&desc, &model)
		return err == nil && model != nil && desc == "Water the plants twice a week"
	})
}
