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

	"github.com/anas-domesticus/personal-agent/internal/tools/files"
	"github.com/anas-domesticus/personal-agent/internal/workspace"
)

type env struct {
	t     *testing.T
	ws    *workspace.Local
	tools map[string]tool.InvokableTool
}

func setup(t *testing.T) *env {
	t.Helper()
	ws, err := workspace.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	specs, err := files.Specs(ws)
	if err != nil {
		t.Fatal(err)
	}
	e := &env{t: t, ws: ws, tools: map[string]tool.InvokableTool{}}
	for _, s := range specs {
		if s.Summary == "" || s.Settings != nil {
			t.Fatalf("spec %s: bad summary/settings", s.Name)
		}
		tl, err := s.New(nil)
		if err != nil {
			t.Fatal(err)
		}
		e.tools[s.Name] = tl
	}
	return e
}

type result struct {
	OK    bool            `json:"ok"`
	Error string          `json:"error"`
	Data  json.RawMessage `json:"data"`
}

// call runs a tool and returns its result.
func (e *env) call(name string, args any) result {
	e.t.Helper()
	raw, err := json.Marshal(args)
	if err != nil {
		e.t.Fatal(err)
	}
	out, err := e.tools[name].InvokableRun(context.Background(), string(raw))
	if err != nil {
		e.t.Fatalf("%s: %v", name, err)
	}
	var r result
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		e.t.Fatalf("%s: bad result %q: %v", name, out, err)
	}
	return r
}

// ok runs a tool, fails on an error result, and decodes the data into v.
func (e *env) ok(name string, args any, v any) {
	e.t.Helper()
	r := e.call(name, args)
	if !r.OK {
		e.t.Fatalf("%s(%v) failed: %s", name, args, r.Error)
	}
	if err := json.Unmarshal(r.Data, v); err != nil {
		e.t.Fatal(err)
	}
}

// fail runs a tool and expects an error result containing want.
func (e *env) fail(name string, args any, want string) {
	e.t.Helper()
	r := e.call(name, args)
	if r.OK || !strings.Contains(r.Error, want) {
		e.t.Fatalf("%s(%v): want error containing %q, got ok=%v error=%q", name, args, want, r.OK, r.Error)
	}
}

func (e *env) file(path string) string {
	e.t.Helper()
	data, err := e.ws.Read(context.Background(), path)
	if err != nil {
		e.t.Fatal(err)
	}
	return string(data)
}

func (e *env) put(path, content string) {
	e.t.Helper()
	if err := e.ws.Write(context.Background(), path, []byte(content)); err != nil {
		e.t.Fatal(err)
	}
}

func TestSpecs(t *testing.T) {
	e := setup(t)
	var names []string
	for n, tl := range e.tools {
		names = append(names, n)
		info, err := tl.Info(context.Background())
		if err != nil || info.Desc == "" || info.ParamsOneOf == nil {
			t.Fatalf("%s info: %+v %v", n, info, err)
		}
	}
	slices.Sort(names)
	if !slices.Equal(names, []string{"edit", "glob", "grep", "read", "shell", "write"}) {
		t.Fatalf("names = %v", names)
	}
	// Required fields come from non-omitempty tags.
	info, _ := e.tools["edit"].Info(context.Background())
	schema, err := info.ParamsOneOf.ToJSONSchema()
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(schema.Required, []string{"path", "old_string", "new_string"}) {
		t.Fatalf("edit required = %v", schema.Required)
	}
}

