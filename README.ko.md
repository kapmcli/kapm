<h1 align="center">kapm</h1>

<p align="center">
  Kiro 에이전트 프로젝트를 위한 관측성 도구와 패키지 호환 도구입니다.
</p>

<p align="center">
  <a href="https://github.com/kapmcli/kapm/actions/workflows/ci.yml"><img alt="CI" src="https://github.com/kapmcli/kapm/actions/workflows/ci.yml/badge.svg"></a>
  <a href="https://go.dev/"><img alt="Go version" src="https://img.shields.io/github/go-mod/go-version/kapmcli/kapm"></a>
  <a href="LICENSE"><img alt="License: MIT" src="https://img.shields.io/badge/License-MIT-blue.svg"></a>
</p>

<p align="center">
  <a href="README.md">English</a> · <a href="README.ja.md">日本語</a> · 한국어 · <a href="README.zh-CN.md">简体中文</a>
</p>

<p align="center">
  <img src="demo-media/demo.gif" alt="kapm demo" />
</p>

## kapm이 하는 일

kapm은 Kiro 에이전트 워크스페이스를 이해하고 관리하기 쉽게 만드는 CLI입니다.

- **Kiro 세션 모니터링**: hook 이벤트를 `.kapm/logs`에 기록하고, 세션, 도구 호출, 실패, 소요 시간, 생성된 에이전트, 프롬프트, 응답, Skill 읽기 정보를 TUI와 WebUI에서 확인합니다.
- **Kiro 에이전트 관리**: `.kiro/agents/*.json`과 `.kiro/agent-prompts/*.md`를 대화형으로 만들고 업데이트합니다.
- **패키지 형식 연결**: APM 패키지와 Kiro Power를 프로젝트 로컬 `.kiro/` 파일로 동기화합니다.

## 설치

### Homebrew (macOS)

```bash
brew install --cask kapmcli/tap/kapm
```

### 릴리스 아카이브

