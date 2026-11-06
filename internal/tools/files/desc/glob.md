Find files by name pattern in the workspace.

- `pattern` is a glob: `*` matches within a path segment, `**` matches any number of directories, `{a,b}` matches alternatives, e.g. `**/*.go`, `src/**/*.{ts,tsx}`, `docs/*.md`.
- A pattern without `/` matches file names at any depth, so `*.go` finds Go files everywhere.
- `path` is the directory to search (default: the workspace root); the pattern is relative to it.
- Results are file paths relative to the workspace root, most recently modified first.
- Hidden files and directories (starting with `.`) are skipped unless `hidden` is true; `.git` is always skipped.
- At most `limit` results are returned (default 100); the result says when there were more.
- Use this to find files by name; use grep to search file contents.
