<h1 align="center">kapm</h1>

<p align="center">
  面向 Kiro Agent 项目的可观测性工具和包兼容工具。
</p>

<p align="center">
  <a href="https://github.com/kapmcli/kapm/actions/workflows/ci.yml"><img alt="CI" src="https://github.com/kapmcli/kapm/actions/workflows/ci.yml/badge.svg"></a>
  <a href="https://go.dev/"><img alt="Go version" src="https://img.shields.io/github/go-mod/go-version/kapmcli/kapm"></a>
  <a href="LICENSE"><img alt="License: MIT" src="https://img.shields.io/badge/License-MIT-blue.svg"></a>
</p>

<p align="center">
  <a href="README.md">English</a> · <a href="README.ja.md">日本語</a> · <a href="README.ko.md">한국어</a> · 简体中文
</p>

<p align="center">
  <img src="demo-media/demo.gif" alt="kapm demo" />
</p>

## kapm 的用途

kapm 是一个 CLI，用于理解和维护 Kiro Agent 工作区。

- **监控 Kiro 会话**：读取 CLI 会话日志（`~/.kiro/sessions/cli/`）、Kiro IDE 会话日志（自动检测）和 v1 SQLite 会话存储，辅以 `.kapm/logs/` 下的可选 hook 日志（提供工具调用时间戳、Agent 归属和 shell 退出状态）。在 TUI 或 WebUI 中查看会话、工具调用、失败、耗时、派生的 Agent、提示、响应、文件变更和 Skill 读取情况。
- **管理 Kiro Agent**：以交互方式创建和更新 `.kiro/agents/*.json` 与 `.kiro/agent-prompts/*.md`。
- **连接包格式**：将 APM 包和 Kiro Power 同步为项目本地的 `.kiro/` 文件。

## 安装

### Homebrew (macOS / Linux)

```bash
brew install --cask kapmcli/tap/kapm
```

### 发布归档

