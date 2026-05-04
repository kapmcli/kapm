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

- **Kiro 세션 모니터링**: CLI 세션 로그(`~/.kiro/sessions/cli/`), Kiro IDE 세션 로그(자동 감지), v1 SQLite 세션 스토어를 읽고, 도구 호출 타임스탬프·에이전트 귀속·셸 종료 상태를 위해 `.kapm/logs/`의 선택적 hook 로그를 보조로 활용합니다. 세션, 도구 호출, 실패, 소요 시간, 에이전트, 프롬프트, 응답, 파일 변경, Skill 읽기 정보를 TUI와 WebUI에서 확인합니다.
- **Kiro 에이전트 관리**: `.kiro/agents/*.json`과 `.kiro/agent-prompts/*.md`를 대화형으로 만들고 업데이트합니다.
- **패키지 형식 연결**: APM 패키지와 Kiro Power를 프로젝트 로컬 `.kiro/` 파일로 동기화합니다.

## 설치

### Homebrew (macOS / Linux)

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
# Kiro를 실행한 뒤 기록된 세션을 확인합니다.
kapm monitor
kapm serve

# Kiro 에이전트를 만들거나 업데이트합니다.
kapm agent generate

# 선택한 에이전트에 kapm hook을 설치합니다.
kapm init-hook
# Kiro IDE용 kapm hook 파일을 설치합니다.
kapm init-ide-hook
```

## 모니터링

kapm은 Kiro CLI v2 세션 데이터(`~/.kiro/sessions/cli/{uuid}.jsonl` 로그와 `{uuid}.json` 메타데이터)를 기본 데이터 소스로 읽습니다. Kiro IDE 세션 로그도 사용 가능한 경우 자동으로 불러옵니다. Kiro CLI v1 SQLite 세션 스토어(`conversations_v2`)도 있으면 읽습니다. 기본 모니터링에는 hook 설치가 필요 없으며, 세션 파일에는 프롬프트, 어시스턴트 응답, 도구 호출, 도구 결과, 턴별 메타데이터(토큰, 크레딧, 소요 시간)가 포함됩니다.

`kapm init-hook`은 선택적으로 `.kiro/agents/*.json`에 hook 항목을 추가해 CLI 에이전트 보조 데이터를 수집합니다. `kapm init-ide-hook`은 Kiro IDE hook 파일을 `.kiro/hooks/*.kiro.hook`에 씁니다. CLI hook은 `.kapm/logs/cli/`에, IDE hook은 `.kapm/logs/ide/`에 최소 레코드를 씁니다.

```bash
kapm init-hook             # 에이전트를 대화형으로 선택
kapm init-hook --global    # ~/.kiro/agents 아래의 전역 에이전트를 선택
kapm init-hook --remove    # kapm 관리 hook 항목 제거
kapm init-ide-hook         # 워크스페이스 Kiro IDE hook 파일 설치
kapm init-ide-hook --remove # kapm 관리 IDE hook 파일 제거

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
--global                   # 모든 프로젝트의 세션 표시 (기본값: 현재 디렉터리만)
--logs-dir <path>
--target-dir <path>
--ide-sessions-dir <path>  # IDE 세션 디렉토리 지정 (기본값: 자동 감지)
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

도구 사용량은 알려진 도구 별칭을 통합하여 집계하며, 도구 상세에서는 관찰된 별칭 분포를 보여줍니다.

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

hook 로그는 최소한의 형식을 사용합니다. CLI 레코드에는 `ts`, `session`, `event`, `agent`, `tool`, 그리고 선택적으로 `shell_exit_status`가 포함되고, IDE 레코드에는 `ts`, `event`, `agent`, `cwd`가 포함됩니다. 프롬프트, 도구 입출력, 어시스턴트 응답은 hook 로그가 아닌 Kiro의 세션 파일에서 읽습니다.

`.kapm/`은 gitignore 처리되며, 디렉터리는 `0700`, 로그 파일은 `0600` 권한으로 생성됩니다.

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

## Links

- [APM docs](https://microsoft.github.io/apm/) · [APM CLI](https://microsoft.github.io/apm/reference/cli-commands/) · [APM source](https://github.com/microsoft/apm)
- [Kiro prompts](https://kiro.dev/docs/cli/chat/manage-prompts/) · [Kiro skills](https://kiro.dev/docs/skills/) · [Kiro steering](https://kiro.dev/docs/steering/) · [Kiro custom agents](https://kiro.dev/docs/chat/subagents/)
- [design.md](https://github.com/google-labs-code/design.md)
