package workspace

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/bmatcuk/doublestar/v4"
)

// Local is a Workspace rooted at a directory on this machine.
type Local struct {
	root string // absolute, symlinks resolved
}

// NewLocal returns a workspace rooted at root, creating it if missing.
func NewLocal(root string) (*Local, error) {
	if root == "" {
		return nil, errors.New("workspace root is empty")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("workspace root: %w", err)
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return nil, fmt.Errorf("create workspace root: %w", err)
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, fmt.Errorf("resolve workspace root: %w", err)
	}
	return &Local{root: real}, nil
}

// Root returns the workspace's absolute root directory.
func (l *Local) Root() string { return l.root }

// resolve maps a workspace path to an absolute path on disk, rejecting any
// path that lands outside the root lexically or through a symlink.
func (l *Local) resolve(path string) (string, error) {
	var abs string
	if filepath.IsAbs(path) {
		abs = filepath.Clean(path)
	} else {
		abs = filepath.Join(l.root, path)
	}
	// Find the deepest ancestor that exists and resolve its symlinks; the
	// missing remainder can't contain symlinks or "..", as Clean removed them.
	existing, rest := abs, ""
	for {
		_, err := os.Lstat(existing)
		if err == nil {
			break
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return "", fmt.Errorf("%s: %w", path, err)
		}
		parent := filepath.Dir(existing)
		if parent == existing {
			return "", fmt.Errorf("%s: %w", path, ErrOutside)
		}
		rest = filepath.Join(filepath.Base(existing), rest)
		existing = parent
	}
	real, err := filepath.EvalSymlinks(existing)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			// A dangling symlink: following it on write could land anywhere.
			return "", fmt.Errorf("%s: dangling symlink: %w", path, ErrOutside)
		}
		return "", fmt.Errorf("%s: %w", path, err)
	}
	real = filepath.Join(real, rest)
	if !l.inside(real) {
		return "", fmt.Errorf("%s: %w", path, ErrOutside)
	}
	return real, nil
}

