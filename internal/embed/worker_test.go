package embed_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/anas-domesticus/personal-agent/internal/embed"
	"github.com/anas-domesticus/personal-agent/internal/mocks"
	"github.com/anas-domesticus/personal-agent/internal/store/db"
)

// These unit tests cover the workers' decisions with mocked collaborators;
// the database behaviour they rely on is covered in embed_test.go.

func vector(n int) [][]float64 {
	v := make([]float64, n)
	v[0] = 1
	return [][]float64{v}
}

func TestText(t *testing.T) {
	t.Parallel()

	errAPI := errors.New("rate limited")
	tests := []struct {
		name    string
		in      string
		setup   func(m *mocks.MockEmbedder)
		wantErr error  // checked with errors.Is
		wantMsg string // substring of the error
	}{
		{
			name: "short text is embedded as is",
			in:   "hello",
			setup: func(m *mocks.MockEmbedder) {
				m.EXPECT().EmbedStrings(mock.Anything, []string{"hello"}).Return(vector(embed.Dimensions), nil).Once()
			},
		},
		{
			name: "long text is embedded from its start",
			in:   strings.Repeat("é", 30000),
			setup: func(m *mocks.MockEmbedder) {
				m.EXPECT().EmbedStrings(mock.Anything, mock.MatchedBy(func(texts []string) bool {
					return len(texts) == 1 && utf8.RuneCountInString(texts[0]) == 24000 && utf8.ValidString(texts[0])
				})).Return(vector(embed.Dimensions), nil).Once()
			},
		},
		{
			name: "embedder error",
			in:   "hello",
			setup: func(m *mocks.MockEmbedder) {
				m.EXPECT().EmbedStrings(mock.Anything, mock.Anything).Return(nil, errAPI).Once()
			},
			wantErr: errAPI,
			wantMsg: "embed:",
		},
		{
			name: "wrong number of vectors",
			in:   "hello",
			setup: func(m *mocks.MockEmbedder) {
				m.EXPECT().EmbedStrings(mock.Anything, mock.Anything).Return([][]float64{}, nil).Once()
			},
			wantMsg: "got 0 vectors, want 1",
		},
		{
			name: "wrong dimensions",
			in:   "hello",
			setup: func(m *mocks.MockEmbedder) {
				m.EXPECT().EmbedStrings(mock.Anything, mock.Anything).Return(vector(768), nil).Once()
			},
			wantErr: embed.ErrDimensions,
			wantMsg: "model returned 768",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			m := mocks.NewMockEmbedder(t)
			tt.setup(m)

			got, err := embed.Text(context.Background(), m, tt.in)
			if tt.wantErr != nil || tt.wantMsg != "" {
				if tt.wantErr != nil {
					require.ErrorIs(t, err, tt.wantErr)
				}
				require.ErrorContains(t, err, tt.wantMsg)
				return
			}
			require.NoError(t, err)
			assert.Len(t, got.Slice(), embed.Dimensions)
		})
	}
}

