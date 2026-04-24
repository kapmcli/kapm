# kapm

[![CI](https://github.com/kapmcli/kapm/actions/workflows/ci.yml/badge.svg)](https://github.com/kapmcli/kapm/actions/workflows/ci.yml)

`kapm` adapts [APM (Agent Package Manager)](https://microsoft.github.io/apm/) content into Kiro-native files.

If your repository already uses APM concepts such as `.apm/`, `apm_modules/`, and `apm.yml`, `kapm` turns that content into the `.kiro/` tree that Kiro expects.

- APM docs: <https://microsoft.github.io/apm/>
- APM quick start: <https://microsoft.github.io/apm/getting-started/quick-start/>
- APM manifest schema (`apm.yml`): <https://microsoft.github.io/apm/reference/manifest-schema/>
- APM CLI commands: <https://microsoft.github.io/apm/reference/cli-commands/>
- APM source repository: <https://github.com/microsoft/apm>
- Kiro prompt docs: <https://kiro.dev/docs/cli/chat/manage-prompts/>
- Kiro skills docs: <https://kiro.dev/docs/skills/>
- Kiro steering docs: <https://kiro.dev/docs/steering/>
- Kiro custom agents docs: <https://kiro.dev/docs/chat/subagents/>

## Installation

### Homebrew (macOS / Linux)

```bash
export HOMEBREW_GITHUB_API_TOKEN=ghp_xxxxxxxxxxxx  # GitHub PAT with repo scope
brew tap kapmcli/tap
brew install kapmcli/tap/kapm
```

### Build from source

```bash
just build
```

## What kapm does

`kapm` has three jobs:

1. **`kapm sync`** reads APM-managed content that already exists in the repo and writes the corresponding `.kiro/` output.
2. **`kapm install`** prefers a local `apm` binary, falls back to `uvx --from apm-cli apm`, and then performs the same sync step automatically.
3. **`kapm agent generate|update`** manages `.kiro/agents/*.json` interactively. This is Kiro-specific authoring support, not APM conversion.

## How kapm works

### Inputs

`kapm sync` gathers content from three places:

- local `.apm/`
- installed modules under `apm_modules/<owner>/<repo>/.apm/`
- MCP dependencies declared in `apm.yml`

### Conversion pipeline

For each discovered APM source directory, `kapm` runs the primitive converters and writes generated Kiro files into `.kiro/`.

- instructions become steering docs
- prompts become prompt docs
- commands become prompt docs
- skills are copied as directory trees
- agents become Kiro agent JSON plus an agent prompt file
- MCP dependencies are merged into `.kiro/settings/mcp.json`

The sync path is deterministic:

- local `.apm/` content has highest precedence
- installed modules are processed in `apm.yml` dependency order when available; otherwise they fall back to a stable path sort
- primitive files are processed in sorted order
- existing generated files are skipped unless you pass `--force`

That means `kapm` is designed to be rerun safely as your APM inputs change.

### Install flow

`kapm install ...` shells out to one of these commands:

```bash
apm install ...
# or, if `apm` is not on PATH
uvx --from apm-cli apm install ...
```

and then immediately runs the same sync pipeline used by `kapm sync`.

### Kiro workspace vs global

Kiro supports both workspace-scoped and user-scoped `.kiro/` content.

- prompts can live in `.kiro/prompts/` or `~/.kiro/prompts/`
- skills can live in `.kiro/skills/` or `~/.kiro/skills/`
- steering can live in `.kiro/steering/` or `~/.kiro/steering/`
- custom agents can live in `.kiro/agents/` or `~/.kiro/agents/`

`kapm` writes the current repository's `.kiro/` tree.

The APM CLI also has a user-scope install mode via `apm install --global`; see the APM CLI docs for details.

## Output mapping

| APM source | Kiro output |
|---|---|
| instructions | `.kiro/steering/<name>.md` |
| prompts | `.kiro/prompts/<name>.md` |
| commands | `.kiro/prompts/<name>.md` |
| skills | `.kiro/skills/<name>/...` |
| agents / chatmodes | `.kiro/agents/<name>.json` + `.kiro/agent-prompts/<name>.md` |
| MCP dependencies | `.kiro/settings/mcp.json` |

Two important details:

- instruction output always gets Kiro steering front matter with `inclusion: always`
- MCP output is **merged** into `.kiro/settings/mcp.json` rather than replacing the whole file

## Commands

 ```bash
 kapm sync
 kapm sync --force
 
  kapm install
  kapm install owner/repo
  kapm install --update owner/repo
  kapm install github/awesome-copilot/skills/review-and-refactor
  kapm install --sync-force owner/repo
  
  kapm agent generate
 kapm agent generate --force

kapm agent update <name>
```

### `kapm sync`

```bash
kapm sync
```

Use this when APM content is already present in the repo and you want to regenerate `.kiro/`.

Use `--force` to overwrite previously generated files.

Within a single sync run, source precedence still follows APM rules: local content wins over dependencies, and earlier dependencies win over later ones.

### `kapm install`

```bash
kapm install owner/repo
```

Use this when you want `kapm` to run `apm install` first and then sync the result into `.kiro/`.

If `apm` is already available in your environment, `kapm` uses it directly. If not, it falls back to `uvx --from apm-cli apm`.

`kapm install` is a thin wrapper: APM-owned install arguments are forwarded to `apm install` rather than reimplemented in `kapm`.

The only install-specific kapm flag is `--sync-force`, which controls the post-install `.kiro` sync step.

All other arguments are passed through to `apm install`, including flags such as `--help`, `--update`, or `--force`.

That means `kapm` can install any APM target that `apm install` accepts, including repo subdirectories / virtual packages such as `github/awesome-copilot/skills/review-and-refactor`.

### `kapm agent generate`

```bash
kapm agent generate
```

This interactively creates:

- `.kiro/agents/<name>.json`
- `.kiro/agent-prompts/<name>.md`

The prompt flow collects:

- `name`
- `description`
- `model`
- `tools`
- `allowedTools`
- `resources`

Existing files are rejected unless you pass `--force`.

### `kapm agent update`

```bash
kapm agent update <name>
```

This interactively updates an existing `.kiro/agents/<name>.json`.

Notable behavior:

- unknown JSON fields are preserved
- empty resource lists are omitted on write
- if nothing changes, the command prints `No changes.`

## Monitoring

`kapm init-hook` installs a structured logger for all Kiro CLI hook events
into selected agents in `.kiro/agents/`. Every hook invocation is written as
one JSONL record to `.kiro/logs/{session_id}.jsonl`. When a new Kiro session
starts, other session files that have been idle for at least 24 hours are
compressed to `.jsonl.gz`.

### Enable

```bash
kapm init-hook
```

You will be shown an interactive multi-select of agents in `.kiro/agents/`.
Selecting an agent appends hook entries to its `hooks` field for all five
event types (`agentSpawn`, `userPromptSubmit`, `preToolUse`, `postToolUse`,
`stop`). kapm-managed entries are detected by the ` kapl` suffix in
the `command` field. Your own hook entries are preserved.
Re-running `kapm init-hook` is safe: kapm-managed entries are replaced, not
duplicated.

### Disable

```bash
kapm init-hook --remove
```

Removes kapm-managed entries while keeping your own hook entries intact.

### Log contents

Each JSONL line contains `ts`, `agent`, `session`, `event`, and, where
applicable, `tool`, `tool_input`, `tool_response`, `prompt`, `cwd`. The
`tool_input` and `tool_response` fields are recorded verbatim.

**Warning — sensitive data**: logs include the full tool input and
response, which may contain file paths, command lines, source code
excerpts, credentials surfaced by the agent, or any other data the agent
touches. `.kiro/logs/` is added to `.gitignore` automatically but log files
are still readable to anyone with access to your workspace. The log
directory is created with `0o700` and files with `0o600`.

### Rotation

Rotation happens automatically when an `agentSpawn` event fires: any other
session's `*.jsonl` file whose last modification is at least 24 hours old is
gzip-compressed to `*.jsonl.gz`. Files modified more recently are left as
`.jsonl` so concurrent sessions can keep appending to them. The current
session's file is never rotated. Retention / cleanup of old `.gz` files is
out of scope for this release.

### Scope and caveats

- **Workspace only**. kapm operates on `.kiro/agents/` only; agents in
  `~/.kiro/agents/` are not touched.
- **`kapm sync --force` and `kapm install --sync-force` delete hooks**.
  Those commands rewrite agent JSON files from sources that do not know
  about hooks. Re-run `kapm init-hook` after any force-sync to reinstall
  the monitoring hooks.
- **Re-run after moving `kapm`**. Each injected hook command holds the
  absolute path to the `kapm` binary. If you reinstall kapm to a
  different location, re-run `kapm init-hook` to rewrite the paths.
- **Independent of `.kiro/hooks/`**. Shell scripts in `.kiro/hooks/` (such
  as the existing `nudge-todo.sh`) are not affected by this feature.
  kapm does not read from or write to `.kiro/hooks/`.

### kapm monitor

```bash
kapm monitor
kapm monitor --json
kapm monitor --session=<sid>
kapm monitor --session=<sid> --agent=<name>
```

Starts a local web server (default `:9090`) and TUI that display aggregated
session metrics from `.kiro/logs/`.

#### Routes

| Route | Description |
|---|---|
| `GET /` | Overview dashboard |
| `GET /sessions` | Session list (one row per `(sid, agent)`) |
| `GET /sessions/{id}` | Merged session detail across all agents for that sid |
| `GET /sessions/{id}/{agent}` | Per-agent session detail |
| `GET /api/metrics` | JSON metrics; supports `?session=` and `?agent=` query params |

#### Flags

| Flag | Description |
|---|---|
| `--json` | Emit metrics as JSON to stdout instead of starting the TUI |
| `--session=<sid>` | Filter to a single session. With `--json`, emits the **merged** SessionDetail (Agent=`"(all)"`). Combine with `--agent` to narrow to one agent. |
| `--agent=<name>` | Narrow output to a specific agent. Has no effect without `--session`. |

**Merged vs narrowed**: `--session=<sid>` alone returns a merged view that
aggregates all agents active in that session. Add `--agent=<name>` to get the
per-agent slice instead.

## Requirements

- Go 1.26+
- either `apm` on your `PATH`, or [uv](https://github.com/astral-sh/uv) for the `uvx` fallback used by `kapm install`

## Development

`kapm` embeds the `kapl` binary via `//go:embed`. Build via the
Justfile so kapl is compiled first:

```bash
just build   # builds kapl → embeds → builds kapm
just test
```

Building with `go build ./cmd/kapm` directly will fail with
`pattern kapl: no matching files` until kapl exists at
`internal/agent/kapl`. Run the two commands manually if `just`
is unavailable:

```bash
go build -o internal/agent/kapl ./cmd/kapl
go build -o kapm ./cmd/kapm
```

The embedded binary is ignored by git (`.gitignore`) so every build
regenerates it from source.
