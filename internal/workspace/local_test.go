package workspace_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/anas-domesticus/personal-agent/internal/workspace"
)

func newLocal(t *testing.T) (*workspace.Local, string) {
	t.Helper()
	ws, err := workspace.NewLocal(filepath.Join(t.TempDir(), "root"))
	require.NoError(t, err)
	return ws, ws.Root()
}

func write(t *testing.T, ws workspace.Workspace, path, content string) {
	t.Helper()
	require.NoError(t, ws.Write(context.Background(), path, []byte(content)))
}

func TestNewLocal(t *testing.T) {
	t.Parallel()

	t.Run("creates and resolves the root", func(t *testing.T) {
		t.Parallel()
		ws, root := newLocal(t)
		fi, err := os.Stat(root)
		require.NoError(t, err)
		assert.True(t, fi.IsDir())
		real, err := filepath.EvalSymlinks(root)
		require.NoError(t, err)
		assert.Equal(t, real, ws.Root())
	})

	t.Run("empty root", func(t *testing.T) {
		t.Parallel()
		_, err := workspace.NewLocal("")
		require.ErrorContains(t, err, "root is empty")
	})
}

func TestReadWrite(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	ws, root := newLocal(t)

	// The steps build on each other's files, so they run in order.
	write(t, ws, "a/b/c.txt", "hello")
	got, err := ws.Read(ctx, "a/b/c.txt")
	require.NoError(t, err)
	assert.Equal(t, "hello", string(got))

	// Absolute paths inside the root are fine.
	got, err = ws.Read(ctx, filepath.Join(root, "a/b/c.txt"))
	require.NoError(t, err)
	assert.Equal(t, "hello", string(got))

	write(t, ws, "a/b/c.txt", "bye")
	got, err = ws.Read(ctx, "a/b/c.txt")
	require.NoError(t, err)
	assert.Equal(t, "bye", string(got), "overwrite")

	_, err = ws.Read(ctx, "a")
	require.ErrorContains(t, err, "is a directory")
}

func TestStat(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	ws, _ := newLocal(t)
	write(t, ws, "a/b/c.txt", "bye")

	tests := []struct {
		name      string
		path      string
		wantPath  string
		wantDir   bool
		wantSize  int64
		wantErrIs error
	}{
		{name: "file", path: "a/b/c.txt", wantPath: "a/b/c.txt", wantSize: 3},
		{name: "directory", path: "a", wantPath: "a", wantDir: true},
		{name: "missing", path: "missing", wantErrIs: os.ErrNotExist},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			e, err := ws.Stat(ctx, tt.path)
			if tt.wantErrIs != nil {
				require.ErrorIs(t, err, tt.wantErrIs)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantPath, e.Path)
			assert.Equal(t, tt.wantDir, e.IsDir)
			if !tt.wantDir {
				assert.Equal(t, tt.wantSize, e.Size)
			}
			assert.False(t, e.ModTime.IsZero())
		})
	}
}

func TestList(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	ws, _ := newLocal(t)
	write(t, ws, "a/b/c.txt", "")
	write(t, ws, "a/z.txt", "")
	write(t, ws, "a/m.txt", "")

	tests := []struct {
		name      string
		path      string
		wantPaths []string
		wantDirs  []bool
	}{
		{name: "subdirectory sorted by name", path: "a",
			wantPaths: []string{"a/b", "a/m.txt", "a/z.txt"}, wantDirs: []bool{true, false, false}},
		{name: "root", path: "", wantPaths: []string{"a"}, wantDirs: []bool{true}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			entries, err := ws.List(ctx, tt.path)
			require.NoError(t, err)
			var paths []string
			var dirs []bool
			for _, e := range entries {
				paths = append(paths, e.Path)
				dirs = append(dirs, e.IsDir)
			}
			assert.Equal(t, tt.wantPaths, paths)
			assert.Equal(t, tt.wantDirs, dirs)
		})
	}
}

