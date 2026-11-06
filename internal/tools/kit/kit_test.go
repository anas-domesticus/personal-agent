package kit_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/anas-domesticus/personal-agent/internal/tools/kit"
)

func TestToolsAreCapped(t *testing.T) {
	big, err := kit.New("big", "x", func(context.Context, struct{}) (string, error) {
		return strings.Repeat("é", 2*kit.MaxOutput), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	out, err := big.InvokableRun(context.Background(), `{}`)
	if err != nil {
		t.Fatal(err)
	}
	if n := len([]rune(out)); n > kit.MaxOutput+300 || !strings.Contains(out, "truncated") {
		t.Fatalf("output not capped: %d runes", n)
	}
	if small := kit.Truncate("short"); small != "short" {
		t.Fatalf("short output changed: %q", small)
	}
}

func TestErrorsBecomeResults(t *testing.T) {
	failing, err := kit.New("failing", "x", func(context.Context, struct{}) (string, error) {
		return "", errors.New("nope")
	})
	if err != nil {
		t.Fatal(err)
	}
	out, err := failing.InvokableRun(context.Background(), `{}`)
	if err != nil || !strings.Contains(out, `"ok":false`) || !strings.Contains(out, "nope") {
		t.Fatalf("out = %q, err = %v", out, err)
	}
}
