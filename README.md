<h1 align="center">kapm</h1>

<p align="center">
  Observability for Kiro agent sessions: capture hook events and inspect tool calls, failures, durations, spawned agents, and skill usage from TUI/Web dashboards.
</p>
<p align="center">
  Also includes compatibility helpers for syncing <a href="https://microsoft.github.io/apm/">APM</a> packages and Kiro Powers into project-local <code>.kiro/</code> files.
</p>

<p align="center">
  <a href="https://github.com/kapmcli/kapm/actions/workflows/ci.yml"><img alt="CI" src="https://github.com/kapmcli/kapm/actions/workflows/ci.yml/badge.svg"></a>
  <a href="https://go.dev/"><img alt="Go version" src="https://img.shields.io/github/go-mod/go-version/kapmcli/kapm"></a>
  <a href="LICENSE"><img alt="License: MIT" src="https://img.shields.io/badge/License-MIT-blue.svg"></a>
</p>

<p align="center">
  ⚡ <a href="#installation">Installation</a>
  · 🚀 <a href="#quick-start">Quick start</a>
  · 🛰️ <a href="#kapm-monitor--kapm-serve">Monitoring</a>
  · 📦 <a href="https://github.com/kapmcli/kapm/releases">Releases</a>
</p>

<p align="center">
  <img src="demo-media/demo.gif" alt="kapm demo" />
</p>

## Installation

### Homebrew (macOS / Linux)

```bash
brew install --cask kapmcli/tap/kapm
```

### Windows