[GitHub Releases](https://github.com/kapmcli/kapm/releases)에서 사용하는 플랫폼에 맞는 아카이브를 내려받아 압축을 풀고, `kapm` 또는 `kapm.exe`를 `PATH`에 추가하세요.

### 소스에서 빌드

```bash
just build
```

## 빠른 시작

```bash
# Kiro 에이전트를 만들거나 업데이트합니다.
kapm agent generate

# 선택한 에이전트에 kapm hook을 설치합니다.
kapm init-hook

# Kiro를 실행한 뒤 기록된 세션을 확인합니다.
kapm monitor
kapm serve
```

## 모니터링

`kapm init-hook`은 선택한 `.kiro/agents/*.json` 파일에 kapm이 관리하는 hook 항목을 추가합니다. Kiro가 hook 이벤트를 내보내면 이 항목이 `kapm hook-handler --agent <name>`을 실행합니다.

hook 이벤트는 `.kapm/logs/{session_id}.jsonl`에 JSONL로 저장됩니다. `kapm monitor`는 터미널에서, `kapm serve`는 로컬 WebUI에서 같은 로그를 보여줍니다.

```bash
kapm init-hook             # 에이전트를 대화형으로 선택
kapm init-hook --remove    # kapm 관리 hook 항목 제거

kapm monitor
kapm monitor --json
kapm monitor --json --session <session-id>
kapm monitor --json --session <session-id> --agent <agent-name>

kapm serve
kapm serve --port 9097 --open
```

`monitor`와 `serve`는 공통으로 다음 옵션을 지원합니다.

```bash
--since 24h
--logs-dir <path>
--target-dir <path>
```

![WebUI overview](demo-media/webui-overview.png)

![WebUI session detail](demo-media/webui-session-detail.png)

### WebUI routes

| Route | 설명 |
|---|---|
| `GET /` | Overview 대시보드 |
| `GET /sessions` | 세션 목록 |
| `GET /sessions/{id}` | 병합된 세션 상세 |
| `GET /sessions/{id}/{agent}` | 에이전트별 세션 상세 |
| `GET /agents` | 에이전트 목록 |
| `GET /agents/{name}` | 에이전트 상세 |
| `GET /tools` | 도구 사용량 |
| `GET /tools/{name}` | 도구 상세 |
| `GET /skills` | Skill 읽기 정보 |

## 에이전트 설정

```bash
kapm agent generate
kapm agent generate --force
kapm agent update <name>
```

`agent generate`는 `.kiro/agents/<name>.json`과 `.kiro/agent-prompts/<name>.md`를 만듭니다. `agent update`는 기존 에이전트를 편집하며 알 수 없는 JSON 필드는 보존합니다.

## APM 호환

```bash
kapm sync
kapm sync --force

kapm install owner/repo
kapm install --update owner/repo
kapm install github/awesome-copilot/skills/review-and-refactor
```

`kapm sync`는 로컬 `.apm/`, 설치된 `apm_modules/`, `apm.yml`의 MCP 의존성을 읽고 Kiro 네이티브 파일을 `.kiro/` 아래에 씁니다. 기존 파일은 `--force`를 사용하지 않으면 건너뜁니다.

`kapm install`은 설치를 `apm install`에 위임합니다. `apm`을 찾을 수 없으면 `uvx --from apm-cli==0.9.1 apm install`로 폴백합니다. 설치 후에는 같은 sync 단계를 실행합니다.

`install`에서 사용할 수 있는 kapm 전용 플래그:

```bash
--sync-force            # sync 단계에서 .kiro 파일 덮어쓰기
--target-dir <path>     # 동기화할 프로젝트 디렉터리 지정
```

`--global`은 APM으로 그대로 전달되며 홈 디렉터리를 sync 루트로 사용합니다. `--target-dir`와 함께 사용할 수 없습니다.

## Kiro Power 호환

```bash
kapm power install ./local/power
kapm power install owner/repo
kapm power install owner/repo/path/to/power --ref main
kapm power install https://github.com/owner/repo
kapm power install https://github.com/owner/repo/tree/main/path/to/power
```

`power install`은 원본 Power 패키지를 `.kiro/powers/<name>/`에 복사합니다. Skill 파일을 생성하거나 MCP 설정을 병합하거나 hook을 활성화하지 않습니다. 대신 다음 작업에 필요한 구체적인 스니펫을 출력합니다.

- `POWER.md`와 `steering/*.md`를 가리키는 `file://` resource 항목
- Power에 `mcp.json`이 있을 때의 `mcpServers` 내용
- Power에 `hooks/`가 있을 때 agent의 `hooks`로 옮길 파일 목록
- 수동 삭제 명령

기존 kapm 관리 Power 디렉터리를 덮어쓰려면 `--force`를 사용하세요.

## 호환 매핑

| Source | Kiro output |
|---|---|
| APM `instructions` | `.kiro/steering/<name>.md` |
| APM `prompts` | `.kiro/prompts/<name>.md` |
| APM `commands` | `.kiro/prompts/<name>.md` |
| APM `skills` | `.kiro/skills/<name>/...` |
| APM `agents` / `chatmodes` | `.kiro/agents/<name>.json` + `.kiro/agent-prompts/<name>.md` |
| APM MCP dependencies | `.kiro/settings/mcp.json` |
| Kiro Power package | `.kiro/powers/<name>/...` |

## 로그 형식과 보관

각 JSONL 레코드는 필요에 따라 `ts`, `agent`, `session`, `event`, `tool`, `tool_input`, `tool_response`, `assistant_response`, `prompt`, `cwd`를 포함합니다.

로그에는 파일 경로, 소스 코드, 프롬프트, 모델 응답, 도구 입출력에 포함된 인증 정보가 기록될 수 있습니다. `.kapm/`은 gitignore 처리되며, 디렉터리는 `0700`, 로그 파일은 `0600` 권한으로 생성됩니다.

`agentSpawn` 시점에 24시간 넘게 쓰이지 않은 idle 세션 로그는 `.jsonl.gz`로 압축됩니다. 활성 세션은 `.jsonl`로 유지됩니다.

## 개발

```bash
just build
just test
just lint
```

`just`를 사용할 수 없다면:

```bash
go build -o kapm ./cmd/kapm      # macOS / Linux
go build -o kapm.exe ./cmd/kapm  # Windows
```

`DESIGN.md`는 WebUI 디자인 시스템의 원본 문서입니다. `internal/serve/DESIGN.md`는 `/design-preview`에 포함되는 생성된 복사본입니다.

## Links

- [APM docs](https://microsoft.github.io/apm/) · [APM CLI](https://microsoft.github.io/apm/reference/cli-commands/) · [APM source](https://github.com/microsoft/apm)
- [Kiro prompts](https://kiro.dev/docs/cli/chat/manage-prompts/) · [Kiro skills](https://kiro.dev/docs/skills/) · [Kiro steering](https://kiro.dev/docs/steering/) · [Kiro custom agents](https://kiro.dev/docs/chat/subagents/)
