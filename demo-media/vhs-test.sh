#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

go build -o internal/agent/kapl ./cmd/kapl
go build -o kapm ./cmd/kapm

PS1='$ ' VHS_NO_SANDBOX=1 KAPM_UPDATED_AT=12:00:00 vhs demo-media/monitor.tape

grep -q "updated: 12:00:00" demo-media/monitor.ascii || { echo "FAIL: updated timestamp missing"; exit 1; }
grep -q "1 Overview" demo-media/monitor.ascii || { echo "FAIL: Overview tab missing"; exit 1; }
grep -q "Top tools" demo-media/monitor.ascii || { echo "FAIL: Top tools missing"; exit 1; }
grep -q "Last act" demo-media/monitor.ascii || { echo "FAIL: Sessions tab missing"; exit 1; }

echo "vhs-test PASS"
