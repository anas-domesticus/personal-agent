Run a shell command in the workspace with `sh -c` and return its combined stdout and stderr and its exit code.

- `workdir` is the directory to run in, relative to the workspace root (default: the root). Use it instead of `cd dir && ...`.
- `timeout` is in milliseconds (default 120000, max 600000). The command and everything it started are killed when it runs out.
- `description` is a short label (5-10 words) saying what the command does, e.g. "Run unit tests".
- A non-zero exit code is reported, not treated as a failure of the tool; check `exit_code`.
- Output over about 50KB keeps only the end.
- The environment is minimal (PATH, HOME, USER, LANG, TMPDIR, TERM); secrets from the agent's environment are not available.
- There is no stdin: interactive commands will fail or hang until the timeout. Pass flags for non-interactive use (e.g. `-y`).
- Prefer the read, write, edit, glob and grep tools over `cat`, `sed`, `find` and `grep` for working with files.
- Chain dependent commands with `&&`; quote paths containing spaces.
