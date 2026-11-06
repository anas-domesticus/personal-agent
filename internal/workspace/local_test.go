package workspace_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/anas-domesticus/personal-agent/internal/workspace"
)

func newLocal(t *testing.T) (*workspace.Local, string) {
	t.Helper()
	dir := t.TempDir()
	ws, err := workspace.NewLocal(filepath.Join(dir, "root"))
	if err != nil {
		t.Fatal(err)
	}
	return ws, ws.Root()
}

func write(t *testing.T, ws workspace.Workspace, path, content string) {
	t.Helper()
	if err := ws.Write(context.Background(), path, []byte(content)); err != nil {
		t.Fatal(err)
	}
}

func TestNewLocalCreatesRoot(t *testing.T) {
	ws, root := newLocal(t)
	if fi, err := os.Stat(root); err != nil || !fi.IsDir() {
		t.Fatalf("root not created: %v", err)
	}
	real, _ := filepath.EvalSymlinks(root)
	if real != ws.Root() {
		t.Fatalf("root not resolved: %s vs %s", ws.Root(), real)
	}
}

func TestReadWriteStatList(t *testing.T) {
	ctx := context.Background()
	ws, root := newLocal(t)
	write(t, ws, "a/b/c.txt", "hello")
	got, err := ws.Read(ctx, "a/b/c.txt")
	if err != nil || string(got) != "hello" {
		t.Fatalf("read = %q, %v", got, err)
	}
	// Absolute paths inside the root are fine.
	got, err = ws.Read(ctx, filepath.Join(root, "a/b/c.txt"))
	if err != nil || string(got) != "hello" {
		t.Fatalf("absolute read = %q, %v", got, err)
	}
	write(t, ws, "a/b/c.txt", "bye")
	got, _ = ws.Read(ctx, "a/b/c.txt")
	if string(got) != "bye" {
		t.Fatalf("overwrite = %q", got)
	}

	e, err := ws.Stat(ctx, "a/b/c.txt")
	if err != nil || e.Path != "a/b/c.txt" || e.IsDir || e.Size != 3 || e.ModTime.IsZero() {
		t.Fatalf("stat = %+v, %v", e, err)
	}
	e, err = ws.Stat(ctx, "a")
	if err != nil || !e.IsDir || e.Path != "a" {
		t.Fatalf("stat dir = %+v, %v", e, err)
	}
	if _, err := ws.Stat(ctx, "missing"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stat missing: %v", err)
	}
	if _, err := ws.Read(ctx, "a"); err == nil {
		t.Fatal("reading a directory should fail")
	}

	write(t, ws, "a/z.txt", "")
	write(t, ws, "a/m.txt", "")
	entries, err := ws.List(ctx, "a")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Path)
	}
	if !slices.Equal(names, []string{"a/b", "a/m.txt", "a/z.txt"}) || !entries[0].IsDir {
		t.Fatalf("list = %v", entries)
	}
	if entries, err := ws.List(ctx, ""); err != nil || len(entries) != 1 || entries[0].Path != "a" {
		t.Fatalf("list root = %v, %v", entries, err)
	}
}

func TestConfinement(t *testing.T) {
	ctx := context.Background()
	ws, root := newLocal(t)
	outside := filepath.Dir(root)
	if err := os.WriteFile(filepath.Join(outside, "secret"), []byte("s"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "secret"), filepath.Join(root, "filelink")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "nothing-here"), filepath.Join(root, "dangling")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("sub", filepath.Join(root, "innerlink")); err != nil {
		t.Fatal(err)
	}

	bad := []string{
		"../secret",
		"a/../../secret",
		filepath.Join(outside, "secret"),
		"/etc/passwd",
		"link/secret",
		"link/new/file.txt",
		"filelink",
		"dangling",
	}
	for _, p := range bad {
		if _, err := ws.Read(ctx, p); !errors.Is(err, workspace.ErrOutside) {
			t.Errorf("read %q: %v", p, err)
		}
		if err := ws.Write(ctx, p, []byte("x")); !errors.Is(err, workspace.ErrOutside) {
			t.Errorf("write %q: %v", p, err)
		}
		if _, err := ws.Stat(ctx, p); !errors.Is(err, workspace.ErrOutside) {
			t.Errorf("stat %q: %v", p, err)
		}
	}
	if _, err := os.Stat(filepath.Join(outside, "nothing-here")); err == nil {
		t.Fatal("write through dangling symlink escaped")
	}
	if _, err := ws.List(ctx, "link"); !errors.Is(err, workspace.ErrOutside) {
		t.Errorf("list link: %v", err)
	}
	if _, err := ws.Glob(ctx, workspace.GlobRequest{Dir: "..", Pattern: "*"}); !errors.Is(err, workspace.ErrOutside) {
		t.Errorf("glob: %v", err)
	}
	if _, err := ws.Grep(ctx, workspace.GrepRequest{Path: "link", Pattern: "s"}); !errors.Is(err, workspace.ErrOutside) {
		t.Errorf("grep: %v", err)
	}
	if _, err := ws.Exec(ctx, workspace.ExecRequest{Command: "true", Dir: ".."}); !errors.Is(err, workspace.ErrOutside) {
		t.Errorf("exec: %v", err)
	}

	// Symlinks that stay inside are fine; ".." that stays inside is fine.
	write(t, ws, "innerlink/x.txt", "ok")
	if got, err := ws.Read(ctx, "sub/x.txt"); err != nil || string(got) != "ok" {
		t.Fatalf("inner symlink: %q, %v", got, err)
	}
	if got, err := ws.Read(ctx, "sub/../sub/x.txt"); err != nil || string(got) != "ok" {
		t.Fatalf("inner dotdot: %q, %v", got, err)
	}
}

