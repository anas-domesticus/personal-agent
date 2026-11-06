package server

import (
	"context"
	"errors"
	"strings"
	"sync"

	"connectrpc.com/connect"
	"github.com/cloudwego/eino/schema"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/anas-domesticus/personal-agent/internal/agent"
	"github.com/anas-domesticus/personal-agent/internal/domain"
	agentv1 "github.com/anas-domesticus/personal-agent/internal/gen/agent/v1"
)

// --- Agents ------------------------------------------------------------------

type agents struct{ src Agents }

func (a *agents) ListAgents(ctx context.Context, _ *connect.Request[agentv1.ListAgentsRequest]) (*connect.Response[agentv1.ListAgentsResponse], error) {
	list, err := a.src.List(ctx)
	if err != nil {
		return nil, toConnect(err)
	}
	out := &agentv1.ListAgentsResponse{}
	for _, ag := range list {
		pb := &agentv1.Agent{Name: ag.Name, Description: ag.Description, Admin: ag.Admin}
		if ag.CurrentVersion != nil {
			pb.Version = *ag.CurrentVersion
		}
		out.Agents = append(out.Agents, pb)
	}
	return connect.NewResponse(out), nil
}

func (a *agents) GetAgent(ctx context.Context, req *connect.Request[agentv1.GetAgentRequest]) (*connect.Response[agentv1.GetAgentResponse], error) {
	d, err := a.src.Get(ctx, req.Msg.Name)
	if err != nil {
		return nil, toConnect(err)
	}
	pb := &agentv1.Agent{Name: d.Name, Description: d.Description, Admin: d.Admin}
	if v := d.Current; v != nil {
		pb.Version, pb.Model, pb.Tools = v.Version, v.Model, v.Config.Tools.Enabled()
	}
	return connect.NewResponse(&agentv1.GetAgentResponse{Agent: pb}), nil
}

// --- Conversations -----------------------------------------------------------

const (
	defaultListLimit = 50
	maxListLimit     = 200
)

type conversations struct{ chat Chat }

func (c *conversations) CreateConversation(ctx context.Context, req *connect.Request[agentv1.CreateConversationRequest]) (*connect.Response[agentv1.CreateConversationResponse], error) {
	name := req.Msg.Agent
	if name == "" {
		name = agent.AdminName
	}
	conv, err := c.chat.Start(ctx, name)
	if err != nil {
		return nil, toConnect(err)
	}
	return connect.NewResponse(&agentv1.CreateConversationResponse{Conversation: conversationPB(conv)}), nil
}

func (c *conversations) ListConversations(ctx context.Context, req *connect.Request[agentv1.ListConversationsRequest]) (*connect.Response[agentv1.ListConversationsResponse], error) {
	limit := req.Msg.Limit
	if limit <= 0 {
		limit = defaultListLimit
	}
	limit = min(limit, maxListLimit)
	list, err := c.chat.List(ctx, limit)
	if err != nil {
		return nil, toConnect(err)
	}
	out := &agentv1.ListConversationsResponse{}
	for _, conv := range list {
		out.Conversations = append(out.Conversations, conversationPB(conv))
	}
	return connect.NewResponse(out), nil
}

func (c *conversations) GetConversation(ctx context.Context, req *connect.Request[agentv1.GetConversationRequest]) (*connect.Response[agentv1.GetConversationResponse], error) {
	id, err := parseID("id", req.Msg.Id)
	if err != nil {
		return nil, err
	}
	conv, err := c.chat.Get(ctx, id)
	if err != nil {
		return nil, toConnect(err)
	}
	history, err := c.chat.History(ctx, id)
	if err != nil {
		return nil, toConnect(err)
	}
	out := &agentv1.GetConversationResponse{Conversation: conversationPB(conv), Busy: c.chat.Busy(id)}
	for _, m := range history {
		out.Messages = append(out.Messages, messagePB(m))
	}
	return connect.NewResponse(out), nil
}

