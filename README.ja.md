<h1 align="center">kapm</h1>

<p align="center">
  Kiro エージェントプロジェクトのための観測性ツールとパッケージ互換ツールです。
</p>

<p align="center">
  <a href="https://github.com/kapmcli/kapm/actions/workflows/ci.yml"><img alt="CI" src="https://github.com/kapmcli/kapm/actions/workflows/ci.yml/badge.svg"></a>
  <a href="https://go.dev/"><img alt="Go version" src="https://img.shields.io/github/go-mod/go-version/kapmcli/kapm"></a>
  <a href="LICENSE"><img alt="License: MIT" src="https://img.shields.io/badge/License-MIT-blue.svg"></a>
</p>

<p align="center">
  <a href="README.md">English</a> · 日本語 · <a href="README.ko.md">한국어</a> · <a href="README.zh-CN.md">简体中文</a>
</p>

<p align="center">
  <img src="demo-media/demo.gif" alt="kapm demo" />
</p>

## kapm でできること

kapm は、Kiro エージェントの作業内容を把握し、ワークスペースを保守しやすくするための CLI です。

- **Kiro セッションの監視**: hook イベントを `.kapm/logs` に記録し、セッション、ツール呼び出し、失敗、所要時間、起動されたエージェント、プロンプト、応答、Skill の参照を TUI / WebUI で確認できます。
- **Kiro エージェント設定の管理**: `.kiro/agents/*.json` と `.kiro/agent-prompts/*.md` を対話的に作成・更新できます。
- **パッケージ形式の橋渡し**: APM パッケージと Kiro Power を、プロジェクトローカルの `.kiro/` ファイルとして同期します。

## インストール

### Homebrew (macOS / Linux)

```bash
brew install --cask kapmcli/tap/kapm
```

### リリースアーカイブ

