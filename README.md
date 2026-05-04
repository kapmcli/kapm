<h1 align="center">kapm</h1>

<p align="center">
  Observability and package compatibility tools for Kiro agent projects.
</p>

<p align="center">
  <a href="https://github.com/kapmcli/kapm/actions/workflows/ci.yml"><img alt="CI" src="https://github.com/kapmcli/kapm/actions/workflows/ci.yml/badge.svg"></a>
  <a href="https://go.dev/"><img alt="Go version" src="https://img.shields.io/github/go-mod/go-version/kapmcli/kapm"></a>
  <a href="LICENSE"><img alt="License: MIT" src="https://img.shields.io/badge/License-MIT-blue.svg"></a>
</p>

<p align="center">
  English · <a href="README.ja.md">日本語</a> · <a href="README.ko.md">한국어</a> · <a href="README.zh-CN.md">简体中文</a>
</p>

<p align="center">
  <img src="demo-media/demo.gif" alt="kapm demo" />
</p>

## What kapm does

kapm helps you understand and maintain Kiro agent workspaces.

- **Monitor Kiro sessions**: read Kiro CLI v2 JSONL session logs (`~/.kiro/sessions/cli/`), Kiro IDE session logs (auto-detected), and Kiro CLI v1 SQLite session stores, supplemented by optional hook logs under `.kapm/logs/` for tool-call timestamps, agent attribution, and shell exit status. Inspect sessions, tool calls, failures, durations, agents, prompts, responses, file changes, and skill reads in a terminal UI or WebUI.
- **Manage Kiro agents**: create and update `.kiro/agents/*.json` and `.kiro/agent-prompts/*.md` interactively.
- **Bridge package formats**: sync APM packages and Kiro Powers into project-local `.kiro/` files.

## Installation

### Homebrew (macOS / Linux)

```bash
brew install --cask kapmcli/tap/kapm
```

### Release archives

