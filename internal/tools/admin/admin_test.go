package admin_test

import (
	"slices"
	"testing"

	"github.com/anas-domesticus/personal-agent/internal/tools/admin"
)

func TestNamesMatchTools(t *testing.T) {
	specs, err := admin.New(admin.Deps{})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, s := range specs {
		got = append(got, s.Name)
		if !s.Admin {
			t.Errorf("%s is not marked admin", s.Name)
		}
	}
	if !slices.Equal(slices.Sorted(slices.Values(got)), slices.Sorted(slices.Values(admin.Names))) {
		t.Fatalf("admin.Names = %v, tools = %v", admin.Names, got)
	}
}
