// Package files provides the workspace tools: read, write, edit, glob, grep
// and shell. They follow OpenCode's semantics and act only through a
// workspace.Workspace.
package files

import (
	"bytes"
	"context"
	_ "embed"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/cloudwego/eino/components/tool"

	"github.com/anas-domesticus/personal-agent/internal/tools/kit"
	"github.com/anas-domesticus/personal-agent/internal/workspace"
)

var (
	//go:embed desc/read.md
	readDesc string
	//go:embed desc/write.md
	writeDesc string
	//go:embed desc/edit.md
	editDesc string
	//go:embed desc/glob.md
	globDesc string
	//go:embed desc/grep.md
	grepDesc string
	//go:embed desc/shell.md
	shellDesc string
)

const (
	// MaxReadLines is the default and maximum lines read returns.
	MaxReadLines = 2000
	// MaxLineLength cuts longer lines in read output.
	MaxLineLength = 2000
	// readBudget keeps read output under kit.MaxOutput (in characters,
	// allowing for JSON escaping) so paging hints aren't cut off.
	readBudget = kit.MaxOutput * 3 / 4

	DefaultShellTimeout = 120_000 // ms
	MaxShellTimeout     = 600_000 // ms
)

// Specs returns the workspace tools, all acting on ws.
func Specs(ws workspace.Workspace) ([]kit.Spec, error) {
	type ctor struct {
		summary string
		build   func() (tool.InvokableTool, error)
	}
	ctors := []ctor{
		{"Read a file (with line numbers) or list a directory in the workspace.", func() (tool.InvokableTool, error) {
			return kit.New("read", readDesc, func(ctx context.Context, in readIn) (string, error) { return read(ctx, ws, in) })
		}},
		{"Create or overwrite a file in the workspace.", func() (tool.InvokableTool, error) {
			return kit.New("write", writeDesc, func(ctx context.Context, in writeIn) (writeOut, error) { return write(ctx, ws, in) })
		}},
		{"Replace exact text in a workspace file.", func() (tool.InvokableTool, error) {
			return kit.New("edit", editDesc, func(ctx context.Context, in editIn) (editOut, error) { return edit(ctx, ws, in) })
		}},
		{"Find workspace files by glob pattern.", func() (tool.InvokableTool, error) {
			return kit.New("glob", globDesc, func(ctx context.Context, in globIn) (globOut, error) { return glob(ctx, ws, in) })
		}},
		{"Search workspace file contents with a regular expression.", func() (tool.InvokableTool, error) {
			return kit.New("grep", grepDesc, func(ctx context.Context, in grepIn) (grepOut, error) { return grep(ctx, ws, in) })
		}},
		{"Run a shell command in the workspace.", func() (tool.InvokableTool, error) {
			return kit.New("shell", shellDesc, func(ctx context.Context, in shellIn) (shellOut, error) { return shell(ctx, ws, in) })
		}},
	}
	out := make([]kit.Spec, 0, len(ctors))
	for _, c := range ctors {
		t, err := c.build()
		if err != nil {
			return nil, err
		}
		out = append(out, kit.Static(c.summary, t))
	}
	return out, nil
}

// pathErr rewrites errors that would otherwise show host paths.
func pathErr(p string, err error) error {
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return fmt.Errorf("%s: not found", p)
	case errors.Is(err, workspace.ErrOutside):
		return fmt.Errorf("%s: %w", p, workspace.ErrOutside)
	}
	return err
}

// --- read ---

type readIn struct {
	Path   string `json:"path" jsonschema_description:"File or directory to read, relative to the workspace root."`
	Offset int    `json:"offset,omitempty" jsonschema_description:"1-based line (or entry) number to start from. Default 1."`
	Limit  int    `json:"limit,omitempty" jsonschema_description:"Number of lines (or entries) to return. Default and maximum 2000."`
}