func TestRead(t *testing.T) {
	e := setup(t)
	var lines []string
	for i := 1; i <= 2500; i++ {
		lines = append(lines, fmt.Sprintf("l%d", i))
	}
	e.put("big.txt", strings.Join(lines, "\n")+"\n")
	e.put("small.txt", "alpha\nbeta\r\ngamma")

	var out string
	e.ok("read", map[string]any{"path": "small.txt"}, &out)
	if !strings.HasPrefix(out, "     1\talpha\n     2\tbeta\n     3\tgamma\n") || !strings.Contains(out, "3 lines total") {
		t.Fatalf("small = %q", out)
	}
	e.ok("read", map[string]any{"path": "small.txt", "offset": 2, "limit": 1}, &out)
	if !strings.HasPrefix(out, "     2\tbeta\n") || !strings.Contains(out, "offset=3") {
		t.Fatalf("paged = %q", out)
	}
	e.ok("read", map[string]any{"path": "big.txt", "offset": 2400}, &out)
	if !strings.HasPrefix(out, "  2400\tl2400\n") || !strings.Contains(out, "  2500\tl2500\n") || !strings.Contains(out, "2500 lines total") {
		t.Fatalf("tail = %q", out[:min(len(out), 200)])
	}
	// From the start, output stops before the cap and says where to continue.
	e.ok("read", map[string]any{"path": "big.txt"}, &out)
	if !strings.Contains(out, "Use offset=") || strings.Contains(out, "truncated") {
		t.Fatalf("big = ...%q", out[max(0, len(out)-200):])
	}
	e.fail("read", map[string]any{"path": "big.txt", "offset": 3000}, "past the end")
	e.fail("read", map[string]any{"path": "missing.txt"}, "not found")
	e.fail("read", map[string]any{"path": "../x"}, "outside")

	e.put("long.txt", strings.Repeat("x", 3000))
	e.ok("read", map[string]any{"path": "long.txt"}, &out)
	if !strings.Contains(out, "(line cut)") || strings.Count(out, "x") != files.MaxLineLength {
		t.Fatalf("long line not cut: %d x", strings.Count(out, "x"))
	}

	e.put("bin", "a\x00b")
	e.fail("read", map[string]any{"path": "bin"}, "binary")

	e.put("dir/sub/x", "")
	e.put("dir/a.txt", "")
	e.ok("read", map[string]any{"path": "dir"}, &out)
	if !strings.Contains(out, "     1\ta.txt\n     2\tsub/\n") {
		t.Fatalf("dir = %q", out)
	}
}

func TestWrite(t *testing.T) {
	e := setup(t)
	var out struct {
		Created bool `json:"created"`
		Bytes   int  `json:"bytes"`
	}
	e.ok("write", map[string]any{"path": "a/b.txt", "content": "hi"}, &out)
	if !out.Created || out.Bytes != 2 || e.file("a/b.txt") != "hi" {
		t.Fatalf("create: %+v", out)
	}
	e.ok("write", map[string]any{"path": "a/b.txt", "content": "bye"}, &out)
	if out.Created || e.file("a/b.txt") != "bye" {
		t.Fatalf("overwrite: %+v", out)
	}
	e.fail("write", map[string]any{"path": "a", "content": "x"}, "directory")
	e.fail("write", map[string]any{"path": "/tmp/escape.txt", "content": "x"}, "outside")
}

func TestEdit(t *testing.T) {
	e := setup(t)
	e.put("f.go", "a := 1\nb := 1\nc := 2\n")
	var out struct {
		Replacements int  `json:"replacements"`
		Created      bool `json:"created"`
	}
	e.ok("edit", map[string]any{"path": "f.go", "old_string": "c := 2", "new_string": "c := 3"}, &out)
	if out.Replacements != 1 || e.file("f.go") != "a := 1\nb := 1\nc := 3\n" {
		t.Fatalf("edit: %+v %q", out, e.file("f.go"))
	}
	e.fail("edit", map[string]any{"path": "f.go", "old_string": ":= 1", "new_string": ":= 9"}, "matches 2 times")
	e.fail("edit", map[string]any{"path": "f.go", "old_string": "zzz", "new_string": "y"}, "not found")
	e.fail("edit", map[string]any{"path": "f.go", "old_string": "a", "new_string": "a"}, "same")
	e.ok("edit", map[string]any{"path": "f.go", "old_string": ":= 1", "new_string": ":= 9", "replace_all": true}, &out)
	if out.Replacements != 2 || e.file("f.go") != "a := 9\nb := 9\nc := 3\n" {
		t.Fatalf("replace_all: %+v %q", out, e.file("f.go"))
	}

	e.ok("edit", map[string]any{"path": "new/n.txt", "old_string": "", "new_string": "fresh"}, &out)
	if !out.Created || e.file("new/n.txt") != "fresh" {
		t.Fatalf("create: %+v", out)
	}
	e.fail("edit", map[string]any{"path": "new/n.txt", "old_string": "", "new_string": "again"}, "already exists")
	e.fail("edit", map[string]any{"path": "nope.txt", "old_string": "x", "new_string": "y"}, "not found")

	e.put("crlf.txt", "one\r\ntwo\r\nthree\r\n")
	e.ok("edit", map[string]any{"path": "crlf.txt", "old_string": "one\ntwo", "new_string": "1\n2"}, &out)
	if e.file("crlf.txt") != "1\r\n2\r\nthree\r\n" {
		t.Fatalf("crlf: %q", e.file("crlf.txt"))
	}
}

