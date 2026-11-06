Replace exact text in a file in the workspace.

- Read the file first so you know its exact current content.
- `old_string` must match the file exactly, including whitespace and indentation. Don't include the line-number prefix the read tool adds.
- `new_string` replaces it and must differ from `old_string`.
- Without `replace_all`, `old_string` must occur exactly once. The edit fails if it occurs zero times or more than once; when it occurs more than once, include more surrounding lines to make it unique, or set `replace_all`.
- `replace_all: true` replaces every occurrence, e.g. to rename a variable throughout the file.
- An empty `old_string` creates a new file containing `new_string` (only if the file doesn't exist or is empty).
