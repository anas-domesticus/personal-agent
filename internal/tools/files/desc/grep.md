Search file contents in the workspace with a regular expression.

- `pattern` is a Go (RE2) regular expression, e.g. `func\s+\w+\(`, `log.*Error`. Set `literal` to search for the text exactly, with no special characters.
- Matching is case-sensitive unless `case_sensitive` is false.
- `path` is a file or directory to search (default: the workspace root).
- `include` filters files by a glob on their name, e.g. `*.go` or `*.{ts,tsx}` (or on their path relative to `path` if it contains `/`).
- Hidden files and directories are skipped unless `hidden` is true; `.git`, binary files and files over 1MB are always skipped.
- Each match is printed as `path:line: text`, grouped by file. At most `limit` matches are returned (default 100); the result says when there were more.
- Use this to find where something is defined or used; then read the file around the line numbers.
