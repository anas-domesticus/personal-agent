package files_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/tool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/anas-domesticus/personal-agent/internal/tools/files"
	"github.com/anas-domesticus/personal-agent/internal/workspace"
)

// env is the file tools over a fresh workspace in a temporary directory.
type env struct {
	ws    *workspace.Local
	tools map[string]tool.InvokableTool
}

// setup builds the tools over a new workspace holding the given files.
func setup(t *testing.T, contents map[string]string) *env {
	t.Helper()
	ws, err := workspace.NewLocal(t.TempDir())
	require.NoError(t, err)
	specs, err := files.Specs(ws)
	require.NoError(t, err)
	e := &env{ws: ws, tools: map[string]tool.InvokableTool{}}
	for _, s := range specs {
		require.NotEmpty(t, s.Summary, s.Name)
		require.Nil(t, s.Settings, s.Name)
		tl, err := s.New(nil)
		require.NoError(t, err)
		e.tools[s.Name] = tl
	}
	for p, c := range contents {
		e.put(t, p, c)
	}
	return e
}

type result struct {
	OK    bool            `json:"ok"`
	Error string          `json:"error"`
	Data  json.RawMessage `json:"data"`
}

// call runs a tool and returns its result.
func (e *env) call(t *testing.T, name string, args any) result {
	t.Helper()
	raw, err := json.Marshal(args)
	require.NoError(t, err)
	out, err := e.tools[name].InvokableRun(context.Background(), string(raw))
	require.NoError(t, err, name)
	var r result
	require.NoError(t, json.Unmarshal([]byte(out), &r), "%s: bad result %q", name, out)
	return r
}

// run calls a tool. With wantErr set it expects an error result containing
// it; otherwise it expects success and decodes the data into v.
func (e *env) run(t *testing.T, name string, args any, wantErr string, v any) bool {
	t.Helper()
	r := e.call(t, name, args)
	if wantErr != "" {
		assert.False(t, r.OK)
		assert.Contains(t, r.Error, wantErr)
		return false
	}
	require.True(t, r.OK, "%s(%v) failed: %s", name, args, r.Error)
	require.NoError(t, json.Unmarshal(r.Data, v))
	return true
}

func (e *env) file(t *testing.T, path string) string {
	t.Helper()
	data, err := e.ws.Read(context.Background(), path)
	require.NoError(t, err)
	return string(data)
}

func (e *env) put(t *testing.T, path, content string) {
	t.Helper()
	require.NoError(t, e.ws.Write(context.Background(), path, []byte(content)))
}

func TestSpecs(t *testing.T) {
	t.Parallel()
	e := setup(t, nil)
	var names []string
	for n, tl := range e.tools {
		names = append(names, n)
		info, err := tl.Info(context.Background())
		require.NoError(t, err, n)
		assert.NotEmpty(t, info.Desc, n)
		assert.NotNil(t, info.ParamsOneOf, n)
	}
	slices.Sort(names)
	assert.Equal(t, []string{"edit", "glob", "grep", "read", "shell", "write"}, names)

	// Required fields come from non-omitempty tags.
	info, err := e.tools["edit"].Info(context.Background())
	require.NoError(t, err)
	schema, err := info.ToJSONSchema()
	require.NoError(t, err)
	assert.Equal(t, []string{"path", "old_string", "new_string"}, schema.Required)
}

