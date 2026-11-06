// Package clock provides time tools.
package clock

import (
	"context"
	"time"

	"github.com/cloudwego/eino/components/tool"

	"github.com/anas-domesticus/personal-agent/internal/tools/kit"
)

// Tools returns the current_time tool, which reads the system clock.
func Tools() ([]tool.InvokableTool, error) { return tools(time.Now) }

// tools takes the clock as a function so tests can fix the time.
func tools(now func() time.Time) ([]tool.InvokableTool, error) {
	return kit.Build(func() (tool.InvokableTool, error) {
		return kit.New("current_time", "Get the current date and time.",
			func(_ context.Context, _ struct{}) (timeOut, error) {
				t := now()
				zone, _ := t.Zone()
				return timeOut{Time: t.Format(time.RFC3339), Weekday: t.Weekday().String(), Zone: zone}, nil
			})
	})
}

type timeOut struct {
	Time    string `json:"time"`
	Weekday string `json:"weekday"`
	Zone    string `json:"zone"`
}