func (l *Local) inside(p string) bool {
	rel, err := filepath.Rel(l.root, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// rel returns p relative to the root with "/" separators.
func (l *Local) rel(p string) string {
	r, err := filepath.Rel(l.root, p)
	if err != nil {
		return filepath.ToSlash(p)
	}
	return filepath.ToSlash(r)
}

func (l *Local) entry(p string, fi fs.FileInfo) Entry {
	return Entry{Path: l.rel(p), IsDir: fi.IsDir(), Size: fi.Size(), ModTime: fi.ModTime()}
}

func (l *Local) Read(_ context.Context, path string) ([]byte, error) {
	p, err := l.resolve(path)
	if err != nil {
		return nil, err
	}
	fi, err := os.Stat(p)
	if err != nil {
		return nil, fmt.Errorf("read: %w", err)
	}
	if fi.IsDir() {
		return nil, fmt.Errorf("%s is a directory", path)
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return nil, fmt.Errorf("read: %w", err)
	}
	return data, nil
}

func (l *Local) Write(_ context.Context, path string, data []byte) error {
	p, err := l.resolve(path)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return fmt.Errorf("write: %w", err)
	}
	if err := os.WriteFile(p, data, 0o644); err != nil {
		return fmt.Errorf("write: %w", err)
	}
	return nil
}

func (l *Local) Stat(_ context.Context, path string) (Entry, error) {
	p, err := l.resolve(path)
	if err != nil {
		return Entry{}, err
	}
	fi, err := os.Stat(p)
	if err != nil {
		return Entry{}, fmt.Errorf("stat: %w", err)
	}
	return l.entry(p, fi), nil
}

func (l *Local) List(_ context.Context, path string) ([]Entry, error) {
	p, err := l.resolve(path)
	if err != nil {
		return nil, err
	}
	des, err := os.ReadDir(p) // sorted by name
	if err != nil {
		return nil, fmt.Errorf("list: %w", err)
	}
	out := make([]Entry, 0, len(des))
	for _, de := range des {
		fi, err := de.Info()
		if err != nil {
			continue // removed meanwhile
		}
		out = append(out, l.entry(filepath.Join(p, de.Name()), fi))
	}
	return out, nil
}

// skipName reports whether a walk should skip an entry by name.
func skipName(name string, hidden bool) bool {
	return name == ".git" || (!hidden && strings.HasPrefix(name, ".") && name != "." && name != "..")
}

func limit(n int) int {
	if n <= 0 {
		return DefaultLimit
	}
	return n
}

func (l *Local) Glob(ctx context.Context, req GlobRequest) ([]string, error) {
	if req.Pattern == "" {
		return nil, errors.New("pattern is empty")
	}
	pattern := strings.TrimPrefix(filepath.ToSlash(req.Pattern), "./")
	if !doublestar.ValidatePattern(pattern) {
		return nil, fmt.Errorf("invalid glob pattern %q", req.Pattern)
	}
	baseOnly := !strings.Contains(pattern, "/")
	dir, err := l.resolve(req.Dir)
	if err != nil {
		return nil, err
	}
	type hit struct {
		path string
		mod  time.Time
	}
	var hits []hit
	err = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			// The walk's own root must be readable; below it, an unreadable
			// entry is skipped so one bad directory doesn't hide the rest.
			if p == dir {
				return err
			}
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if p != dir && skipName(d.Name(), req.Hidden) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(dir, p)
		subject := filepath.ToSlash(rel)
		if baseOnly {
			subject = d.Name()
		}
		if ok, _ := doublestar.Match(pattern, subject); !ok {
			return nil
		}
		if mod, ok := modTime(d); ok {
			hits = append(hits, hit{l.rel(p), mod})
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("glob: %w", err)
	}
	slices.SortStableFunc(hits, func(a, b hit) int {
		if c := b.mod.Compare(a.mod); c != 0 {
			return c
		}
		return strings.Compare(a.path, b.path)
	})
	out := make([]string, 0, min(len(hits), limit(req.Limit)))
	for _, h := range hits[:min(len(hits), limit(req.Limit))] {
		out = append(out, h.path)
	}
	return out, nil
}

// modTime returns an entry's modification time. ok is false if the entry
// can no longer be described, such as when it was removed during a walk;
// callers skip such entries.
func modTime(d fs.DirEntry) (time.Time, bool) {
	fi, err := d.Info()
	if err != nil {
		return time.Time{}, false
	}
	return fi.ModTime(), true
}

func (l *Local) Grep(ctx context.Context, req GrepRequest) ([]GrepMatch, error) {
	if req.Pattern == "" {
		return nil, errors.New("pattern is empty")
	}
	expr := req.Pattern
	if req.Literal {
		expr = regexp.QuoteMeta(expr)
	}
	if req.IgnoreCase {
		expr = "(?i)" + expr
	}
	re, err := regexp.Compile(expr)
	if err != nil {
		return nil, fmt.Errorf("invalid regular expression: %w", err)
	}
	include := filepath.ToSlash(req.Include)
	if include != "" && !doublestar.ValidatePattern(include) {
		return nil, fmt.Errorf("invalid include pattern %q", req.Include)
	}
	root, err := l.resolve(req.Path)
	if err != nil {
		return nil, err
	}
	fi, err := os.Stat(root)
	if err != nil {
		return nil, fmt.Errorf("grep: %w", err)
	}
	max := limit(req.Limit)
	var out []GrepMatch
	errFull := errors.New("full")

	// search scans one file's text; data is nil for a binary or oversized
	// file, which is skipped.
	search := func(p string, data []byte) error {
		line := 0
		for len(data) > 0 {
			line++
			var text []byte
			if i := bytes.IndexByte(data, '\n'); i >= 0 {
				text, data = data[:i], data[i+1:]
			} else {
				text, data = data, nil
			}
			text = bytes.TrimSuffix(text, []byte("\r"))
			if !re.Match(text) {
				continue
			}
			out = append(out, GrepMatch{Path: l.rel(p), Line: line, Text: cutLine(string(text))})
			if len(out) >= max {
				return errFull
			}
		}
		return nil
	}

	if !fi.IsDir() {
		// A file named explicitly must be readable.
		data, err := readText(root)
		if err != nil {
			return nil, fmt.Errorf("grep: %w", err)
		}
		if err := search(root, data); err != nil && !errors.Is(err, errFull) {
			return nil, err
		}
		return out, nil
	}
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			// As in Glob: only the root must be readable.
			if p == root {
				return err
			}
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if p != root && skipName(d.Name(), req.Hidden) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil // dirs, symlinks, devices
		}
		if include != "" {
			subject := d.Name()
			if strings.Contains(include, "/") {
				rel, _ := filepath.Rel(root, p)
				subject = filepath.ToSlash(rel)
			}
			if ok, _ := doublestar.Match(include, subject); !ok {
				return nil
			}
		}
		data, err := readText(p)
		if err != nil {
			// Skipped like an unreadable directory entry: it may have been
			// removed or be unreadable, and the rest of the tree still counts.
			return nil //nolint:nilerr // deliberately skips unreadable files
		}
		return search(p, data)
	})
	if err != nil && !errors.Is(err, errFull) {
		return nil, fmt.Errorf("grep: %w", err)
	}
	return out, nil
}

