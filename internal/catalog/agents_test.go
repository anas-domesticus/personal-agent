package catalog_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/anas-domesticus/personal-agent/internal/catalog"
	"github.com/anas-domesticus/personal-agent/internal/domain"
	"github.com/anas-domesticus/personal-agent/internal/testdb"
)

// version is a valid version input; cases change one field at a time.
func version(prompt string) domain.AgentVersionInput {
	return domain.AgentVersionInput{SystemPrompt: prompt, Model: "openai/gpt-4.1-mini"}
}

func TestAgentsCreate(t *testing.T) {
	t.Parallel()
	st, _ := testdb.Open(t)
	ctx := context.Background()

	taken := catalog.NewAgents(st, catalog.NewMockToolPolicy(t))
	_, err := taken.Create(ctx, "taken", "", version("p"))
	require.NoError(t, err)

	withConfig := func(cfg string) domain.AgentVersionInput {
		in := version("p")
		in.Config = json.RawMessage(cfg)
		return in
	}
	withModel := func(model string) domain.AgentVersionInput {
		in := version("p")
		in.Model = model
		return in
	}

	// Each case gets its own agent name, so cases run in parallel against
	// the shared database.
	tests := []struct {
		name       string
		agent      string
		in         domain.AgentVersionInput
		setup      func(m *catalog.MockToolPolicy)
		wantConfig string // canonical stored config
		wantErr    string // substring; empty means no error
		wantErrIs  error
	}{
		{name: "minimal", agent: "minimal", in: version("p"), wantConfig: `{}`},
		{name: "longest name", agent: "a" + strings.Repeat("b", 62), in: version("p"), wantConfig: `{}`},
		{name: "digits and hyphens", agent: "agent-2", in: version("p"), wantConfig: `{}`},
		{name: "empty name", agent: "", in: version("p"), wantErr: "must be lowercase letters, digits and hyphens"},
		{name: "upper case name", agent: "Agent", in: version("p"), wantErr: "must be lowercase letters, digits and hyphens"},
		{name: "name with a space", agent: "my agent", in: version("p"), wantErr: "must be lowercase letters, digits and hyphens"},
		{name: "name with an underscore", agent: "my_agent", in: version("p"), wantErr: "must be lowercase letters, digits and hyphens"},
		{name: "name starting with a hyphen", agent: "-agent", in: version("p"), wantErr: "must be lowercase letters, digits and hyphens"},
		{name: "name too long", agent: "a" + strings.Repeat("b", 63), in: version("p"), wantErr: "must be lowercase letters, digits and hyphens"},
		{name: "name taken", agent: "taken", in: version("p"), wantErrIs: domain.ErrExists},
		{name: "blank system prompt", agent: "blank-prompt", in: version(" \n"), wantErr: "system prompt is required"},
		{name: "model without provider", agent: "no-provider", in: withModel("gpt-4"), wantErr: "must be an OpenRouter slug"},
		{name: "model with empty provider", agent: "empty-provider", in: withModel("/gpt-4"), wantErr: "must be an OpenRouter slug"},
		{name: "model with empty name", agent: "empty-model", in: withModel("openai/"), wantErr: "must be an OpenRouter slug"},
		{name: "config unknown key", agent: "unknown-key", in: withConfig(`{"nope":1}`), wantErr: "unknown field"},
		{name: "config out of range", agent: "out-of-range", in: withConfig(`{"max_iterations":101}`), wantErr: "max_iterations"},
		{name: "config bad tool value", agent: "bad-tool-value", in: withConfig(`{"tools":{"clock":1}}`),
			wantErr: "tools.clock must be true, false or a settings object"},
		{name: "config is canonicalised and every tool checked", agent: "tools-ok",
			in: withConfig(`{"max_iterations": 3, "tools": {"web_search": { "max_results" : 3 }, "clock": true}}`),
			setup: func(m *catalog.MockToolPolicy) {
				m.EXPECT().Check("clock", json.RawMessage(`true`)).Return(nil).Once()
				m.EXPECT().Check("web_search", json.RawMessage(`{"max_results":3}`)).Return(nil).Once()
			},
			wantConfig: `{"max_iterations":3,"tools":{"clock":true,"web_search":{"max_results":3}}}`},
		{name: "tool policy rejects", agent: "tool-rejected", in: withConfig(`{"tools":{"nope":true}}`),
			setup: func(m *catalog.MockToolPolicy) {
				m.EXPECT().Check("nope", json.RawMessage(`true`)).Return(errors.New("unknown tool")).Once()
			},
			wantErr: "config: tools.nope: unknown tool"},
		{name: "invalid output schema", agent: "bad-schema",
			in:      domain.AgentVersionInput{SystemPrompt: "p", Model: "a/b", OutputSchema: json.RawMessage(`{"type":"string"}`)},
			wantErr: "output schema must have"},
		{name: "every error is reported", agent: "many-errors", in: domain.AgentVersionInput{Model: "x"},
			wantErr: "system prompt is required\nmodel \"x\" must be an OpenRouter slug"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			policy := catalog.NewMockToolPolicy(t) // no expectations: any call fails the test
			if tt.setup != nil {
				tt.setup(policy)
			}
			agents := catalog.NewAgents(st, policy)

			v, err := agents.Create(ctx, tt.agent, "desc", tt.in)
			if tt.wantErr != "" || tt.wantErrIs != nil {
				if tt.wantErr != "" {
					require.ErrorContains(t, err, tt.wantErr)
				}
				if tt.wantErrIs != nil {
					require.ErrorIs(t, err, tt.wantErrIs)
				}
				if tt.wantErrIs == nil {
					_, err := agents.Get(ctx, tt.agent)
					require.ErrorIs(t, err, domain.ErrNotFound, "a rejected agent must not be stored")
				}
				return
			}
			require.NoError(t, err)
			assert.Equal(t, int32(1), v.Version)
			cfg, err := json.Marshal(v.Config)
			require.NoError(t, err)
			assert.JSONEq(t, tt.wantConfig, string(cfg))

			got, err := agents.Get(ctx, tt.agent)
			require.NoError(t, err)
			assert.False(t, got.Admin, "new agents are never admins")
			require.NotNil(t, got.Current)
			assert.Equal(t, int32(1), got.Current.Version)
			assert.Equal(t, v.Config, got.Current.Config)
		})
	}
}

