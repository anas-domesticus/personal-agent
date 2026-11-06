package clock_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/anas-domesticus/personal-agent/internal/tools/clock"
	"github.com/anas-domesticus/personal-agent/internal/tools/kit"
)

func TestCurrentTime(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		now  time.Time
		want string
	}{
		{
			name: "utc",
			now:  time.Date(2026, 11, 6, 9, 30, 0, 0, time.UTC),
			want: `{"ok":true,"data":{"time":"2026-11-06T09:30:00Z","weekday":"Friday","zone":"UTC"}}`,
		},
		{
			name: "fixed offset zone",
			now:  time.Date(2026, 1, 4, 23, 0, 0, 0, time.FixedZone("XYZ", -5*3600)),
			want: `{"ok":true,"data":{"time":"2026-01-04T23:00:00-05:00","weekday":"Sunday","zone":"XYZ"}}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ts, err := clock.ToolsAt(func() time.Time { return tt.now })
			require.NoError(t, err)
			require.Len(t, ts, 1)
			assert.Equal(t, "current_time", kit.Name(ts[0]))

			out, err := ts[0].InvokableRun(context.Background(), `{}`)
			require.NoError(t, err)
			assert.JSONEq(t, tt.want, out)
		})
	}
}

func TestTools(t *testing.T) {
	t.Parallel()
	ts, err := clock.Tools()
	require.NoError(t, err)
	require.Len(t, ts, 1)
	assert.Equal(t, "current_time", kit.Name(ts[0]))
}