func TestRead(t *testing.T) {
	t.Parallel()
	lines := make([]string, 0, 2500)
	for i := 1; i <= 2500; i++ {
		lines = append(lines, fmt.Sprintf("l%d", i))
	}
	fixtures := map[string]string{
		"big.txt":   strings.Join(lines, "\n") + "\n",
		"small.txt": "alpha\nbeta\r\ngamma",
		"long.txt":  strings.Repeat("x", 3000),
		"bin":       "a\x00b",
		"dir/sub/x": "",
		"dir/a.txt": "",
	}
	e := setup(t, fixtures)

	tests := []struct {
		name            string
		args            map[string]any
		wantPrefix      string
		wantContains    []string
		wantNotContains []string
		wantXs          int // exact count of "x" in the output, if set
		wantErr         string
	}{
		{
			name:         "small file with line numbers",
			args:         map[string]any{"path": "small.txt"},
			wantPrefix:   "     1\talpha\n     2\tbeta\n     3\tgamma\n",
			wantContains: []string{"3 lines total"},
		},
		{
			name:         "paged",
			args:         map[string]any{"path": "small.txt", "offset": 2, "limit": 1},
			wantPrefix:   "     2\tbeta\n",
			wantContains: []string{"offset=3"},
		},
		{
			name:         "tail of a big file",
			args:         map[string]any{"path": "big.txt", "offset": 2400},
			wantPrefix:   "  2400\tl2400\n",
			wantContains: []string{"  2500\tl2500\n", "2500 lines total"},
		},
		{
			name:            "big file stops before the cap and says where to continue",
			args:            map[string]any{"path": "big.txt"},
			wantContains:    []string{"Use offset="},
			wantNotContains: []string{"truncated"},
		},
		{
			name:         "long line is cut",
			args:         map[string]any{"path": "long.txt"},
			wantContains: []string{"(line cut)"},
			wantXs:       files.MaxLineLength,
		},
		{
			name:         "directory lists entries",
			args:         map[string]any{"path": "dir"},
			wantContains: []string{"     1\ta.txt\n     2\tsub/\n"},
		},
		{name: "offset past the end", args: map[string]any{"path": "big.txt", "offset": 3000}, wantErr: "past the end"},
		{name: "missing file", args: map[string]any{"path": "missing.txt"}, wantErr: "not found"},
		{name: "path escapes root", args: map[string]any{"path": "../x"}, wantErr: "outside"},
		{name: "binary file", args: map[string]any{"path": "bin"}, wantErr: "binary"},
		{name: "no path", args: map[string]any{}, wantErr: "path is required"},
		{name: "negative offset", args: map[string]any{"path": "small.txt", "offset": -1}, wantErr: "must not be negative"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var out string
			if !e.run(t, "read", tt.args, tt.wantErr, &out) {
				return
			}
			assert.True(t, strings.HasPrefix(out, tt.wantPrefix), "output starts %q", out[:min(len(out), 200)])
			for _, s := range tt.wantContains {
				assert.Contains(t, out, s)
			}
			for _, s := range tt.wantNotContains {
				assert.NotContains(t, out, s)
			}
			if tt.wantXs != 0 {
				assert.Equal(t, tt.wantXs, strings.Count(out, "x"))
			}
		})
	}
}

func TestWrite(t *testing.T) {
	t.Parallel()
	type writeOut struct {
		Created bool `json:"created"`
		Bytes   int  `json:"bytes"`
	}
	tests := []struct {
		name     string
		existing map[string]string
		args     map[string]any
		want     writeOut
		wantFile string
		wantErr  string
	}{
		{
			name:     "creates parent directories",
			args:     map[string]any{"path": "a/b.txt", "content": "hi"},
			want:     writeOut{Created: true, Bytes: 2},
			wantFile: "hi",
		},
		{
			name:     "overwrites",
			existing: map[string]string{"a/b.txt": "hi"},
			args:     map[string]any{"path": "a/b.txt", "content": "bye"},
			want:     writeOut{Created: false, Bytes: 3},
			wantFile: "bye",
		},
		{
			name:     "path is a directory",
			existing: map[string]string{"a/b.txt": "hi"},
			args:     map[string]any{"path": "a", "content": "x"},
			wantErr:  "directory",
		},
		{name: "path escapes root", args: map[string]any{"path": "/tmp/escape.txt", "content": "x"}, wantErr: "outside"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			e := setup(t, tt.existing)
			var out writeOut
			if !e.run(t, "write", tt.args, tt.wantErr, &out) {
				return
			}
			assert.Equal(t, tt.want, out)
			assert.Equal(t, tt.wantFile, e.file(t, tt.args["path"].(string)))
		})
	}
}