Download the archive for your platform from [GitHub Releases](https://github.com/kapmcli/kapm/releases), extract it, and place `kapm` or `kapm.exe` on your `PATH`.

## Quick start

```bash
# Run Kiro, then inspect the recorded sessions.
# Terminal UI
kapm monitor
# Web UI
kapm serve

# Create or update a Kiro agent.
kapm agent generate

# Install kapm hook entries for selected agents.
kapm init-hook
# Install kapm hook files for Kiro IDE.
kapm init-ide-hook
```

## Monitoring

kapm reads Kiro CLI v2 session data (`~/.kiro/sessions/cli/{uuid}.jsonl` logs with `{uuid}.json` metadata) as its primary data source. Kiro IDE session logs are also loaded automatically when available. Kiro CLI v1 SQLite session stores (`conversations_v2`) are also read when present. No hook installation is required for basic monitoring — sessions contain prompts, assistant responses, tool calls, tool results, and per-turn metadata (credits).

`kapm init-hook` optionally adds hook entries to `.kiro/agents/*.json` for supplementary CLI agent data. `kapm init-ide-hook` writes Kiro IDE hook files under `.kiro/hooks/*.kiro.hook`. CLI hooks write minimal records under `.kapm/logs/cli/`; IDE hooks write minimal records under `.kapm/logs/ide/`.

```bash
kapm init-hook             # select agents interactively
kapm init-hook --global    # select global agents under ~/.kiro/agents
kapm init-hook --remove    # remove kapm-managed hook entries
kapm init-ide-hook         # install workspace Kiro IDE hook files
kapm init-ide-hook --remove # remove kapm-managed IDE hook files

kapm monitor
kapm monitor --json
kapm monitor --json --session <session-id>
kapm monitor --json --session <session-id> --agent <agent-name>

kapm serve
kapm serve --port 9097 --open
```

Both `monitor` and `serve` support:

```bash
--since 24h
--global                   # show sessions from all projects (default: current directory only)
--logs-dir <path>
--target-dir <path>
--ide-sessions-dir <path>  # override auto-detected IDE sessions directory
```

![WebUI overview](demo-media/webui-overview.png)

![WebUI session detail](demo-media/webui-session-detail.png)

### WebUI routes

| Route | Description |
|---|---|
| `GET /` | Overview dashboard |
| `GET /sessions` | Session list |
| `GET /sessions/{id}` | Merged session detail |
| `GET /sessions/{id}/{agent}` | Per-agent session detail |
| `GET /agents` | Agent list |
| `GET /agents/{name}` | Agent detail |
| `GET /tools` | Tool usage |
| `GET /tools/{name}` | Tool detail |
| `GET /skills` | Skill reads |

## Agent configuration

```bash
kapm agent generate
kapm agent generate --force
kapm agent update <name>
```

`agent generate` creates `.kiro/agents/<name>.json` and `.kiro/agent-prompts/<name>.md`. `agent update` edits an existing agent and preserves unknown JSON fields.

## APM compatibility

```bash
kapm sync
kapm sync --force

kapm install owner/repo
kapm install --update owner/repo
kapm install github/awesome-copilot/skills/review-and-refactor
```

`kapm sync` reads local `.apm/`, installed `apm_modules/`, and MCP dependencies from `apm.yml`, then writes Kiro-native files under `.kiro/`. Existing files are skipped unless `--force` is used.

`kapm install` delegates installation to `apm install`. If `apm` is not available, kapm falls back to `uvx --from apm-cli==0.9.1 apm install`. After installation, it runs the same sync step.

Additional kapm flags for `install`:

```bash
--sync-force            # overwrite .kiro files during the sync step
--target-dir <path>     # choose the project directory to sync into
```

`--global` is forwarded to APM and uses your home directory as the sync root. It cannot be combined with `--target-dir`.

## Kiro Power compatibility

```bash
kapm power install ./local/power
kapm power install owner/repo
kapm power install owner/repo/path/to/power --ref main
kapm power install https://github.com/owner/repo
kapm power install https://github.com/owner/repo/tree/main/path/to/power
```

`power install` copies the raw Power package into `.kiro/powers/<name>/`. It does not synthesize a skill file, merge MCP settings, or activate hooks. Instead, it prints concrete follow-up snippets:

- `file://` resource entries for `POWER.md` and `steering/*.md`
- `mcpServers` content when the Power includes `mcp.json`
- hook files to adapt when the Power includes `hooks/`
- a manual remove command

Use `--force` to overwrite an existing kapm-managed Power directory.

## Compatibility mapping

| Source | Kiro output |
|---|---|
| APM `instructions` | `.kiro/steering/<name>.md` |
| APM `prompts` | `.kiro/prompts/<name>.md` |
| APM `commands` | `.kiro/prompts/<name>.md` |
| APM `skills` | `.kiro/skills/<name>/...` |
| APM `agents` / `chatmodes` | `.kiro/agents/<name>.json` + `.kiro/agent-prompts/<name>.md` |
| APM MCP dependencies | `.kiro/settings/mcp.json` |
| Kiro Power package | `.kiro/powers/<name>/...` |

## Log format and retention

Hook logs use a minimal format: CLI records contain `ts`, `session`, `event`, `agent`, `tool`, and optionally `shell_exit_status`; IDE records contain `ts`, `event`, `agent`, and `cwd`. Prompts, tool input/output, and assistant responses are read from Kiro's session files, not from hook logs.

`.kapm/` is gitignored, directories are created with `0700`, and log files are created with `0600`.

## Development

```bash
just build
just test
just lint
```

If `just` is unavailable:

```bash
go build -o kapm ./cmd/kapm      # macOS / Linux
go build -o kapm.exe ./cmd/kapm  # Windows
```

## Links

- [APM docs](https://microsoft.github.io/apm/) · [APM CLI](https://microsoft.github.io/apm/reference/cli-commands/) · [APM source](https://github.com/microsoft/apm)
- [Kiro prompts](https://kiro.dev/docs/cli/chat/manage-prompts/) · [Kiro skills](https://kiro.dev/docs/skills/) · [Kiro steering](https://kiro.dev/docs/steering/) · [Kiro custom agents](https://kiro.dev/docs/chat/subagents/)
- [design.md](https://github.com/google-labs-code/design.md)