func (c *conversations) SendMessage(ctx context.Context, req *connect.Request[agentv1.SendMessageRequest], stream *connect.ServerStream[agentv1.SendMessageResponse]) error {
	id, err := parseID("conversation_id", req.Msg.ConversationId)
	if err != nil {
		return err
	}
	text := strings.TrimSpace(req.Msg.Text)
	if text == "" {
		return connect.NewError(connect.CodeInvalidArgument, errors.New("text is empty"))
	}

	// Events go to the client through a buffer, so a slow or half-open
	// client never holds up the turn. If the client falls a buffer behind,
	// or a send fails, it is dropped and the turn runs on without it.
	out := newWriter(stream)
	defer out.close()
	emit := out.emit
	started := false
	obs := agent.Observer{
		Started: func(info agent.Info) {
			started = true
			emit(&agentv1.SendMessageResponse{Event: &agentv1.SendMessageResponse_Started{Started: &agentv1.TurnStarted{
				Agent: info.Name, Version: info.Version, Model: info.Model,
			}}})
		},
		Delta: func(chunk *schema.Message) {
			emit(&agentv1.SendMessageResponse{Event: &agentv1.SendMessageResponse_Delta{Delta: &agentv1.MessageDelta{
				Content: chunk.Content, Reasoning: chunk.ReasoningContent,
			}}})
		},
		Message: func(m *schema.Message) {
			emit(&agentv1.SendMessageResponse{Event: &agentv1.SendMessageResponse_Message{Message: messagePB(m)}})
		},
	}

	// The turn belongs to the server, not the request: it has side effects
	// and costs money, so it finishes and is saved even if the client goes.
	// CancelTurn and server shutdown still interrupt it.
	turn, err := c.chat.Send(context.WithoutCancel(ctx), id, text, obs)
	if err != nil && !started {
		return toConnect(err) // nothing ran: busy, not found, or the agent can't be built
	}

	done := &agentv1.TurnCompleted{
		Status:           agentv1.TurnStatus_TURN_STATUS_COMPLETED,
		PromptTokens:     int64(turn.PromptTokens),
		CompletionTokens: int64(turn.CompletionTokens),
	}
	switch {
	case errors.Is(err, context.Canceled):
		done.Status = agentv1.TurnStatus_TURN_STATUS_INTERRUPTED
	case err != nil:
		done.Status, done.Error = agentv1.TurnStatus_TURN_STATUS_FAILED, err.Error()
	}
	if conv, err := c.chat.Get(context.WithoutCancel(ctx), id); err == nil {
		done.Conversation = conversationPB(conv)
	}
	emit(&agentv1.SendMessageResponse{Event: &agentv1.SendMessageResponse_Completed{Completed: done}})
	out.close()
	if out.gone {
		// The client missed events, so tell it to reload rather than trust
		// what it has.
		return connect.NewError(connect.CodeResourceExhausted, errors.New("the client fell behind the turn's events; reload the conversation"))
	}
	return nil
}

func (c *conversations) CancelTurn(_ context.Context, req *connect.Request[agentv1.CancelTurnRequest]) (*connect.Response[agentv1.CancelTurnResponse], error) {
	id, err := parseID("conversation_id", req.Msg.ConversationId)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&agentv1.CancelTurnResponse{Cancelled: c.chat.Cancel(id)}), nil
}

// --- Conversions -------------------------------------------------------------

func conversationPB(c domain.Conversation) *agentv1.Conversation {
	pb := &agentv1.Conversation{
		Id:         c.ID.String(),
		Agent:      c.Agent,
		Title:      c.Title,
		CreateTime: timestamppb.New(c.CreatedAt),
		UpdateTime: timestamppb.New(c.UpdatedAt),
	}
	if c.ParentID != nil {
		pb.ParentId = c.ParentID.String()
	}
	return pb
}

var roles = map[schema.RoleType]agentv1.Role{
	schema.System:    agentv1.Role_ROLE_SYSTEM,
	schema.User:      agentv1.Role_ROLE_USER,
	schema.Assistant: agentv1.Role_ROLE_ASSISTANT,
	schema.Tool:      agentv1.Role_ROLE_TOOL,
}

func messagePB(m *schema.Message) *agentv1.Message {
	pb := &agentv1.Message{
		Role:       roles[m.Role],
		Content:    m.Content,
		Reasoning:  m.ReasoningContent,
		ToolCallId: m.ToolCallID,
		ToolName:   m.ToolName,
	}
	for _, tc := range m.ToolCalls {
		pb.ToolCalls = append(pb.ToolCalls, &agentv1.ToolCall{Id: tc.ID, Name: tc.Function.Name, Arguments: tc.Function.Arguments})
	}
	return pb
}

// eventBuffer is how many events a client may fall behind before it's
// dropped. Deltas are small and frequent, so this is a few seconds' worth.
const eventBuffer = 1024

// writer sends a turn's events to its client from its own goroutine.
type writer struct {
	events chan *agentv1.SendMessageResponse
	done   chan struct{}
	gone   bool // set by emit when the buffer overflows; turn goroutine only
	once   sync.Once
}

func newWriter(stream *connect.ServerStream[agentv1.SendMessageResponse]) *writer {
	w := &writer{events: make(chan *agentv1.SendMessageResponse, eventBuffer), done: make(chan struct{})}
	go func() {
		defer close(w.done)
		failed := false
		for ev := range w.events {
			if !failed && stream.Send(ev) != nil {
				failed = true // keep draining so emit never blocks
			}
		}
	}()
	return w
}

// emit queues an event. It never blocks.
func (w *writer) emit(ev *agentv1.SendMessageResponse) {
	if w.gone {
		return
	}
	select {
	case w.events <- ev:
	default:
		w.gone = true
	}
}

// close sends what's queued and waits for the writer to finish. The handler
// must not return while it is still writing to the stream.
func (w *writer) close() {
	w.once.Do(func() { close(w.events) })
	<-w.done
}