func TestWorker(t *testing.T) {
	t.Parallel()

	id := uuid.New()
	model := "test-model"
	errDB := errors.New("connection reset")
	workflow := db.GetWorkflowTextRow{ID: id, Name: "plants", Description: "Water the plants"}

	tests := []struct {
		name   string
		target embed.Target
		setup  func(s *embed.MockStore, e *mocks.MockEmbedder)
		// wantErrs must all be in the chain of the error Work returns
		// (errors.Is); River's cancel and snooze errors match any error of
		// their type. Nil means success.
		wantErrs []error
	}{
		{
			name:   "workflow is embedded from its name and description",
			target: embed.TargetWorkflow,
			setup: func(s *embed.MockStore, e *mocks.MockEmbedder) {
				s.EXPECT().GetWorkflowText(mock.Anything, id).Return(workflow, nil).Once()
				e.EXPECT().EmbedStrings(mock.Anything, []string{embed.WorkflowText("plants", "Water the plants")}).
					Return(vector(embed.Dimensions), nil).Once()
				s.EXPECT().SetWorkflowEmbedding(mock.Anything, mock.MatchedBy(func(p db.SetWorkflowEmbeddingParams) bool {
					return p.ID == id && p.Name == "plants" && p.Description == "Water the plants" &&
						p.Embedding != nil && len(p.Embedding.Slice()) == embed.Dimensions &&
						p.EmbeddingModel != nil && *p.EmbeddingModel == model
				})).Return(1, nil).Once()
			},
		},
		{
			name:   "memory is embedded from its content",
			target: embed.TargetMemory,
			setup: func(s *embed.MockStore, e *mocks.MockEmbedder) {
				s.EXPECT().GetMemory(mock.Anything, id).Return(db.GetMemoryRow{ID: id, Content: "likes tea"}, nil).Once()
				e.EXPECT().EmbedStrings(mock.Anything, []string{"likes tea"}).Return(vector(embed.Dimensions), nil).Once()
				s.EXPECT().SetMemoryEmbedding(mock.Anything, mock.MatchedBy(func(p db.SetMemoryEmbeddingParams) bool {
					return p.ID == id && p.Content == "likes tea" && p.EmbeddingModel != nil && *p.EmbeddingModel == model
				})).Return(1, nil).Once()
			},
		},
		{
			name:   "row deleted since the job was queued",
			target: embed.TargetWorkflow,
			setup: func(s *embed.MockStore, _ *mocks.MockEmbedder) {
				s.EXPECT().GetWorkflowText(mock.Anything, id).Return(db.GetWorkflowTextRow{}, pgx.ErrNoRows).Once()
			},
		},
		{
			name:   "text changed while embedding snoozes",
			target: embed.TargetWorkflow,
			setup: func(s *embed.MockStore, e *mocks.MockEmbedder) {
				s.EXPECT().GetWorkflowText(mock.Anything, id).Return(workflow, nil).Once()
				e.EXPECT().EmbedStrings(mock.Anything, mock.Anything).Return(vector(embed.Dimensions), nil).Once()
				s.EXPECT().SetWorkflowEmbedding(mock.Anything, mock.Anything).Return(0, nil).Once()
			},
			wantErrs: []error{&rivertype.JobSnoozeError{}},
		},
		{
			name:   "wrong dimensions cancel the job",
			target: embed.TargetWorkflow,
			setup: func(s *embed.MockStore, e *mocks.MockEmbedder) {
				s.EXPECT().GetWorkflowText(mock.Anything, id).Return(workflow, nil).Once()
				e.EXPECT().EmbedStrings(mock.Anything, mock.Anything).Return(vector(768), nil).Once()
			},
			wantErrs: []error{&rivertype.JobCancelError{}, embed.ErrDimensions},
		},
		{
			name:     "unknown target cancels the job",
			target:   "nonsense",
			wantErrs: []error{&rivertype.JobCancelError{}},
		},
		{
			name:   "store error is retried",
			target: embed.TargetMemory,
			setup: func(s *embed.MockStore, _ *mocks.MockEmbedder) {
				s.EXPECT().GetMemory(mock.Anything, id).Return(db.GetMemoryRow{}, errDB).Once()
			},
			wantErrs: []error{errDB},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			store := embed.NewMockStore(t)
			embedder := mocks.NewMockEmbedder(t)
			if tt.setup != nil {
				tt.setup(store, embedder)
			}
			w := &embed.Worker{Store: store, Embedder: embedder, Model: model}

			err := w.Work(context.Background(), &river.Job[embed.Args]{Args: embed.Args{Target: tt.target, ID: id}})
			if tt.wantErrs == nil {
				require.NoError(t, err)
				return
			}
			for _, want := range tt.wantErrs {
				require.ErrorIs(t, err, want)
			}
		})
	}

}