func TestConfinement(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	ws, root := newLocal(t)
	outside := filepath.Dir(root)
	require.NoError(t, os.WriteFile(filepath.Join(outside, "secret"), []byte("s"), 0o644))
	require.NoError(t, os.Symlink(outside, filepath.Join(root, "link")))
	require.NoError(t, os.Symlink(filepath.Join(outside, "secret"), filepath.Join(root, "filelink")))
	require.NoError(t, os.Symlink(filepath.Join(outside, "nothing-here"), filepath.Join(root, "dangling")))
	require.NoError(t, os.Mkdir(filepath.Join(root, "sub"), 0o755))
	require.NoError(t, os.Symlink("sub", filepath.Join(root, "innerlink")))

	t.Run("escaping paths", func(t *testing.T) {
		t.Parallel()
		tests := []struct {
			name string
			path string
		}{
			{name: "dotdot", path: "../secret"},
			{name: "dotdot below a subdirectory", path: "a/../../secret"},
			{name: "absolute path outside", path: filepath.Join(outside, "secret")},
			{name: "system file", path: "/etc/passwd"},
			{name: "through a directory symlink", path: "link/secret"},
			{name: "new file through a directory symlink", path: "link/new/file.txt"},
			{name: "file symlink", path: "filelink"},
			{name: "dangling symlink", path: "dangling"},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				_, err := ws.Read(ctx, tt.path)
				require.ErrorIs(t, err, workspace.ErrOutside, "read")
				err = ws.Write(ctx, tt.path, []byte("x"))
				require.ErrorIs(t, err, workspace.ErrOutside, "write")
				_, err = ws.Stat(ctx, tt.path)
				require.ErrorIs(t, err, workspace.ErrOutside, "stat")
			})
		}
	})
	// The group above has finished, so every write has been tried.
	_, err := os.Stat(filepath.Join(outside, "nothing-here"))
	require.ErrorIs(t, err, os.ErrNotExist, "write through dangling symlink escaped")

	t.Run("operations", func(t *testing.T) {
		t.Parallel()
		tests := []struct {
			name string
			call func() error
		}{
			{name: "list through a symlink", call: func() error {
				_, err := ws.List(ctx, "link")
				return err
			}},
			{name: "glob above the root", call: func() error {
				_, err := ws.Glob(ctx, workspace.GlobRequest{Dir: "..", Pattern: "*"})
				return err
			}},
			{name: "grep through a symlink", call: func() error {
				_, err := ws.Grep(ctx, workspace.GrepRequest{Path: "link", Pattern: "s"})
				return err
			}},
			{name: "exec above the root", call: func() error {
				_, err := ws.Exec(ctx, workspace.ExecRequest{Command: "true", Dir: ".."})
				return err
			}},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				require.ErrorIs(t, tt.call(), workspace.ErrOutside)
			})
		}
	})

	t.Run("paths that stay inside", func(t *testing.T) {
		t.Parallel()
		write(t, ws, "innerlink/x.txt", "ok")
		tests := []struct {
			name string
			path string
		}{
			{name: "through an inner symlink", path: "sub/x.txt"},
			{name: "dotdot that stays inside", path: "sub/../sub/x.txt"},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				got, err := ws.Read(ctx, tt.path)
				require.NoError(t, err)
				assert.Equal(t, "ok", string(got))
			})
		}
	})
}

