// Package client connects to an agent server. It is all a UI needs: the
// generated Connect clients, pointed at a URL and carrying the token.
package client

import (
	"net/http"
	"strings"

	"github.com/anas-domesticus/personal-agent/internal/gen/agent/v1/agentv1connect"
)

// Client is a connection to one server: its URL and a client per service.
type Client struct {
	URL           string
	Agents        agentv1connect.AgentServiceClient
	Conversations agentv1connect.ConversationServiceClient
}

// New connects to the server at url (e.g. http://127.0.0.1:7433). token, if
// set, is sent as a bearer token. Streams can run for minutes, so the HTTP
// client has no overall timeout; calls take their deadlines from contexts.
func New(url, token string) *Client {
	url = strings.TrimRight(url, "/")
	hc := &http.Client{Transport: bearer{token: token, next: http.DefaultTransport}}
	return &Client{
		URL:           url,
		Agents:        agentv1connect.NewAgentServiceClient(hc, url),
		Conversations: agentv1connect.NewConversationServiceClient(hc, url),
	}
}

type bearer struct {
	token string
	next  http.RoundTripper
}

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	if b.token != "" {
		r = r.Clone(r.Context())
		r.Header.Set("Authorization", "Bearer "+b.token)
	}
	//nolint:wrapcheck // a RoundTripper passes errors through; http.Client adds the context
	return b.next.RoundTrip(r)
}
