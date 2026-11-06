// Package workspace is the layer every environment-affecting tool (files,
// shell) goes through. A Workspace is a set of operations rather than file
// handles so it can be implemented locally or over a remote sandbox API.
package workspace

import (
	"context"
	"errors"
	"time"
)

var (
	// ErrUnimplemented is returned by workspaces that do not support an
	// operation yet.
	ErrUnimplemented = errors.New("unimplemented")
	// ErrOutside is returned for a path that resolves outside the workspace,
	// whether through "..", an absolute path or a symlink.
	ErrOutside = errors.New("path is outside the workspace")
)

// Default limits, used when a request leaves them zero.
const (
	DefaultLimit       = 100
	DefaultExecTimeout = 2 * time.Minute
	MaxExecTimeout     = 10 * time.Minute
	// MaxExecOutput caps the combined output Exec returns, in bytes. The
	// tail is kept.
	MaxExecOutput = 50_000
	// MaxGrepFileSize is the largest file Grep searches.
	MaxGrepFileSize = 1 << 20
	// MaxGrepLine caps the length of a matched line's text.
	MaxGrepLine = 2000
)

// Workspace is the environment the agent's file and shell tools act on.
// Paths are relative to the workspace root; absolute paths are accepted only
// if they are inside it. Paths that resolve outside fail with ErrOutside.
type Workspace interface {
	// Read returns a file's contents.
	Read(ctx context.Context, path string) ([]byte, error)
	// Write creates or replaces a file, creating parent directories.
	Write(ctx context.Context, path string, data []byte) error
	// Stat describes one path.
	Stat(ctx context.Context, path string) (Entry, error)
	// List returns one directory level, sorted by name.
	List(ctx context.Context, path string) ([]Entry, error)
	// Glob returns the files matching a pattern, newest-modified first.
	Glob(ctx context.Context, req GlobRequest) ([]string, error)
	// Grep searches file contents line by line.
	Grep(ctx context.Context, req GrepRequest) ([]GrepMatch, error)
	// Exec runs a shell command. A non-zero exit is not an error; it is
	// reported in ExecResult.
	Exec(ctx context.Context, req ExecRequest) (ExecResult, error)
}

// Entry describes a file or directory.
type Entry struct {
	// Path is relative to the workspace root, with "/" separators.
	Path    string
	IsDir   bool
	Size    int64
	ModTime time.Time
}

// GlobRequest selects files by pattern.
type GlobRequest struct {
	// Dir is the directory to search; empty means the root.
	Dir string
	// Pattern is a doublestar glob relative to Dir ("**" matches any number
	// of directories). A pattern without "/" matches file names at any
	// depth, like ripgrep's --glob.
	Pattern string
	// Hidden includes dot-files and dot-directories (".git" is always
	// skipped).
	Hidden bool
	// Limit caps the results; zero means DefaultLimit.
	Limit int
}

// GrepRequest searches file contents.
type GrepRequest struct {
	// Path is a file or directory; empty means the root.
	Path string
	// Pattern is a Go regular expression, or a plain string if Literal.
	Pattern string
	Literal bool
	// IgnoreCase makes the match case-insensitive.
	IgnoreCase bool
	// Include is a doublestar glob that file names (or, if it contains "/",
	// paths relative to Path) must match, e.g. "*.{ts,tsx}".
	Include string
	// Hidden includes dot-files and dot-directories (".git" is always
	// skipped).
	Hidden bool
	// Limit caps the matches; zero means DefaultLimit.
	Limit int
}

// GrepMatch is one matching line.
type GrepMatch struct {
	// Path is relative to the workspace root.
	Path string
	// Line is 1-based.
	Line int
	Text string
}

// ExecRequest runs a command with "sh -c".
type ExecRequest struct {
	Command string
	// Dir is the working directory; empty means the root.
	Dir string
	// Timeout defaults to DefaultExecTimeout and is capped at
	// MaxExecTimeout.
	Timeout time.Duration
}

// ExecResult is a finished command.
type ExecResult struct {
	// Output is stdout and stderr combined, capped at MaxExecOutput bytes.
	Output string
	// ExitCode is the command's exit status, or -1 if it was killed.
	ExitCode int
	// TimedOut is set when the command was killed for exceeding its timeout.
	TimedOut bool
	// Truncated is set when the start of the output was dropped.
	Truncated bool
}

// Sandbox is a placeholder for a workspace backed by a remote sandbox. Every
// method returns ErrUnimplemented.
type Sandbox struct{}

var (
	_ Workspace = (*Local)(nil)
	_ Workspace = Sandbox{}
)

func (Sandbox) Read(context.Context, string) ([]byte, error)        { return nil, ErrUnimplemented }
func (Sandbox) Write(context.Context, string, []byte) error         { return ErrUnimplemented }
func (Sandbox) Stat(context.Context, string) (Entry, error)         { return Entry{}, ErrUnimplemented }
func (Sandbox) List(context.Context, string) ([]Entry, error)       { return nil, ErrUnimplemented }
func (Sandbox) Glob(context.Context, GlobRequest) ([]string, error) { return nil, ErrUnimplemented }
func (Sandbox) Grep(context.Context, GrepRequest) ([]GrepMatch, error) {
	return nil, ErrUnimplemented
}
func (Sandbox) Exec(context.Context, ExecRequest) (ExecResult, error) {
	return ExecResult{}, ErrUnimplemented
}
