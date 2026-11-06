Read a file or list a directory in the workspace.

- `path` is relative to the workspace root (absolute paths inside the workspace also work).
- By default up to 2000 lines are returned from the start of the file. For long files, page with `offset` (1-based line number to start at) and `limit` (lines to return, max 2000). The result says when more lines remain and which offset to use next.
- Each line is prefixed with its line number and a tab: `     1\tcontent`. The prefix is NOT part of the file; never include it in `old_string` for the edit tool.
- Lines longer than 2000 characters are cut.
- Reading a directory lists its entries, one per line, with a trailing `/` on subdirectories; it pages the same way.
- Binary files can't be read.
- Read a file before editing it. When you need several files, call read for each of them rather than one after another.