func read(ctx context.Context, ws workspace.Workspace, in readIn) (string, error) {
	if in.Path == "" {
		return "", errors.New("path is required")
	}
	if in.Offset < 0 || in.Limit < 0 {
		return "", errors.New("offset and limit must not be negative")
	}
	offset := max(in.Offset, 1)
	limit := in.Limit
	if limit == 0 || limit > MaxReadLines {
		limit = MaxReadLines
	}
	st, err := ws.Stat(ctx, in.Path)
	if err != nil {
		return "", pathErr(in.Path, err)
	}

	var lines []string
	unit := "lines"
	if st.IsDir {
		unit = "entries"
		entries, err := ws.List(ctx, in.Path)
		if err != nil {
			return "", pathErr(in.Path, err)
		}
		for _, e := range entries {
			name := path.Base(e.Path)
			if e.IsDir {
				name += "/"
			}
			lines = append(lines, name)
		}
		if len(lines) == 0 {
			return "(empty directory)", nil
		}
	} else {
		data, err := ws.Read(ctx, in.Path)
		if err != nil {
			return "", pathErr(in.Path, err)
		}
		if bytes.IndexByte(data[:min(len(data), 8192)], 0) >= 0 {
			return "", fmt.Errorf("%s is a binary file and can't be read", in.Path)
		}
		if len(data) == 0 {
			return "(empty file)", nil
		}
		text := strings.TrimSuffix(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
		lines = strings.Split(text, "\n")
	}

	total := len(lines)
	if offset > total {
		return "", fmt.Errorf("offset %d is past the end (%d %s)", offset, total, unit)
	}
	var b strings.Builder
	used, last := 0, offset-1
	for i := offset - 1; i < total && i < offset-1+limit; i++ {
		line := cut(lines[i])
		cost := utf8.RuneCountInString(line) + 12
		if used+cost > readBudget && i > offset-1 {
			break
		}
		used += cost
		fmt.Fprintf(&b, "%6d\t%s\n", i+1, line)
		last = i + 1
	}
	if last < total {
		fmt.Fprintf(&b, "\n(Showing %s %d-%d of %d. Use offset=%d to read more.)", unit, offset, last, total, last+1)
	} else {
		fmt.Fprintf(&b, "\n(End - %d %s total.)", total, unit)
	}
	return b.String(), nil
}

func cut(line string) string {
	if utf8.RuneCountInString(line) <= MaxLineLength {
		return line
	}
	return string([]rune(line)[:MaxLineLength]) + "... (line cut)"
}

// --- write ---

type writeIn struct {
	Path    string `json:"path" jsonschema_description:"File to write, relative to the workspace root. Parent directories are created."`
	Content string `json:"content" jsonschema_description:"The complete new content of the file."`
}

type writeOut struct {
	Path    string `json:"path"`
	Bytes   int    `json:"bytes"`
	Created bool   `json:"created"`
}

func write(ctx context.Context, ws workspace.Workspace, in writeIn) (writeOut, error) {
	if in.Path == "" {
		return writeOut{}, errors.New("path is required")
	}
	st, err := ws.Stat(ctx, in.Path)
	created := errors.Is(err, fs.ErrNotExist)
	if err != nil && !created {
		return writeOut{}, pathErr(in.Path, err)
	}
	if err == nil && st.IsDir {
		return writeOut{}, fmt.Errorf("%s is a directory", in.Path)
	}
	if err := ws.Write(ctx, in.Path, []byte(in.Content)); err != nil {
		return writeOut{}, pathErr(in.Path, err)
	}
	return writeOut{Path: in.Path, Bytes: len(in.Content), Created: created}, nil
}

// --- edit ---

type editIn struct {
	Path       string `json:"path" jsonschema_description:"File to edit, relative to the workspace root."`
	OldString  string `json:"old_string" jsonschema_description:"Exact text to replace. Empty creates a new file with new_string."`
	NewString  string `json:"new_string" jsonschema_description:"Text to replace it with. Must differ from old_string."`
	ReplaceAll bool   `json:"replace_all,omitempty" jsonschema_description:"Replace every occurrence instead of requiring exactly one. Default false."`
}

type editOut struct {
	Path         string `json:"path"`
	Replacements int    `json:"replacements"`
	Created      bool   `json:"created,omitempty"`
}

func edit(ctx context.Context, ws workspace.Workspace, in editIn) (editOut, error) {
	if in.Path == "" {
		return editOut{}, errors.New("path is required")
	}
	if in.OldString == in.NewString {
		return editOut{}, errors.New("old_string and new_string are the same; nothing to change")
	}
	data, err := ws.Read(ctx, in.Path)
	missing := errors.Is(err, fs.ErrNotExist)
	if err != nil && !missing {
		return editOut{}, pathErr(in.Path, err)
	}

	if in.OldString == "" {
		if !missing && len(data) > 0 {
			return editOut{}, fmt.Errorf("%s already exists and is not empty; old_string must be the text to replace (use write to replace the whole file)", in.Path)
		}
		if err := ws.Write(ctx, in.Path, []byte(in.NewString)); err != nil {
			return editOut{}, pathErr(in.Path, err)
		}
		return editOut{Path: in.Path, Replacements: 1, Created: missing}, nil
	}
	if missing {
		return editOut{}, fmt.Errorf("%s: not found", in.Path)
	}

	content := string(data)
	old, repl := in.OldString, in.NewString
	n := strings.Count(content, old)
	if n == 0 && strings.Contains(content, "\r\n") && !strings.Contains(old, "\r\n") {
		// The file uses CRLF line endings; match the model's LF text to it.
		old = strings.ReplaceAll(old, "\n", "\r\n")
		repl = strings.ReplaceAll(repl, "\n", "\r\n")
		n = strings.Count(content, old)
	}
	switch {
	case n == 0:
		return editOut{}, fmt.Errorf("old_string not found in %s; it must match exactly, including whitespace and indentation (read the file again to check)", in.Path)
	case n > 1 && !in.ReplaceAll:
		return editOut{}, fmt.Errorf("old_string matches %d times in %s; include more surrounding context to make it unique, or set replace_all", n, in.Path)
	}
	if in.ReplaceAll {
		content = strings.ReplaceAll(content, old, repl)
	} else {
		content = strings.Replace(content, old, repl, 1)
	}
	if err := ws.Write(ctx, in.Path, []byte(content)); err != nil {
		return editOut{}, pathErr(in.Path, err)
	}
	return editOut{Path: in.Path, Replacements: n}, nil
}

// --- glob ---

type globIn struct {
	Pattern string `json:"pattern" jsonschema_description:"Glob pattern, e.g. **/*.go or src/**/*.{ts,tsx}. Without / it matches file names at any depth."`
	Path    string `json:"path,omitempty" jsonschema_description:"Directory to search, relative to the workspace root. Default: the root."`
	Hidden  bool   `json:"hidden,omitempty" jsonschema_description:"Include hidden files and directories (names starting with a dot). Default false."`
	Limit   int    `json:"limit,omitempty" jsonschema_description:"Maximum number of results. Default 100."`
}

type globOut struct {
	Files     []string `json:"files"`
	Truncated bool     `json:"truncated,omitempty"`
	Note      string   `json:"note,omitempty"`
}

func glob(ctx context.Context, ws workspace.Workspace, in globIn) (globOut, error) {
	limit := defaultLimit(in.Limit)
	files, err := ws.Glob(ctx, workspace.GlobRequest{Dir: in.Path, Pattern: in.Pattern, Hidden: in.Hidden, Limit: limit + 1})
	if err != nil {
		return globOut{}, pathErr(in.Path, err)
	}
	out := globOut{Files: files}
	if out.Files == nil {
		out.Files = []string{}
		out.Note = "No files found."
	}
	if len(files) > limit {
		out.Files, out.Truncated = files[:limit], true
		out.Note = fmt.Sprintf("Only the first %d results are shown. Use a more specific pattern or path.", limit)
	}
	return out, nil
}

func defaultLimit(n int) int {
	if n <= 0 {
		return workspace.DefaultLimit
	}
	return n
}

// --- grep ---

type grepIn struct {
	Pattern       string `json:"pattern" jsonschema_description:"Regular expression (Go RE2 syntax) to search for, or plain text if literal is true."`
	Path          string `json:"path,omitempty" jsonschema_description:"File or directory to search, relative to the workspace root. Default: the root."`
	Include       string `json:"include,omitempty" jsonschema_description:"Only search files whose name matches this glob, e.g. *.go or *.{ts,tsx}."`
	Literal       bool   `json:"literal,omitempty" jsonschema_description:"Treat pattern as plain text, not a regular expression. Default false."`
	CaseSensitive *bool  `json:"case_sensitive,omitempty" jsonschema_description:"Match case exactly. Default true."`
	Hidden        bool   `json:"hidden,omitempty" jsonschema_description:"Include hidden files and directories. Default false."`
	Limit         int    `json:"limit,omitempty" jsonschema_description:"Maximum number of matching lines. Default 100."`
}

type grepOut struct {
	Matches   int    `json:"matches"`
	Truncated bool   `json:"truncated,omitempty"`
	Output    string `json:"output"`
}

func grep(ctx context.Context, ws workspace.Workspace, in grepIn) (grepOut, error) {
	limit := defaultLimit(in.Limit)
	ms, err := ws.Grep(ctx, workspace.GrepRequest{
		Path:       in.Path,
		Pattern:    in.Pattern,
		Literal:    in.Literal,
		IgnoreCase: in.CaseSensitive != nil && !*in.CaseSensitive,
		Include:    in.Include,
		Hidden:     in.Hidden,
		Limit:      limit + 1,
	})
	if err != nil {
		return grepOut{}, pathErr(in.Path, err)
	}
	out := grepOut{}
	if len(ms) > limit {
		ms, out.Truncated = ms[:limit], true
	}
	out.Matches = len(ms)
	if len(ms) == 0 {
		out.Output = "No matches found."
		return out, nil
	}
	var b strings.Builder
	for i, m := range ms {
		if i > 0 && m.Path != ms[i-1].Path {
			b.WriteByte('\n')
		}
		fmt.Fprintf(&b, "%s:%d: %s\n", m.Path, m.Line, m.Text)
	}
	if out.Truncated {
		fmt.Fprintf(&b, "\n(Only the first %d matches are shown. Narrow the pattern, path or include.)", limit)
	}
	out.Output = b.String()
	return out, nil
}

// --- shell ---

type shellIn struct {
	Command     string `json:"command" jsonschema_description:"The command to run with sh -c."`
	Workdir     string `json:"workdir,omitempty" jsonschema_description:"Directory to run in, relative to the workspace root. Default: the root."`
	Timeout     int    `json:"timeout,omitempty" jsonschema_description:"Timeout in milliseconds. Default 120000, maximum 600000."`
	Description string `json:"description,omitempty" jsonschema_description:"Short label (5-10 words) for what the command does."`
}

type shellOut struct {
	ExitCode  int    `json:"exit_code"`
	Output    string `json:"output"`
	TimedOut  bool   `json:"timed_out,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
}

func shell(ctx context.Context, ws workspace.Workspace, in shellIn) (shellOut, error) {
	if strings.TrimSpace(in.Command) == "" {
		return shellOut{}, errors.New("command is required")
	}
	ms := in.Timeout
	if ms <= 0 {
		ms = DefaultShellTimeout
	}
	ms = min(ms, MaxShellTimeout)
	res, err := ws.Exec(ctx, workspace.ExecRequest{
		Command: in.Command,
		Dir:     in.Workdir,
		Timeout: time.Duration(ms) * time.Millisecond,
	})
	if err != nil {
		return shellOut{}, pathErr(in.Workdir, err)
	}
	out := shellOut{ExitCode: res.ExitCode, Output: res.Output, TimedOut: res.TimedOut, Truncated: res.Truncated}
	if r := []rune(out.Output); len(r) > readBudget {
		// kit's cap keeps the head; for command output the tail matters more.
		out.Output = fmt.Sprintf("...[output truncated: only the last %d characters are shown]\n", readBudget) +
			string(r[len(r)-readBudget:])
		out.Truncated = true
	}
	if res.TimedOut {
		out.Output += fmt.Sprintf("\n(Command timed out after %dms and was killed.)", ms)
	}
	return out, nil
}