func TestEdit(t *testing.T) {
	t.Parallel()
	type editOut struct {
		Replacements int  `json:"replacements"`
		Created      bool `json:"created"`
	}
	const src = "a := 1\nb := 1\nc := 2\n"
	tests := []struct {
		name     string
		existing map[string]string
		args     map[string]any
		want     editOut
		wantFile string
		wantErr  string
	}{
		{
			name:     "single replacement",
			existing: map[string]string{"f.go": src},
			args:     map[string]any{"path": "f.go", "old_string": "c := 2", "new_string": "c := 3"},
			want:     editOut{Replacements: 1},
			wantFile: "a := 1\nb := 1\nc := 3\n",
		},
		{
			name:     "ambiguous match",
			existing: map[string]string{"f.go": src},
			args:     map[string]any{"path": "f.go", "old_string": ":= 1", "new_string": ":= 9"},
			wantErr:  "matches 2 times",
		},
		{
			name:     "no match",
			existing: map[string]string{"f.go": src},
			args:     map[string]any{"path": "f.go", "old_string": "zzz", "new_string": "y"},
			wantErr:  "not found",
		},
		{
			name:     "same text",
			existing: map[string]string{"f.go": src},
			args:     map[string]any{"path": "f.go", "old_string": "a", "new_string": "a"},
			wantErr:  "same",
		},
		{
			name:     "replace all",
			existing: map[string]string{"f.go": src},
			args:     map[string]any{"path": "f.go", "old_string": ":= 1", "new_string": ":= 9", "replace_all": true},
			want:     editOut{Replacements: 2},
			wantFile: "a := 9\nb := 9\nc := 2\n",
		},
		{
			name:     "empty old_string creates a file",
			args:     map[string]any{"path": "new/n.txt", "old_string": "", "new_string": "fresh"},
			want:     editOut{Replacements: 1, Created: true},
			wantFile: "fresh",
		},
		{
			name:     "empty old_string on an existing file",
			existing: map[string]string{"new/n.txt": "fresh"},
			args:     map[string]any{"path": "new/n.txt", "old_string": "", "new_string": "again"},
			wantErr:  "already exists",
		},
		{
			name:    "missing file",
			args:    map[string]any{"path": "nope.txt", "old_string": "x", "new_string": "y"},
			wantErr: "not found",
		},
		{
			name:     "keeps CRLF line endings",
			existing: map[string]string{"crlf.txt": "one\r\ntwo\r\nthree\r\n"},
			args:     map[string]any{"path": "crlf.txt", "old_string": "one\ntwo", "new_string": "1\n2"},
			want:     editOut{Replacements: 1},
			wantFile: "1\r\n2\r\nthree\r\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			e := setup(t, tt.existing)
			var out editOut
			if !e.run(t, "edit", tt.args, tt.wantErr, &out) {
				return
			}
			assert.Equal(t, tt.want, out)
			assert.Equal(t, tt.wantFile, e.file(t, tt.args["path"].(string)))
		})
	}
}

func TestGlob(t *testing.T) {
	t.Parallel()
	e := setup(t, map[string]string{"a.go": "", "pkg/b.go": "", "pkg/c.ts": "", ".hid/d.go": ""})
	type globOut struct {
		Files     []string `json:"files"`
		Truncated bool     `json:"truncated"`
	}
	tests := []struct {
		name          string
		args          map[string]any
		wantFiles     []string // exact, in any order; nil means check the count only
		wantCount     int
		wantTruncated bool
	}{
		{name: "recursive", args: map[string]any{"pattern": "**/*.go"}, wantFiles: []string{"a.go", "pkg/b.go"}},
		{name: "under a path", args: map[string]any{"pattern": "*", "path": "pkg"}, wantCount: 2},
		{name: "hidden", args: map[string]any{"pattern": "*.go", "hidden": true}, wantCount: 3},
		{name: "limit", args: map[string]any{"pattern": "**/*", "limit": 2}, wantCount: 2, wantTruncated: true},
		{name: "no matches is an empty list", args: map[string]any{"pattern": "*.rs"}, wantFiles: []string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var out globOut
			e.run(t, "glob", tt.args, "", &out)
			require.NotNil(t, out.Files)
			if tt.wantFiles != nil {
				assert.ElementsMatch(t, tt.wantFiles, out.Files)
			} else {
				assert.Len(t, out.Files, tt.wantCount)
			}
			assert.Equal(t, tt.wantTruncated, out.Truncated)
		})
	}
}