func TestAgentsVersions(t *testing.T) {
	t.Parallel()
	st, _ := testdb.Open(t)
	ctx := context.Background()
	agents := catalog.NewAgents(st, catalog.NewMockToolPolicy(t))

	current := func(t *testing.T) int32 {
		t.Helper()
		d, err := agents.Get(ctx, "versioned")
		require.NoError(t, err)
		require.NotNil(t, d.CurrentVersion)
		return *d.CurrentVersion
	}

	// An ordered scenario over one agent: each step builds on the versions
	// before it, so the steps run sequentially.
	v, err := agents.Create(ctx, "versioned", "", version("one"))
	require.NoError(t, err)
	assert.Equal(t, int32(1), v.Version)

	v, err = agents.AddVersion(ctx, "versioned", version("two"), false)
	require.NoError(t, err)
	assert.Equal(t, int32(2), v.Version)
	assert.Equal(t, int32(1), current(t), "an inactive version doesn't become current")

	v, err = agents.AddVersion(ctx, "versioned", version("three"), true)
	require.NoError(t, err)
	assert.Equal(t, int32(3), v.Version)
	assert.Equal(t, int32(3), current(t))

	_, err = agents.AddVersion(ctx, "versioned", version(""), true)
	require.ErrorContains(t, err, "system prompt is required")

	require.NoError(t, agents.SetVersion(ctx, "versioned", 2))
	d, err := agents.Get(ctx, "versioned")
	require.NoError(t, err)
	require.NotNil(t, d.Current)
	assert.Equal(t, "two", d.Current.SystemPrompt)

	all, err := agents.Versions(ctx, "versioned")
	require.NoError(t, err)
	var numbers []int32
	for _, v := range all {
		numbers = append(numbers, v.Version)
	}
	assert.Equal(t, []int32{3, 2, 1}, numbers, "newest first; the rejected version wasn't stored")

	t.Run("not found", func(t *testing.T) {
		t.Parallel()
		tests := []struct {
			name string
			call func() error
		}{
			{name: "set a missing version", call: func() error { return agents.SetVersion(ctx, "versioned", 99) }},
			{name: "set a version of a missing agent", call: func() error { return agents.SetVersion(ctx, "ghost", 1) }},
			{name: "add a version to a missing agent", call: func() error {
				_, err := agents.AddVersion(ctx, "ghost", version("p"), true)
				return err
			}},
			{name: "get a missing agent", call: func() error {
				_, err := agents.Get(ctx, "ghost")
				return err
			}},
			{name: "versions of a missing agent", call: func() error {
				_, err := agents.Versions(ctx, "ghost")
				return err
			}},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				require.ErrorIs(t, tt.call(), domain.ErrNotFound)
			})
		}
	})
}

func TestAgentsEnsure(t *testing.T) {
	t.Parallel()
	st, _ := testdb.Open(t)
	ctx := context.Background()
	// No expectations: Ensure must not consult the tool policy.
	agents := catalog.NewAgents(st, catalog.NewMockToolPolicy(t))

	seed := version("seed")
	seed.Config = json.RawMessage(`{"tools":{"not_in_this_process":true}}`)

	// Ordered steps over one agent, so they run sequentially.
	require.NoError(t, agents.Ensure(ctx, "seeded", "desc", true, seed))
	d, err := agents.Get(ctx, "seeded")
	require.NoError(t, err)
	assert.True(t, d.Admin)
	require.NotNil(t, d.Current)
	assert.Equal(t, "seed", d.Current.SystemPrompt)
	assert.Equal(t, []string{"not_in_this_process"}, d.Current.Config.Tools.Enabled(), "seed versions skip the tool check")

	// Again, with a different version and admin off: versions are left
	// alone, the admin flag follows the call.
	require.NoError(t, agents.Ensure(ctx, "seeded", "desc", false, version("changed")))
	d, err = agents.Get(ctx, "seeded")
	require.NoError(t, err)
	assert.False(t, d.Admin)
	require.NotNil(t, d.Current)
	assert.Equal(t, "seed", d.Current.SystemPrompt)
	all, err := agents.Versions(ctx, "seeded")
	require.NoError(t, err)
	assert.Len(t, all, 1)

	err = agents.Ensure(ctx, "bad-seed", "", false, version(""))
	require.ErrorContains(t, err, `seed version for "bad-seed"`)

	// Several processes ensuring the same agent at once end with one agent
	// and one version.
	var wg sync.WaitGroup
	errs := make([]error, 8)
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = agents.Ensure(ctx, "racing", "", true, version("p"))
		}()
	}
	wg.Wait()
	for _, err := range errs {
		require.NoError(t, err)
	}
	all, err = agents.Versions(ctx, "racing")
	require.NoError(t, err)
	assert.Len(t, all, 1)
}
