---
name: kapm
version: alpha
description: kapm WebUI theme — dark purple accent on deep navy
colors:
  bg: "#1a1a2e"
  bg-2: "#16213e"
  card: "#1e1e3f"
  accent: "#7D56F4"
  success: "#04B575"
  error: "#E06C75"
  warning: "#E5C07B"
  text: "#FAFAFA"
  muted: "#6B6B6B"
  chart: "#61AFEF"
---

## Overview

kapm WebUI は deep navy 背景に purple accent を重ねた dark theme。
観測系ツールとして静かで落ち着いた配色を採用する。

## Colors

- **bg (#1a1a2e)**: ページ全体の背景。夜間の落ち着き
- **bg-2 (#16213e)**: 代替背景。サイドパネルや section break に利用
- **card (#1e1e3f)**: カード・ヘッダー背景。bg より明度わずか上
- **accent (#7D56F4)**: リンク、アクティブ nav、主要な強調
- **success (#04B575)**: 成功系 status バッジ
- **error (#E06C75)**: 失敗系 status バッジ
- **warning (#E5C07B)**: 注意系 status バッジ
- **text (#FAFAFA)**: 本文。warm off-white
- **muted (#6B6B6B)**: セカンダリテキスト、timestamp 等
- **chart (#61AFEF)**: echarts bar の色

## serve Security Model

`kapm serve` binds to `127.0.0.1` (localhost only) and does not implement authentication.

**Design rationale:**
- The dashboard is a local developer tool, not a network service.
- Binding to localhost prevents remote access by default.
- No sensitive credentials are stored or transmitted — only session metadata and agent activity logs.

**Limitations:**
- On multi-user systems, any local user can access the dashboard on the configured port.
- If localhost binding is changed (e.g., via reverse proxy), authentication should be added at the proxy layer.