func TestGlob(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	ws, root := newLocal(t)
	// Fixed modification times make the newest-first order deterministic;
	// ties are broken by path.
	base := time.Now().Truncate(time.Second)
	for _, p := range []string{"main.go", "pkg/a.go", "pkg/deep/b.go", "pkg/deep/c.txt", ".hidden/h.go", ".git/config.go"} {
		write(t, ws, p, "")
		mod := base
		if p == "main.go" {
			mod = base.Add(-time.Hour)
		}
		require.NoError(t, os.Chtimes(filepath.Join(root, p), mod, mod))
	}

	tests := []struct {
		name    string
		req     workspace.GlobRequest
		want    []string
		wantErr string
	}{
		{name: "recursive, oldest last", req: workspace.GlobRequest{Pattern: "**/*.go"},
			want: []string{"pkg/a.go", "pkg/deep/b.go", "main.go"}},
		{name: "base name pattern matches at any depth", req: workspace.GlobRequest{Pattern: "*.go"},
			want: []string{"pkg/a.go", "pkg/deep/b.go", "main.go"}},
		{name: "relative to a directory", req: workspace.GlobRequest{Pattern: "deep/*", Dir: "pkg"},
			want: []string{"pkg/deep/b.go", "pkg/deep/c.txt"}},
		{name: "hidden included but never .git", req: workspace.GlobRequest{Pattern: "**/*.go", Hidden: true},
			want: []string{".hidden/h.go", "pkg/a.go", "pkg/deep/b.go", "main.go"}},
		{name: "limit", req: workspace.GlobRequest{Pattern: "**/*", Limit: 2},
			want: []string{"pkg/a.go", "pkg/deep/b.go"}},
		{name: "braces", req: workspace.GlobRequest{Pattern: "*.{go,txt}"},
			want: []string{"pkg/a.go", "pkg/deep/b.go", "pkg/deep/c.txt", "main.go"}},
		{name: "empty pattern", req: workspace.GlobRequest{}, wantErr: "pattern is empty"},
		{name: "invalid pattern", req: workspace.GlobRequest{Pattern: "[a"}, wantErr: "invalid glob pattern"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := ws.Glob(ctx, tt.req)
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestGrep(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	ws, _ := newLocal(t)
	write(t, ws, "a.go", "package a\nfunc Hello() {}\n// hello world\n")
	write(t, ws, "b.ts", "const hello = 1;\nHELLO.x(a+b)\n")
	write(t, ws, "c.tsx", "hello\r\n")
	write(t, ws, "bin.dat", "hello\x00world")
	write(t, ws, "big.txt", strings.Repeat("hello\n", workspace.MaxGrepFileSize/6+10))
	write(t, ws, ".hidden/h.go", "hello")

	var (
		aHello  = workspace.GrepMatch{Path: "a.go", Line: 3, Text: "// hello world"}
		aFunc   = workspace.GrepMatch{Path: "a.go", Line: 2, Text: "func Hello() {}"}
		bHello  = workspace.GrepMatch{Path: "b.ts", Line: 1, Text: "const hello = 1;"}
		bUpper  = workspace.GrepMatch{Path: "b.ts", Line: 2, Text: "HELLO.x(a+b)"}
		cHello  = workspace.GrepMatch{Path: "c.tsx", Line: 1, Text: "hello"} // CR trimmed
		hHidden = workspace.GrepMatch{Path: ".hidden/h.go", Line: 1, Text: "hello"}
	)

	tests := []struct {
		name    string
		req     workspace.GrepRequest
		want    []workspace.GrepMatch
		wantErr string
	}{
		{name: "skips binary, big and hidden files", req: workspace.GrepRequest{Pattern: "hello"},
			want: []workspace.GrepMatch{aHello, bHello, cHello}},
		{name: "ignore case", req: workspace.GrepRequest{Pattern: "hello", IgnoreCase: true},
			want: []workspace.GrepMatch{aFunc, aHello, bHello, bUpper, cHello}},
		{name: "regular expression with include", req: workspace.GrepRequest{Pattern: `func \w+\(`, Include: "*.go"},
			want: []workspace.GrepMatch{aFunc}},
		{name: "literal", req: workspace.GrepRequest{Pattern: "x(a+b)", Literal: true},
			want: []workspace.GrepMatch{bUpper}},
		{name: "include with braces", req: workspace.GrepRequest{Pattern: "hello", Include: "*.{ts,tsx}"},
			want: []workspace.GrepMatch{bHello, cHello}},
		{name: "limit", req: workspace.GrepRequest{Pattern: "hello", Limit: 2},
			want: []workspace.GrepMatch{aHello, bHello}},
		{name: "hidden", req: workspace.GrepRequest{Pattern: "hello", Hidden: true},
			want: []workspace.GrepMatch{hHidden, aHello, bHello, cHello}},
		{name: "single file", req: workspace.GrepRequest{Pattern: "hello", Path: "a.go"},
			want: []workspace.GrepMatch{aHello}},
		{name: "bad regular expression", req: workspace.GrepRequest{Pattern: "x(a+b"}, wantErr: "invalid regular expression"},
		{name: "empty pattern", req: workspace.GrepRequest{}, wantErr: "pattern is empty"},
		{name: "invalid include", req: workspace.GrepRequest{Pattern: "x", Include: "[a"}, wantErr: "invalid include pattern"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := ws.Grep(ctx, tt.req)
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestGrepUnreadable(t *testing.T) {
	t.Parallel()
	if os.Geteuid() == 0 {
		t.Skip("root can read files without permission")
	}
	ctx := context.Background()
	ws, root := newLocal(t)
	write(t, ws, "ok.txt", "hello")
	write(t, ws, "locked.txt", "hello")
	require.NoError(t, os.Chmod(filepath.Join(root, "locked.txt"), 0))

	tests := []struct {
		name    string
		path    string
		want    []workspace.GrepMatch
		wantErr string
	}{
		{name: "walk skips an unreadable file", path: "",
			want: []workspace.GrepMatch{{Path: "ok.txt", Line: 1, Text: "hello"}}},
		{name: "an unreadable file named explicitly fails", path: "locked.txt", wantErr: "permission denied"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := ws.Grep(ctx, workspace.GrepRequest{Pattern: "hello", Path: tt.path})
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestExec(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	ws, root := newLocal(t)
	write(t, ws, "sub/f.txt", "")

	tests := []struct {
		name         string
		req          workspace.ExecRequest
		wantExitCode int
		wantContains []string
		wantOutput   string // exact, after trimming space; empty means unchecked
	}{
		{name: "non-zero exit with both streams", req: workspace.ExecRequest{Command: "echo out; echo err >&2; exit 3"},
			wantExitCode: 3, wantContains: []string{"out", "err"}},
		{name: "working directory", req: workspace.ExecRequest{Command: "pwd; ls", Dir: "sub"},
			wantContains: []string{filepath.Join(root, "sub"), "f.txt"}},
		{name: "defaults to the root", req: workspace.ExecRequest{Command: "pwd"}, wantOutput: root},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			res, err := ws.Exec(ctx, tt.req)
			require.NoError(t, err)
			assert.Equal(t, tt.wantExitCode, res.ExitCode)
			assert.False(t, res.TimedOut)
			for _, s := range tt.wantContains {
				assert.Contains(t, res.Output, s)
			}
			if tt.wantOutput != "" {
				assert.Equal(t, tt.wantOutput, strings.TrimSpace(res.Output))
			}
		})
	}

	t.Run("output keeps the tail", func(t *testing.T) {
		t.Parallel()
		res, err := ws.Exec(ctx, workspace.ExecRequest{Command: "head -c 200000 /dev/zero | tr '\\0' a; echo END"})
		require.NoError(t, err)
		assert.True(t, res.Truncated)
		assert.True(t, strings.HasSuffix(res.Output, "END\n"), "tail kept")
		assert.LessOrEqual(t, len(res.Output), workspace.MaxExecOutput+200)
	})

	t.Run("empty command", func(t *testing.T) {
		t.Parallel()
		_, err := ws.Exec(ctx, workspace.ExecRequest{Command: "  "})
		require.ErrorContains(t, err, "command is empty")
	})

	t.Run("directory is a file", func(t *testing.T) {
		t.Parallel()
		_, err := ws.Exec(ctx, workspace.ExecRequest{Command: "true", Dir: "sub/f.txt"})
		require.ErrorContains(t, err, "is not a directory")
	})
}

// TestExecEnv sets a process environment variable, so it can't run in
// parallel with other tests.
func TestExecEnv(t *testing.T) {
	ws, _ := newLocal(t)
	t.Setenv("PERSONAL_AGENT_TEST_SECRET", "hunter2")

	res, err := ws.Exec(context.Background(), workspace.ExecRequest{Command: "env"})
	require.NoError(t, err)
	assert.NotContains(t, res.Output, "hunter2", "environment leaked")
	assert.Contains(t, res.Output, "PATH=")
}

func TestExecTimeoutKillsChildren(t *testing.T) {
	t.Parallel()
	ws, root := newLocal(t)
	start := time.Now()
	// The background child would touch a file after 2s if it survived.
	res, err := ws.Exec(context.Background(), workspace.ExecRequest{
		Command: "(sleep 2; touch survived) & echo started; sleep 30",
		Timeout: 300 * time.Millisecond,
	})
	require.NoError(t, err)
	assert.True(t, res.TimedOut)
	assert.Equal(t, -1, res.ExitCode)
	assert.Contains(t, res.Output, "started")
	assert.Less(t, time.Since(start), 5*time.Second)

	survived := func() bool {
		_, err := os.Stat(filepath.Join(root, "survived"))
		return err == nil
	}
	assert.Never(t, survived, 2500*time.Millisecond, 100*time.Millisecond, "child process survived the timeout")
}

func TestExecCancel(t *testing.T) {
	t.Parallel()
	ws, _ := newLocal(t)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_, err := ws.Exec(ctx, workspace.ExecRequest{Command: "sleep 30"})
	require.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestSandboxUnimplemented(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	var ws workspace.Workspace = workspace.Sandbox{}

	tests := []struct {
		name string
		call func() error
	}{
		{name: "read", call: func() error { _, err := ws.Read(ctx, "x"); return err }},
		{name: "write", call: func() error { return ws.Write(ctx, "x", nil) }},
		{name: "stat", call: func() error { _, err := ws.Stat(ctx, "x"); return err }},
		{name: "list", call: func() error { _, err := ws.List(ctx, "x"); return err }},
		{name: "glob", call: func() error { _, err := ws.Glob(ctx, workspace.GlobRequest{}); return err }},
		{name: "grep", call: func() error { _, err := ws.Grep(ctx, workspace.GrepRequest{}); return err }},
		{name: "exec", call: func() error { _, err := ws.Exec(ctx, workspace.ExecRequest{}); return err }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.ErrorIs(t, tt.call(), workspace.ErrUnimplemented)
		})
	}
}