func TestGlob(t *testing.T) {
	ctx := context.Background()
	ws, root := newLocal(t)
	write(t, ws, "main.go", "")
	write(t, ws, "pkg/a.go", "")
	write(t, ws, "pkg/deep/b.go", "")
	write(t, ws, "pkg/deep/c.txt", "")
	write(t, ws, ".hidden/h.go", "")
	write(t, ws, ".git/config.go", "")
	old := time.Now().Add(-time.Hour)
	_ = os.Chtimes(filepath.Join(root, "main.go"), old, old)

	got, err := ws.Glob(ctx, workspace.GlobRequest{Pattern: "**/*.go"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[len(got)-1] != "main.go" {
		t.Fatalf("**/*.go = %v (want 3, oldest last)", got)
	}
	got, _ = ws.Glob(ctx, workspace.GlobRequest{Pattern: "*.go"})
	if len(got) != 3 {
		t.Fatalf("*.go should match at any depth: %v", got)
	}
	got, _ = ws.Glob(ctx, workspace.GlobRequest{Pattern: "deep/*", Dir: "pkg"})
	slices.Sort(got)
	if !slices.Equal(got, []string{"pkg/deep/b.go", "pkg/deep/c.txt"}) {
		t.Fatalf("dir glob = %v", got)
	}
	got, _ = ws.Glob(ctx, workspace.GlobRequest{Pattern: "**/*.go", Hidden: true})
	if len(got) != 4 || slices.Contains(got, ".git/config.go") {
		t.Fatalf("hidden glob = %v", got)
	}
	got, _ = ws.Glob(ctx, workspace.GlobRequest{Pattern: "**/*", Limit: 2})
	if len(got) != 2 {
		t.Fatalf("limit = %v", got)
	}
	got, _ = ws.Glob(ctx, workspace.GlobRequest{Pattern: "*.{go,txt}"})
	if len(got) != 4 {
		t.Fatalf("braces = %v", got)
	}
}

func TestGrep(t *testing.T) {
	ctx := context.Background()
	ws, _ := newLocal(t)
	write(t, ws, "a.go", "package a\nfunc Hello() {}\n// hello world\n")
	write(t, ws, "b.ts", "const hello = 1;\nHELLO.x(a+b)\n")
	write(t, ws, "c.tsx", "hello\r\n")
	write(t, ws, "bin.dat", "hello\x00world")
	write(t, ws, "big.txt", strings.Repeat("hello\n", workspace.MaxGrepFileSize/6+10))
	write(t, ws, ".hidden/h.go", "hello")

	paths := func(ms []workspace.GrepMatch) []string {
		var out []string
		for _, m := range ms {
			out = append(out, m.Path+":"+string(rune('0'+m.Line)))
		}
		return out
	}

	ms, err := ws.Grep(ctx, workspace.GrepRequest{Pattern: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if got := paths(ms); !slices.Equal(got, []string{"a.go:3", "b.ts:1", "c.tsx:1"}) {
		t.Fatalf("grep = %v", got)
	}
	if ms[2].Text != "hello" {
		t.Fatalf("CR not trimmed: %q", ms[2].Text)
	}
	ms, _ = ws.Grep(ctx, workspace.GrepRequest{Pattern: "hello", IgnoreCase: true})
	if len(ms) != 5 {
		t.Fatalf("ignore case = %v", paths(ms))
	}
	ms, _ = ws.Grep(ctx, workspace.GrepRequest{Pattern: "func \\w+\\(", Include: "*.go"})
	if len(ms) != 1 || ms[0].Line != 2 {
		t.Fatalf("regex = %v", ms)
	}
	ms, _ = ws.Grep(ctx, workspace.GrepRequest{Pattern: "x(a+b)", Literal: true})
	if len(ms) != 1 || ms[0].Path != "b.ts" {
		t.Fatalf("literal = %v", ms)
	}
	if _, err := ws.Grep(ctx, workspace.GrepRequest{Pattern: "x(a+b"}); err == nil {
		t.Fatal("bad regex should fail")
	}
	ms, _ = ws.Grep(ctx, workspace.GrepRequest{Pattern: "hello", Include: "*.{ts,tsx}"})
	if got := paths(ms); !slices.Equal(got, []string{"b.ts:1", "c.tsx:1"}) {
		t.Fatalf("include = %v", got)
	}
	ms, _ = ws.Grep(ctx, workspace.GrepRequest{Pattern: "hello", Limit: 2})
	if len(ms) != 2 {
		t.Fatalf("limit = %v", ms)
	}
	ms, _ = ws.Grep(ctx, workspace.GrepRequest{Pattern: "hello", Hidden: true})
	if len(ms) != 4 {
		t.Fatalf("hidden = %v", paths(ms))
	}
	ms, _ = ws.Grep(ctx, workspace.GrepRequest{Pattern: "hello", Path: "a.go"})
	if len(ms) != 1 || ms[0].Path != "a.go" {
		t.Fatalf("single file = %v", ms)
	}
}

func TestExec(t *testing.T) {
	ctx := context.Background()
	ws, root := newLocal(t)
	t.Setenv("PERSONAL_AGENT_TEST_SECRET", "hunter2")

	res, err := ws.Exec(ctx, workspace.ExecRequest{Command: "echo out; echo err >&2; exit 3"})
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != 3 || !strings.Contains(res.Output, "out") || !strings.Contains(res.Output, "err") || res.TimedOut {
		t.Fatalf("res = %+v", res)
	}

	res, _ = ws.Exec(ctx, workspace.ExecRequest{Command: "env"})
	if strings.Contains(res.Output, "hunter2") || !strings.Contains(res.Output, "PATH=") {
		t.Fatalf("env leaked or missing PATH: %s", res.Output)
	}

	write(t, ws, "sub/f.txt", "")
	res, _ = ws.Exec(ctx, workspace.ExecRequest{Command: "pwd; ls", Dir: "sub"})
	if !strings.Contains(res.Output, filepath.Join(root, "sub")) || !strings.Contains(res.Output, "f.txt") {
		t.Fatalf("workdir = %q", res.Output)
	}
	res, _ = ws.Exec(ctx, workspace.ExecRequest{Command: "pwd"})
	if strings.TrimSpace(res.Output) != root {
		t.Fatalf("default dir = %q", res.Output)
	}

	res, _ = ws.Exec(ctx, workspace.ExecRequest{Command: "head -c 200000 /dev/zero | tr '\\0' a; echo END"})
	if !res.Truncated || !strings.HasSuffix(res.Output, "END\n") || len(res.Output) > workspace.MaxExecOutput+200 {
		t.Fatalf("truncation: %v, %d bytes", res.Truncated, len(res.Output))
	}
}

func TestExecTimeoutKillsChildren(t *testing.T) {
	ctx := context.Background()
	ws, root := newLocal(t)
	start := time.Now()
	// The background child would touch a file after 2s if it survived.
	res, err := ws.Exec(ctx, workspace.ExecRequest{
		Command: "(sleep 2; touch survived) & echo started; sleep 30",
		Timeout: 300 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.TimedOut || res.ExitCode != -1 || !strings.Contains(res.Output, "started") {
		t.Fatalf("res = %+v", res)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatalf("took %v", time.Since(start))
	}
	time.Sleep(2500 * time.Millisecond)
	if _, err := os.Stat(filepath.Join(root, "survived")); err == nil {
		t.Fatal("child process survived the timeout")
	}
}

func TestExecCancel(t *testing.T) {
	ws, _ := newLocal(t)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if _, err := ws.Exec(ctx, workspace.ExecRequest{Command: "sleep 30"}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v", err)
	}
}

func TestSandboxUnimplemented(t *testing.T) {
	ctx := context.Background()
	var ws workspace.Workspace = workspace.Sandbox{}
	errs := []error{}
	_, err := ws.Read(ctx, "x")
	errs = append(errs, err, ws.Write(ctx, "x", nil))
	_, err = ws.Stat(ctx, "x")
	errs = append(errs, err)
	_, err = ws.List(ctx, "x")
	errs = append(errs, err)
	_, err = ws.Glob(ctx, workspace.GlobRequest{})
	errs = append(errs, err)
	_, err = ws.Grep(ctx, workspace.GrepRequest{})
	errs = append(errs, err)
	_, err = ws.Exec(ctx, workspace.ExecRequest{})
	errs = append(errs, err)
	for i, err := range errs {
		if !errors.Is(err, workspace.ErrUnimplemented) {
			t.Errorf("method %d: %v", i, err)
		}
	}
}