// readText returns a file's contents, or nil if it is too big or binary.
func readText(p string) ([]byte, error) {
	fi, err := os.Stat(p)
	if err != nil {
		return nil, fmt.Errorf("stat: %w", err)
	}
	if !fi.Mode().IsRegular() || fi.Size() > MaxGrepFileSize {
		return nil, nil
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return nil, fmt.Errorf("read: %w", err)
	}
	if bytes.IndexByte(data[:min(len(data), 8192)], 0) >= 0 {
		return nil, nil
	}
	return data, nil
}

func cutLine(s string) string {
	if len(s) <= MaxGrepLine {
		return s
	}
	s = s[:MaxGrepLine]
	for len(s) > 0 && !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s + "..."
}

// execEnv is the only environment commands get: the parent's environment
// holds secrets.
var execEnv = []string{"PATH", "HOME", "USER", "LANG", "TMPDIR", "TERM"}

func (l *Local) Exec(ctx context.Context, req ExecRequest) (ExecResult, error) {
	if strings.TrimSpace(req.Command) == "" {
		return ExecResult{}, errors.New("command is empty")
	}
	dir, err := l.resolve(req.Dir)
	if err != nil {
		return ExecResult{}, err
	}
	if fi, err := os.Stat(dir); err != nil {
		return ExecResult{}, fmt.Errorf("exec: %w", err)
	} else if !fi.IsDir() {
		return ExecResult{}, fmt.Errorf("%s is not a directory", req.Dir)
	}
	timeout := req.Timeout
	if timeout <= 0 {
		timeout = DefaultExecTimeout
	}
	timeout = min(timeout, MaxExecTimeout)

	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(runCtx, "sh", "-c", req.Command)
	cmd.Dir = dir
	cmd.Stdin = nil
	for _, k := range execEnv {
		if v, ok := os.LookupEnv(k); ok {
			cmd.Env = append(cmd.Env, k+"="+v)
		}
	}
	if cmd.Env == nil {
		cmd.Env = []string{} // never inherit
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		// Kill the whole process group, not just sh.
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	// Don't wait forever on background children that keep the pipes open.
	cmd.WaitDelay = 2 * time.Second
	out := &tailBuffer{max: MaxExecOutput}
	cmd.Stdout = out
	cmd.Stderr = out

	err = cmd.Run()
	res := ExecResult{ExitCode: -1}
	if cmd.ProcessState != nil {
		res.ExitCode = cmd.ProcessState.ExitCode()
	}
	if ctx.Err() != nil {
		return ExecResult{}, fmt.Errorf("exec: %w", ctx.Err())
	}
	if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
		res.TimedOut = true
		res.ExitCode = -1
	}
	var exitErr *exec.ExitError
	if err != nil && !errors.As(err, &exitErr) && !errors.Is(err, exec.ErrWaitDelay) && !res.TimedOut {
		return ExecResult{}, fmt.Errorf("run command: %w", err)
	}
	res.Output, res.Truncated = out.String()
	return res, nil
}

// tailBuffer keeps the last max bytes written to it.
type tailBuffer struct {
	mu      sync.Mutex
	max     int
	buf     []byte
	dropped int
}

func (b *tailBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf = append(b.buf, p...)
	if over := len(b.buf) - b.max; over > b.max {
		b.dropped += over
		b.buf = append(b.buf[:0], b.buf[over:]...)
	}
	return len(p), nil
}

// String returns the kept output and whether anything was dropped.
func (b *tailBuffer) String() (string, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	buf, dropped := b.buf, b.dropped
	if over := len(buf) - b.max; over > 0 {
		buf, dropped = buf[over:], dropped+over
	}
	if dropped == 0 {
		return string(buf), false
	}
	// Don't start mid-rune.
	for len(buf) > 0 && !utf8.RuneStart(buf[0]) {
		buf, dropped = buf[1:], dropped+1
	}
	return fmt.Sprintf("...[output truncated: the first %d bytes were dropped]\n", dropped) + string(buf), true
}