func TestGlob(t *testing.T) {
	e := setup(t)
	e.put("a.go", "")
	e.put("pkg/b.go", "")
	e.put("pkg/c.ts", "")
	e.put(".hid/d.go", "")
	var out struct {
		Files     []string `json:"files"`
		Truncated bool     `json:"truncated"`
	}
	e.ok("glob", map[string]any{"pattern": "**/*.go"}, &out)
	slices.Sort(out.Files)
	if !slices.Equal(out.Files, []string{"a.go", "pkg/b.go"}) {
		t.Fatalf("glob = %v", out.Files)
	}
	e.ok("glob", map[string]any{"pattern": "*", "path": "pkg"}, &out)
	if len(out.Files) != 2 {
		t.Fatalf("path = %v", out.Files)
	}
	e.ok("glob", map[string]any{"pattern": "*.go", "hidden": true}, &out)
	if len(out.Files) != 3 {
		t.Fatalf("hidden = %v", out.Files)
	}
	e.ok("glob", map[string]any{"pattern": "**/*", "limit": 2}, &out)
	if len(out.Files) != 2 || !out.Truncated {
		t.Fatalf("limit = %+v", out)
	}
	e.ok("glob", map[string]any{"pattern": "*.rs"}, &out)
	if out.Files == nil || len(out.Files) != 0 {
		t.Fatalf("empty = %+v", out)
	}
}

func TestGrep(t *testing.T) {
	e := setup(t)
	e.put("a.go", "package a\nfunc Foo() {}\nfunc bar() {}\n")
	e.put("b.ts", "function foo() {}\n")
	e.put("c.tsx", "Foo.x(1+1)\n")
	var out struct {
		Matches   int    `json:"matches"`
		Truncated bool   `json:"truncated"`
		Output    string `json:"output"`
	}
	e.ok("grep", map[string]any{"pattern": "Foo"}, &out)
	if out.Matches != 2 || out.Output != "a.go:2: func Foo() {}\n\nc.tsx:1: Foo.x(1+1)\n" {
		t.Fatalf("grep = %+v", out)
	}
	e.ok("grep", map[string]any{"pattern": "foo", "case_sensitive": false}, &out)
	if out.Matches != 3 {
		t.Fatalf("case = %+v", out)
	}
	e.ok("grep", map[string]any{"pattern": "foo", "case_sensitive": false, "include": "*.{ts,tsx}"}, &out)
	if out.Matches != 2 || strings.Contains(out.Output, "a.go") {
		t.Fatalf("include = %+v", out)
	}
	e.ok("grep", map[string]any{"pattern": "x(1+1)", "literal": true}, &out)
	if out.Matches != 1 {
		t.Fatalf("literal = %+v", out)
	}
	e.ok("grep", map[string]any{"pattern": `^func \w+\(`, "path": "a.go"}, &out)
	if out.Matches != 2 {
		t.Fatalf("regex = %+v", out)
	}
	e.ok("grep", map[string]any{"pattern": "func", "limit": 1}, &out)
	if out.Matches != 1 || !out.Truncated || !strings.Contains(out.Output, "first 1 matches") {
		t.Fatalf("limit = %+v", out)
	}
	e.ok("grep", map[string]any{"pattern": "nothing-here"}, &out)
	if out.Matches != 0 || out.Output != "No matches found." {
		t.Fatalf("none = %+v", out)
	}
	e.fail("grep", map[string]any{"pattern": "("}, "invalid regular expression")
}

func TestShell(t *testing.T) {
	e := setup(t)
	var out struct {
		ExitCode  int    `json:"exit_code"`
		Output    string `json:"output"`
		TimedOut  bool   `json:"timed_out"`
		Truncated bool   `json:"truncated"`
	}
	e.ok("shell", map[string]any{"command": "echo hi; exit 2", "description": "say hi"}, &out)
	if out.ExitCode != 2 || out.Output != "hi\n" {
		t.Fatalf("shell = %+v", out)
	}
	e.put("sub/f", "")
	e.ok("shell", map[string]any{"command": "ls", "workdir": "sub"}, &out)
	if out.Output != "f\n" {
		t.Fatalf("workdir = %+v", out)
	}
	e.ok("shell", map[string]any{"command": "sleep 5", "timeout": 200}, &out)
	if !out.TimedOut || !strings.Contains(out.Output, "timed out after 200ms") {
		t.Fatalf("timeout = %+v", out)
	}
	e.ok("shell", map[string]any{"command": "seq 1 20000; echo END"}, &out)
	if !out.Truncated || !strings.HasSuffix(out.Output, "END\n") {
		t.Fatalf("long output = %v ...%q", out.Truncated, out.Output[max(0, len(out.Output)-50):])
	}
	e.fail("shell", map[string]any{"command": "true", "workdir": "../.."}, "outside")
	e.fail("shell", map[string]any{"command": "true", "workdir": "missing"}, "not found")

	// The workspace root on disk is where the command ran.
	e.ok("shell", map[string]any{"command": "touch made"}, &out)
	if _, err := os.Stat(filepath.Join(e.ws.Root(), "made")); err != nil {
		t.Fatal(err)
	}
}
