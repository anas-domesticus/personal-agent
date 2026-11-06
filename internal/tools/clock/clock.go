// Package clock provides time tools.
package clock

import (
	"context"
	"time"

	"github.com/cloudwego/eino/components/tool"

	"github.com/anas-domesticus/personal-agent/internal/tools/kit"
)

func Tools() ([]tool.InvokableTool, error) {
	return kit.Build(func() (tool.InvokableTool, error) {
		return kit.New("current_time", "Get the current date and time.",
			func(_ context.Context, _ struct{}) (timeOut, error) {
				now := time.Now()
				zone, _ := now.Zone()
				return timeOut{Time: now.Format(time.RFC3339), Weekday: now.Weekday().String(), Zone: zone}, nil
			})
	})
}

type timeOut struct {
	Time    string `json:"time"`
	Weekday string `json:"weekday"`
	Zone    string `json:"zone"`
}
