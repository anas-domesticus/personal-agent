Write a file in the workspace, creating it (and any missing parent directories) or replacing it entirely.

- `path` is relative to the workspace root.
- `content` becomes the whole file.
- If the file already exists, read it first. Prefer the edit tool for changing part of an existing file; use write for new files or full rewrites.
- Don't create documentation or README files unless asked.