func TestGrep(t *testing.T) {
	t.Parallel()
	e := setup(t, map[string]string{
		"a.go":  "package a\nfunc Foo() {}\nfunc bar() {}\n",
		"b.ts":  "function foo() {}\n",
		"c.tsx": "Foo.x(1+1)\n",
	})
	type grepOut struct {
		Matches   int    `json:"matches"`
		Truncated bool   `json:"truncated"`
		Output    string `json:"output"`
	}
	tests := []struct {
		name            string
		args            map[string]any
		wantMatches     int
		wantTruncated   bool
		wantOutput      string // exact, if set
		wantContains    string
		wantNotContains string
		wantErr         string
	}{
		{
			name:        "case sensitive by default",
			args:        map[string]any{"pattern": "Foo"},
			wantMatches: 2,
			wantOutput:  "a.go:2: func Foo() {}\n\nc.tsx:1: Foo.x(1+1)\n",
		},
		{name: "case insensitive", args: map[string]any{"pattern": "foo", "case_sensitive": false}, wantMatches: 3},
		{
			name:            "include filter",
			args:            map[string]any{"pattern": "foo", "case_sensitive": false, "include": "*.{ts,tsx}"},
			wantMatches:     2,
			wantNotContains: "a.go",
		},
		{name: "literal", args: map[string]any{"pattern": "x(1+1)", "literal": true}, wantMatches: 1},
		{name: "regex on one file", args: map[string]any{"pattern": `^func \w+\(`, "path": "a.go"}, wantMatches: 2},
		{
			name:          "limit",
			args:          map[string]any{"pattern": "func", "limit": 1},
			wantMatches:   1,
			wantTruncated: true,
			wantContains:  "first 1 matches",
		},
		{name: "no matches", args: map[string]any{"pattern": "nothing-here"}, wantOutput: "No matches found."},
		{name: "invalid regex", args: map[string]any{"pattern": "("}, wantErr: "invalid regular expression"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var out grepOut
			if !e.run(t, "grep", tt.args, tt.wantErr, &out) {
				return
			}
			assert.Equal(t, tt.wantMatches, out.Matches)
			assert.Equal(t, tt.wantTruncated, out.Truncated)
			if tt.wantOutput != "" {
				assert.Equal(t, tt.wantOutput, out.Output)
			}
			if tt.wantContains != "" {
				assert.Contains(t, out.Output, tt.wantContains)
			}
			if tt.wantNotContains != "" {
				assert.NotContains(t, out.Output, tt.wantNotContains)
			}
		})
	}
}

func TestShell(t *testing.T) {
	t.Parallel()
	type shellOut struct {
		ExitCode  int    `json:"exit_code"`
		Output    string `json:"output"`
		TimedOut  bool   `json:"timed_out"`
		Truncated bool   `json:"truncated"`
	}
	tests := []struct {
		name          string
		args          map[string]any
		wantExit      int
		wantOutput    string // exact, if set
		wantContains  string
		wantSuffix    string
		wantTimedOut  bool
		wantTruncated bool
		wantErr       string
	}{
		{
			name:       "exit code and output",
			args:       map[string]any{"command": "echo hi; exit 2", "description": "say hi"},
			wantExit:   2,
			wantOutput: "hi\n",
		},
		{name: "workdir", args: map[string]any{"command": "ls", "workdir": "sub"}, wantOutput: "f\n"},
		{
			name:         "timeout",
			args:         map[string]any{"command": "sleep 5", "timeout": 200},
			wantTimedOut: true,
			wantContains: "timed out after 200ms",
		},
		{
			name:          "long output keeps the end",
			args:          map[string]any{"command": "seq 1 20000; echo END"},
			wantTruncated: true,
			wantSuffix:    "END\n",
		},
		{name: "workdir escapes root", args: map[string]any{"command": "true", "workdir": "../.."}, wantErr: "outside"},
		{name: "missing workdir", args: map[string]any{"command": "true", "workdir": "missing"}, wantErr: "not found"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			e := setup(t, map[string]string{"sub/f": ""})
			var out shellOut
			if !e.run(t, "shell", tt.args, tt.wantErr, &out) {
				return
			}
			if !tt.wantTimedOut {
				assert.Equal(t, tt.wantExit, out.ExitCode)
			}
			assert.Equal(t, tt.wantTimedOut, out.TimedOut)
			assert.Equal(t, tt.wantTruncated, out.Truncated)
			if tt.wantOutput != "" {
				assert.Equal(t, tt.wantOutput, out.Output)
			}
			if tt.wantContains != "" {
				assert.Contains(t, out.Output, tt.wantContains)
			}
			if tt.wantSuffix != "" {
				assert.True(t, strings.HasSuffix(out.Output, tt.wantSuffix), "output ends %q", out.Output[max(0, len(out.Output)-50):])
			}
		})
	}
}

func TestShellRunsInTheWorkspaceRoot(t *testing.T) {
	t.Parallel()
	e := setup(t, nil)
	var out struct{}
	e.run(t, "shell", map[string]any{"command": "touch made"}, "", &out)
	_, err := os.Stat(filepath.Join(e.ws.Root(), "made"))
	require.NoError(t, err)
}
