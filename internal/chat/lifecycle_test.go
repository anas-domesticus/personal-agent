package chat_test

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/anas-domesticus/personal-agent/internal/agent"
	"github.com/anas-domesticus/personal-agent/internal/chat"
	"github.com/anas-domesticus/personal-agent/internal/domain"
)

// blockingRunnable runs a tool, then waits until its context ends, as a
// model call does when interrupted. It's a fake because the test needs a turn
// that is reliably in progress, which a mock's scripted return can't hold.
type blockingRunnable struct {
	started chan struct{}
}

var _ agent.Runnable = (*blockingRunnable)(nil)

func (r *blockingRunnable) Info() agent.Info { return agent.Info{Name: "admin", Version: 1} }

func (r *blockingRunnable) Run(ctx context.Context, _ []*schema.Message, _ agent.Observer) (agent.Turn, error) {
	ran := []*schema.Message{toolCall("c1"), schema.ToolMessage("done", "c1")}
	close(r.started)
	<-ctx.Done()
	return agent.Turn{Messages: ran}, fmt.Errorf("model call: %w", ctx.Err())
}

// turnSave lets a test hold the save of a partial turn: the store signals
// saving when the save starts and finishes it when release is closed.
type turnSave struct {
	saving  chan struct{}
	release chan struct{}
	saved   atomic.Bool
}

// running starts a turn that blocks until interrupted.
func running(t *testing.T, id uuid.UUID) (s *chat.Service, result chan error, save *turnSave) {
	t.Helper()
	store := chat.NewMockStore(t)
	agents := chat.NewMockAgents(t)
	r := &blockingRunnable{started: make(chan struct{})}
	save = &turnSave{saving: make(chan struct{}), release: make(chan struct{})}
	store.EXPECT().Get(mock.Anything, id).Return(domain.Conversation{ID: id, Agent: "admin", Title: "t"}, nil).Once()
	store.EXPECT().History(mock.Anything, id).Return(nil, int64(0), nil).Once()
	agents.EXPECT().Build(mock.Anything, "admin").Return(r, nil).Once()
	store.EXPECT().AppendTurn(mock.Anything, id, int64(0), mock.Anything, int32(1), "").
		RunAndReturn(func(context.Context, uuid.UUID, int64, []*schema.Message, int32, string) error {
			close(save.saving)
			<-save.release
			save.saved.Store(true)
			return nil
		}).Once()

	s = chat.New(store, agents)
	result = make(chan error, 1)
	go func() {
		_, err := s.Send(context.Background(), id, "go", agent.Observer{})
		result <- err
	}()
	<-r.started
	return s, result, save
}

func TestCancelInterruptsTheTurn(t *testing.T) {
	t.Parallel()
	id := uuid.New()
	s, result, save := running(t, id)
	close(save.release)

	assert.True(t, s.Busy(id), "busy while the turn runs")
	assert.False(t, s.Cancel(uuid.New()), "cancelling an idle conversation")
	assert.True(t, s.Cancel(id))

	require.ErrorIs(t, <-result, context.Canceled)
	assert.True(t, save.saved.Load(), "the part where a tool ran is saved")
	assert.False(t, s.Busy(id), "busy after the turn ended")
	assert.False(t, s.Cancel(id), "cancelling after the turn ended")
}

func TestCloseInterruptsAndWaitsForTheSave(t *testing.T) {
	t.Parallel()
	id := uuid.New()
	s, result, save := running(t, id)
	closed := make(chan struct{})
	go func() {
		s.Close(context.Background())
		close(closed)
	}()

	<-save.saving // Close interrupted the turn, which is now being saved
	select {
	case <-closed:
		t.Fatal("Close returned before the turn was saved")
	default:
	}
	close(save.release)
	<-closed
	assert.True(t, save.saved.Load())
	require.ErrorIs(t, <-result, context.Canceled)
	_, err := s.Send(context.Background(), uuid.New(), "late", agent.Observer{})
	require.ErrorIs(t, err, domain.ErrUnavailable)
}

func TestCloseGivesUpWhenItsContextEnds(t *testing.T) {
	t.Parallel()
	id := uuid.New()
	s, result, save := running(t, id)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	s.Close(ctx) // returns at once rather than waiting for the save
	assert.False(t, save.saved.Load())

	close(save.release)
	require.ErrorIs(t, <-result, context.Canceled, "the turn was still interrupted")
}
