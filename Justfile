build:
	go build -o internal/agent/kapl ./cmd/kapl
	go build -o kapm ./cmd/kapm

test:
	go test -race ./cmd/... ./internal/...

test-e2e:
	go test -tags=e2e ./internal/e2e/...

fmt:
	find . -name '*.go' -not -path './.git/*' -exec gofmt -w {} +

lint:
	golangci-lint run ./cmd/... ./internal/...

tidy:
	go mod tidy

# Refresh the README/demo media assets.
media-refresh: build media-monitor media-demo media-webui

media-monitor: build
	@which vhs > /dev/null 2>&1 || (echo "vhs not installed" && exit 1)
	PS1='$ ' VHS_NO_SANDBOX=1 KAPM_UPDATED_AT=12:00:00 vhs demo-media/monitor.tape

media-demo: build
	@which vhs > /dev/null 2>&1 || (echo "vhs not installed" && exit 1)
	VHS_NO_SANDBOX=1 vhs demo-media/demo.tape

media-webui port="9097" logs-dir="testdata/monitor/logs" since="8760h": build
	@which npx > /dev/null 2>&1 || (echo "npx not installed" && exit 1)
	bash -lc 'set -euo pipefail; cleanup() { just serve-stop >/dev/null 2>&1 || true; }; trap cleanup EXIT; npx -y playwright@latest install chromium >/dev/null; KAPM_UPDATED_AT=12:00:00 just serve {{port}} {{logs-dir}} {{since}} >/dev/null; sleep 2; npx -y playwright@latest screenshot --browser chromium --viewport-size "1440,1024" --wait-for-timeout 1500 "http://127.0.0.1:{{port}}/" demo-media/webui-overview.png; npx -y playwright@latest screenshot --browser chromium --viewport-size "1440,1024" --wait-for-timeout 1500 "http://127.0.0.1:{{port}}/sessions" demo-media/webui-sessions.png; npx -y playwright@latest screenshot --browser chromium --viewport-size "1440,1024" --wait-for-timeout 1500 "http://127.0.0.1:{{port}}/sessions/cd75bf13-61bf-48ca-94ae-d444e19cb59d" demo-media/webui-session-detail.png; npx -y playwright@latest screenshot --browser chromium --viewport-size "1440,1024" --wait-for-timeout 1500 "http://127.0.0.1:{{port}}/agents" demo-media/webui-agents.png; npx -y playwright@latest screenshot --browser chromium --viewport-size "1440,1024" --wait-for-timeout 1500 "http://127.0.0.1:{{port}}/tools" demo-media/webui-tools.png; npx -y playwright@latest screenshot --browser chromium --viewport-size "1440,1024" --wait-for-timeout 1500 "http://127.0.0.1:{{port}}/skills" demo-media/webui-skills.png'

# Launch the WebUI server in the background, or stop it with `just serve stop`.
# Logs to /tmp/kapm-serve.log, PID to /tmp/kapm-serve.pid.
serve port="9090" logs-dir=".kiro/logs" since="24h":
	#!/usr/bin/env bash
	set -eu
	if [ "{{port}}" = "stop" ]; then
		pkill -f '[k]apm serve' 2>/dev/null || true
		rm -f /tmp/kapm-serve.pid
		echo "stopped"
		exit 0
	fi
	just build
	pkill -f '[k]apm serve' 2>/dev/null || true
	rm -f /tmp/kapm-serve.pid
	nohup ./kapm serve --port {{port}} --logs-dir {{logs-dir}} --since {{since}} > /tmp/kapm-serve.log 2>&1 &
	echo $! > /tmp/kapm-serve.pid
	sleep 0.5
	echo "kapm serve pid $(cat /tmp/kapm-serve.pid) on http://127.0.0.1:{{port}}/ (log: /tmp/kapm-serve.log)"

serve-stop:
	@pkill -f '[k]apm serve' 2>/dev/null && echo "stopped" || echo "not running"
	@rm -f /tmp/kapm-serve.pid

vhs-test: build
	@which vhs > /dev/null 2>&1 || (echo "vhs not installed" && exit 1)
	PS1='$ ' VHS_NO_SANDBOX=1 KAPM_UPDATED_AT=12:00:00 vhs demo-media/monitor.tape
	@grep -q "updated: 12:00:00" demo-media/monitor.ascii || (echo "FAIL: updated timestamp missing" && exit 1)
	@grep -q "1 Overview" demo-media/monitor.ascii || (echo "FAIL: Overview tab missing" && exit 1)
	@grep -q "Top tools" demo-media/monitor.ascii || (echo "FAIL: Top tools missing" && exit 1)
	@grep -q "Last act" demo-media/monitor.ascii || (echo "FAIL: Sessions tab missing" && exit 1)
	@echo "vhs-test PASS"

vhs:
	@which vhs > /dev/null 2>&1 || (echo "vhs not installed" && exit 1)
	PS1='$ ' VHS_NO_SANDBOX=1 KAPM_UPDATED_AT=12:00:00 vhs demo-media/monitor.tape
	git diff --exit-code demo-media/monitor.ascii
