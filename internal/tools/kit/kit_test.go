package kit_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/cloudwego/eino/components/tool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/anas-domesticus/personal-agent/internal/tools/kit"
)

func TestToolsAreCapped(t *testing.T) {
	t.Parallel()
	big, err := kit.New("big", "x", func(context.Context, struct{}) (string, error) {
		return strings.Repeat("é", 2*kit.MaxOutput), nil
	})
	require.NoError(t, err)

	out, err := big.InvokableRun(context.Background(), `{}`)
	require.NoError(t, err)
	assert.LessOrEqual(t, utf8.RuneCountInString(out), kit.MaxOutput+300)
	assert.Contains(t, out, "truncated")
}

func TestTruncate(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name          string
		in            string
		wantUnchanged bool
	}{
		{name: "short", in: "short", wantUnchanged: true},
		{name: "exactly the cap", in: strings.Repeat("x", kit.MaxOutput), wantUnchanged: true},
		{name: "multi-byte under the cap in runes", in: strings.Repeat("é", kit.MaxOutput), wantUnchanged: true},
		{name: "over the cap", in: strings.Repeat("x", kit.MaxOutput+1)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := kit.Truncate(tt.in)
			if tt.wantUnchanged {
				assert.Equal(t, tt.in, got)
				return
			}
			assert.True(t, strings.HasPrefix(got, tt.in[:kit.MaxOutput]))
			assert.Contains(t, got, "[truncated: the result was")
		})
	}
}

func TestErrorsBecomeResults(t *testing.T) {
	t.Parallel()
	failing, err := kit.New("failing", "x", func(context.Context, struct{}) (string, error) {
		return "", errors.New("nope")
	})
	require.NoError(t, err)

	out, err := failing.InvokableRun(context.Background(), `{}`)
	require.NoError(t, err)
	assert.JSONEq(t, `{"ok":false,"error":"nope"}`, out)
}

func TestResultsCarryData(t *testing.T) {
	t.Parallel()
	echo, err := kit.New("echo", "x", func(_ context.Context, in struct {
		Text string `json:"text"`
	}) (string, error) {
		return in.Text, nil
	})
	require.NoError(t, err)

	out, err := echo.InvokableRun(context.Background(), `{"text":"hi"}`)
	require.NoError(t, err)
	assert.JSONEq(t, `{"ok":true,"data":"hi"}`, out)
}

func TestCancelledCallIsAGoError(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	tl, err := kit.New("slow", "x", func(ctx context.Context, _ struct{}) (string, error) {
		cancel()
		return "", ctx.Err()
	})
	require.NoError(t, err)

	_, err = tl.InvokableRun(ctx, `{}`)
	require.ErrorIs(t, err, context.Canceled)
}

func TestLimit(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		n    int32
		want int32
	}{
		{name: "zero uses the default", n: 0, want: 7},
		{name: "negative uses the default", n: -3, want: 7},
		{name: "in range", n: 20, want: 20},
		{name: "clamped to 50", n: 500, want: 50},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, kit.Limit(tt.n, 7))
		})
	}
}

// limitSettings is a settings type with validation.
type limitSettings struct {
	Limit int `json:"limit"`
}

func (s *limitSettings) Validate() error {
	if s.Limit < 0 {
		return errors.New("limit must not be negative")
	}
	return nil
}

func TestDecodeSettings(t *testing.T) {
	t.Parallel()
	withSettings := kit.Spec{Name: "limited", Settings: func() any { return &limitSettings{Limit: 5} }}
	noSettings := kit.Spec{Name: "plain"}

	tests := []struct {
		name    string
		spec    kit.Spec
		raw     string
		want    any
		wantErr string
	}{
		{name: "true gives defaults", spec: withSettings, raw: "true", want: &limitSettings{Limit: 5}},
		{name: "empty gives defaults", spec: withSettings, raw: "", want: &limitSettings{Limit: 5}},
		{name: "object overrides defaults", spec: withSettings, raw: ` {"limit": 3} `, want: &limitSettings{Limit: 3}},
		{name: "validation fails", spec: withSettings, raw: `{"limit": -1}`, wantErr: "must not be negative"},
		{name: "unknown setting", spec: withSettings, raw: `{"limt": 3}`, wantErr: "unknown field"},
		{name: "true without settings", spec: noSettings, raw: "true", want: nil},
		{name: "object without settings", spec: noSettings, raw: `{"x": 1}`, wantErr: `tool "plain" takes no settings`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := tt.spec.DecodeSettings(json.RawMessage(tt.raw))
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestSpecs(t *testing.T) {
	t.Parallel()
	tl, err := kit.New("multi", "First line.\nMore detail.", func(context.Context, struct{}) (string, error) { return "", nil })
	require.NoError(t, err)

	specs, err := kit.Specs([]tool.InvokableTool{tl}, nil)
	require.NoError(t, err)
	require.Len(t, specs, 1)
	assert.Equal(t, "multi", specs[0].Name)
	assert.Equal(t, "First line.", specs[0].Summary)
	assert.Nil(t, specs[0].Settings)
	built, err := specs[0].New(nil)
	require.NoError(t, err)
	assert.Equal(t, tl, built)

	_, err = kit.Specs(nil, errors.New("build failed"))
	require.ErrorContains(t, err, "build failed")
}

func TestBuild(t *testing.T) {
	t.Parallel()
	ok := func() (tool.InvokableTool, error) {
		return kit.New("ok", "x", func(context.Context, struct{}) (string, error) { return "", nil })
	}
	ts, err := kit.Build(ok, ok)
	require.NoError(t, err)
	assert.Len(t, ts, 2)

	_, err = kit.Build(ok, func() (tool.InvokableTool, error) { return nil, errors.New("bad") })
	require.ErrorContains(t, err, "bad")
}

func TestName(t *testing.T) {
	t.Parallel()
	tl, err := kit.New("named", "x", func(context.Context, struct{}) (string, error) { return "", nil })
	require.NoError(t, err)
	assert.Equal(t, "named", kit.Name(tl))
}