Download the Windows zip archive from the [GitHub Releases](https://github.com/kapmcli/kapm/releases) page and place `kapm.exe` on your `PATH`.

### Build from source

```bash
just build
```

## Quick start

```bash
# Create or use an existing Kiro agent
kapm agent generate

# Enable session logging for selected agents
kapm init-hook

# Run Kiro as usual, then inspect what happened

# View session metrics in the terminal
kapm monitor

# Open the WebUI dashboard
kapm serve

# Sync existing APM content into .kiro/
kapm sync

# Install an APM package and sync
kapm install owner/repo

# Install a Kiro Power package into .kiro/powers
kapm power install ./local/power
```

## Commands

### `kapm init-hook`

Installs a structured JSONL logger into selected agents. Every hook event (`agentSpawn`, `userPromptSubmit`, `preToolUse`, `postToolUse`, `stop`) is recorded to `.kiro/logs/{session_id}.jsonl`.

```bash
kapm init-hook             # interactive agent selection
kapm init-hook --remove    # remove kapm-managed hooks
```

Generated hook entries invoke `kapm hook-handler --agent <name>` directly, so no separate helper binary is installed under `.kiro/hooks/`.

Re-running is safe — existing hooks are replaced, not duplicated. Your own hook entries are preserved.

**Note**: `kapm sync --force` and `kapm install --sync-force` rewrite agent JSON and remove hooks. Re-run `kapm init-hook` after force-sync.

### `kapm monitor` / `kapm serve`

Inspect recorded Kiro sessions from `.kiro/logs/`: tool calls, failures, durations, spawned agents, skill reads, and per-agent timelines.

```bash
kapm monitor                                     # TUI
kapm monitor --json                              # JSON to stdout
kapm monitor --json --session=<sid>              # single session (merged)
kapm monitor --json --session=<sid> --agent=<a>  # single session, single agent

kapm serve                                # WebUI on :9090
kapm serve --port 9097                    # custom port
```

![WebUI](demo-media/webui-overview.png)

![WebUI session detail](demo-media/webui-session-detail.png)

#### WebUI routes

| Route | Description |
|---|---|
| `GET /` | Overview dashboard |
| `GET /sessions` | Session list |
| `GET /sessions/{id}` | Merged session detail |
| `GET /sessions/{id}/{agent}` | Per-agent session detail |
| `GET /agents` | Agent list |
| `GET /tools` | Tool usage |
| `GET /skills` | Skill reads |

### `kapm agent generate` / `kapm agent update`

Interactively create or update `.kiro/agents/<name>.json` and `.kiro/agent-prompts/<name>.md`.

```bash
kapm agent generate            # create new agent
kapm agent generate --force    # overwrite existing
kapm agent update <name>       # update existing agent
```

### `kapm sync`

Reads APM content from `.apm/`, `apm_modules/`, and MCP dependencies in `apm.yml`, then writes the corresponding `.kiro/` output.

```bash
kapm sync            # skip existing files
kapm sync --force    # overwrite existing files
```

Source precedence: local `.apm/` > installed modules (in `apm.yml` dependency order) > fallback path sort. Existing files are skipped unless `--force` is passed.

### `kapm install`

Runs `apm install` (or `uvx --from apm-cli apm install` as fallback), then syncs the result into `.kiro/`.

```bash
kapm install owner/repo
kapm install --update owner/repo
kapm install github/awesome-copilot/skills/review-and-refactor
```

Most arguments are forwarded to `apm install`. kapm also adds `--sync-force` (overwrite `.kiro/` files during the post-install sync) and `--target-dir` (change where the post-install sync writes `.kiro/`). `--global` is still forwarded to `apm install`; when present, kapm uses your home directory as the sync root, and it cannot be combined with `--target-dir`.

### `kapm power install`

Installs a Kiro Power package into `.kiro/powers/<name>/`.

```bash
kapm power install ./local/power
kapm power install upstash/context7/plugins/context7-power --ref master
kapm power install https://github.com/owner/repo
kapm power install https://github.com/owner/repo/tree/main/sub/path
```

Use `POWER.md` and `steering/*.md` as custom agent `resources`. If the Power includes `mcp.json` or `hooks/`, wire those into the agent separately. Use `--force` to overwrite an existing kapm-managed Power dir.

## Compatibility mapping

These mappings are compatibility helpers for using APM and Power package content from Kiro projects.

| APM source | Kiro output |
|---|---|
| instructions | `.kiro/steering/<name>.md` |
| prompts | `.kiro/prompts/<name>.md` |
| commands | `.kiro/prompts/<name>.md` |
| skills | `.kiro/skills/<name>/...` |
| Kiro Power package | `.kiro/powers/<name>/...` |
| agents / chatmodes | `.kiro/agents/<name>.json` + `.kiro/agent-prompts/<name>.md` |
| MCP dependencies | `.kiro/settings/mcp.json` (merged) |

## Logging

### Log contents

Each JSONL line contains `ts`, `agent`, `session`, `event`, and where applicable `tool`, `tool_input`, `tool_response`, `prompt`, `cwd`.

**Warning**: logs include full tool input/response which may contain file paths, source code, or credentials. `.kiro/logs/` is gitignored and created with `0o700` / files `0o600`.

### Rotation

On `agentSpawn`, idle session files (>24h since last write) are gzip-compressed to `.jsonl.gz`. Active sessions are left as `.jsonl`.

## Development

Always build via the Justfile:

```bash
just build
just test
just lint
```

Manual build (if `just` is unavailable):

```bash
go build -o kapm ./cmd/kapm      # macOS / Linux
go build -o kapm.exe ./cmd/kapm  # Windows
```

The repo-root `DESIGN.md` is the canonical WebUI design-system document, following the upstream [`design.md`](https://github.com/google-labs-code/design.md) convention. `internal/serve/DESIGN.md` is only the generated embed copy for `/design-preview`.

## Links

- [APM docs](https://microsoft.github.io/apm/) · [APM quick start](https://microsoft.github.io/apm/getting-started/quick-start/) · [APM manifest schema](https://microsoft.github.io/apm/reference/manifest-schema/) · [APM CLI](https://microsoft.github.io/apm/reference/cli-commands/) · [APM source](https://github.com/microsoft/apm)
- [Kiro prompts](https://kiro.dev/docs/cli/chat/manage-prompts/) · [Kiro skills](https://kiro.dev/docs/skills/) · [Kiro steering](https://kiro.dev/docs/steering/) · [Kiro custom agents](https://kiro.dev/docs/chat/subagents/)