[GitHub Releases](https://github.com/kapmcli/kapm/releases) から利用するプラットフォーム向けのアーカイブをダウンロードし、展開した `kapm` または `kapm.exe` を `PATH` に配置してください。

### Nix (Nightly)

```bash
nix profile add github:kapmcli/kapm#kapm
```

### ソースからビルド

```bash
just build
```

## クイックスタート

```bash
# Kiro エージェントを作成または更新
kapm agent generate

# 選択したエージェントに kapm hook を追加
kapm init-hook

# Kiro を実行したあと、記録されたセッションを確認
kapm monitor
kapm serve
```

## 監視

`kapm init-hook` は、選択した `.kiro/agents/*.json` に kapm 管理の hook エントリを追加します。Kiro が hook イベントを発行すると、そのエントリが `kapm hook-handler --agent <name>` を実行します。

hook イベントは `.kapm/logs/{session_id}.jsonl` に JSONL として保存されます。`kapm monitor` はターミナルで、`kapm serve` はローカル WebUI で同じログを表示します。

```bash
kapm init-hook             # エージェントを対話的に選択
kapm init-hook --remove    # kapm 管理の hook を削除

kapm monitor
kapm monitor --json
kapm monitor --json --session <session-id>
kapm monitor --json --session <session-id> --agent <agent-name>

kapm serve
kapm serve --port 9097 --open
```

`monitor` と `serve` は共通で以下を指定できます。

```bash
--since 24h
--logs-dir <path>
--target-dir <path>
```

![WebUI overview](demo-media/webui-overview.png)

![WebUI session detail](demo-media/webui-session-detail.png)

### WebUI ルート

| Route | 説明 |
|---|---|
| `GET /` | Overview ダッシュボード |
| `GET /sessions` | セッション一覧 |
| `GET /sessions/{id}` | マージ済みセッション詳細 |
| `GET /sessions/{id}/{agent}` | エージェント別セッション詳細 |
| `GET /agents` | エージェント一覧 |
| `GET /agents/{name}` | エージェント詳細 |
| `GET /tools` | ツール利用状況 |
| `GET /tools/{name}` | ツール詳細 |
| `GET /skills` | Skill 参照状況 |

## エージェント設定

```bash
kapm agent generate
kapm agent generate --force
kapm agent update <name>
```

`agent generate` は `.kiro/agents/<name>.json` と `.kiro/agent-prompts/<name>.md` を作成します。`agent update` は既存エージェントを編集し、未知の JSON フィールドを保持します。

## APM 互換

```bash
kapm sync
kapm sync --force

kapm install owner/repo
kapm install --update owner/repo
kapm install github/awesome-copilot/skills/review-and-refactor
```

`kapm sync` は、ローカルの `.apm/`、インストール済みの `apm_modules/`、および `apm.yml` の MCP 依存関係を読み込み、Kiro ネイティブなファイルを `.kiro/` 以下に書き出します。既存ファイルは `--force` がない限り上書きしません。

`kapm install` はインストール処理を `apm install` に委譲します。`apm` が見つからない場合は `uvx --from apm-cli==0.9.1 apm install` にフォールバックします。インストール後は同じ同期処理を実行します。

`install` で使える kapm 固有のフラグ:

```bash
--sync-force            # 同期時に .kiro ファイルを上書き
--target-dir <path>     # 同期先のプロジェクトディレクトリを指定
```

`--global` は APM にそのまま渡され、ホームディレクトリを同期ルートとして使います。`--target-dir` とは併用できません。

## Kiro Power 互換

```bash
kapm power install ./local/power
kapm power install owner/repo
kapm power install owner/repo/path/to/power --ref main
kapm power install https://github.com/owner/repo
kapm power install https://github.com/owner/repo/tree/main/path/to/power
```

`power install` は Power パッケージをそのまま `.kiro/powers/<name>/` にコピーします。Skill ファイルの生成、MCP 設定のマージ、hook の有効化は行いません。代わりに、次の作業に必要な具体的なスニペットを表示します。

- `POWER.md` と `steering/*.md` を参照する `file://` resource エントリ
- `mcp.json` が含まれる場合の `mcpServers` 内容
- `hooks/` が含まれる場合に agent の `hooks` へ移植するファイル一覧
- 手動削除コマンド

既存の kapm 管理 Power ディレクトリを上書きするには `--force` を使います。

## 互換マッピング

| Source | Kiro output |
|---|---|
| APM `instructions` | `.kiro/steering/<name>.md` |
| APM `prompts` | `.kiro/prompts/<name>.md` |
| APM `commands` | `.kiro/prompts/<name>.md` |
| APM `skills` | `.kiro/skills/<name>/...` |
| APM `agents` / `chatmodes` | `.kiro/agents/<name>.json` + `.kiro/agent-prompts/<name>.md` |
| APM MCP dependencies | `.kiro/settings/mcp.json` |
| Kiro Power package | `.kiro/powers/<name>/...` |

## ログ形式と保存

JSONL レコードには、必要に応じて `ts`, `agent`, `session`, `event`, `tool`, `tool_input`, `tool_response`, `assistant_response`, `prompt`, `cwd` が含まれます。

ログには、ファイルパス、ソースコード、プロンプト、モデル応答、ツール入出力に含まれる認証情報などが記録される可能性があります。`.kapm/` は gitignore され、ディレクトリは `0700`、ログファイルは `0600` で作成されます。

`agentSpawn` 時に、24 時間以上書き込みのないアイドルセッションログは `.jsonl.gz` に圧縮されます。アクティブなセッションは `.jsonl` のまま残ります。

## 開発

```bash
just build
just test
just lint
```

`just` がない場合:

```bash
go build -o kapm ./cmd/kapm      # macOS / Linux
go build -o kapm.exe ./cmd/kapm  # Windows
```

## Links

- [APM docs](https://microsoft.github.io/apm/) · [APM CLI](https://microsoft.github.io/apm/reference/cli-commands/) · [APM source](https://github.com/microsoft/apm)
- [Kiro prompts](https://kiro.dev/docs/cli/chat/manage-prompts/) · [Kiro skills](https://kiro.dev/docs/skills/) · [Kiro steering](https://kiro.dev/docs/steering/) · [Kiro custom agents](https://kiro.dev/docs/chat/subagents/)
- [design.md](https://github.com/google-labs-code/design.md)