从 [GitHub Releases](https://github.com/kapmcli/kapm/releases) 下载适合你平台的归档文件，解压后将 `kapm` 或 `kapm.exe` 放到 `PATH` 中。

### 从源码构建

```bash
just build
```

## 快速开始

```bash
# 正常运行 Kiro，然后查看记录的会话。
kapm monitor
kapm serve

# 创建或更新 Kiro Agent。
kapm agent generate

# 为选中的 Agent 安装 kapm hook。
kapm init-hook
# 为 Kiro IDE 安装 kapm hook 文件。
kapm init-ide-hook
```

## 监控

kapm 以 Kiro CLI v2 的会话数据（`~/.kiro/sessions/cli/{uuid}.jsonl` 日志和 `{uuid}.json` 元数据）作为主要数据源，同时在有 IDE 会话日志时自动加载。存在 Kiro CLI v1 SQLite 会话存储（`conversations_v2`）时也会读取。基本监控无需安装 hook——会话文件已包含提示、助手响应、工具调用、工具结果以及每轮的元数据（token 数、积分、耗时）。

`kapm init-hook` 可选地向 `.kiro/agents/*.json` 添加 hook 条目，以获取补充 CLI Agent 数据。`kapm init-ide-hook` 会将 Kiro IDE hook 文件写入 `.kiro/hooks/*.kiro.hook`。CLI hook 记录 `agentSpawn`、`preToolUse`、`postToolUse` 和 `stop`；IDE hook 会在 `preToolUse`、`postToolUse` 和 `stop` 调用 `kapm hook-dump`，把 raw stdin 和部分 Kiro 环境变量写入 `.kapm/logs/hook-input.jsonl`，以便先确认哪些 IDE telemetry 值可用。

```bash
kapm init-hook             # 交互式选择 Agent
kapm init-hook --global    # 选择 ~/.kiro/agents 下的全局 Agent
kapm init-hook --remove    # 移除 kapm 管理的 hook 条目
kapm init-ide-hook         # 安装工作区 Kiro IDE hook 文件
kapm init-ide-hook --remove # 移除 kapm 管理的 IDE hook 文件

kapm monitor
kapm monitor --json
kapm monitor --json --session <session-id>
kapm monitor --json --session <session-id> --agent <agent-name>

kapm serve
kapm serve --port 9097 --open
```

`monitor` 和 `serve` 都支持：

```bash
--since 24h
--global                   # 显示所有项目的会话（默认仅显示当前目录）
--logs-dir <path>
--target-dir <path>
--ide-sessions-dir <path>  # 指定 IDE 会话目录（默认：自动检测）
```

![WebUI overview](demo-media/webui-overview.png)

![WebUI session detail](demo-media/webui-session-detail.png)

### WebUI 路由

| Route | 说明 |
|---|---|
| `GET /` | Overview 仪表盘 |
| `GET /sessions` | 会话列表 |
| `GET /sessions/{id}` | 合并后的会话详情 |
| `GET /sessions/{id}/{agent}` | 按 Agent 查看会话详情 |
| `GET /agents` | Agent 列表 |
| `GET /agents/{name}` | Agent 详情 |
| `GET /tools` | 工具使用情况 |
| `GET /tools/{name}` | 工具详情 |
| `GET /skills` | Skill 读取情况 |

## Agent 配置

```bash
kapm agent generate
kapm agent generate --force
kapm agent update <name>
```

`agent generate` 会创建 `.kiro/agents/<name>.json` 和 `.kiro/agent-prompts/<name>.md`。`agent update` 会编辑现有 Agent，并保留未知的 JSON 字段。

## APM 兼容

```bash
kapm sync
kapm sync --force

kapm install owner/repo
kapm install --update owner/repo
kapm install github/awesome-copilot/skills/review-and-refactor
```

`kapm sync` 会读取本地 `.apm/`、已安装的 `apm_modules/` 以及 `apm.yml` 中的 MCP 依赖，并将 Kiro 原生文件写入 `.kiro/`。除非使用 `--force`，否则会跳过已有文件。

`kapm install` 会把安装过程委托给 `apm install`。如果找不到 `apm`，kapm 会回退到 `uvx --from apm-cli==0.9.1 apm install`。安装完成后会执行相同的 sync 步骤。

`install` 支持的 kapm 专用参数：

```bash
--sync-force            # 在 sync 步骤中覆盖 .kiro 文件
--target-dir <path>     # 指定要同步的项目目录
```

`--global` 会原样转发给 APM，并使用用户主目录作为 sync 根目录。它不能与 `--target-dir` 同时使用。

## Kiro Power 兼容

```bash
kapm power install ./local/power
kapm power install owner/repo
kapm power install owner/repo/path/to/power --ref main
kapm power install https://github.com/owner/repo
kapm power install https://github.com/owner/repo/tree/main/path/to/power
```

`power install` 会将原始 Power 包复制到 `.kiro/powers/<name>/`。它不会生成 Skill 文件、合并 MCP 设置或启用 hook，而是输出后续配置所需的具体片段：

- 指向 `POWER.md` 和 `steering/*.md` 的 `file://` resource 条目
- 当 Power 包含 `mcp.json` 时的 `mcpServers` 内容
- 当 Power 包含 `hooks/` 时，需要迁移到 Agent `hooks` 字段的文件列表
- 手动删除命令

使用 `--force` 可以覆盖已有的 kapm 管理 Power 目录。

## 兼容映射

| Source | Kiro output |
|---|---|
| APM `instructions` | `.kiro/steering/<name>.md` |
| APM `prompts` | `.kiro/prompts/<name>.md` |
| APM `commands` | `.kiro/prompts/<name>.md` |
| APM `skills` | `.kiro/skills/<name>/...` |
| APM `agents` / `chatmodes` | `.kiro/agents/<name>.json` + `.kiro/agent-prompts/<name>.md` |
| APM MCP dependencies | `.kiro/settings/mcp.json` |
| Kiro Power package | `.kiro/powers/<name>/...` |

## 日志格式和保留

Hook 日志采用精简格式：每条 JSONL 记录仅包含 `ts`、`session`、`event`、`agent`、`tool`，以及可选的 `shell_exit_status`。提示、工具输入/输出和助手响应均从 Kiro 的会话文件中读取，而非来自 hook 日志。

`.kapm/` 已加入 gitignore；目录以 `0700` 权限创建，日志文件以 `0600` 权限创建。

## 开发

```bash
just build
just test
just lint
```

如果没有 `just`：

```bash
go build -o kapm ./cmd/kapm      # macOS / Linux
go build -o kapm.exe ./cmd/kapm  # Windows
```

## Links

- [APM docs](https://microsoft.github.io/apm/) · [APM CLI](https://microsoft.github.io/apm/reference/cli-commands/) · [APM source](https://github.com/microsoft/apm)
- [Kiro prompts](https://kiro.dev/docs/cli/chat/manage-prompts/) · [Kiro skills](https://kiro.dev/docs/skills/) · [Kiro steering](https://kiro.dev/docs/steering/) · [Kiro custom agents](https://kiro.dev/docs/chat/subagents/)
- [design.md](https://github.com/google-labs-code/design.md)
