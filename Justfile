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
	VHS_NO_SANDBOX=1 KAPM_UPDATED_AT=12:00:00 vhs vhs/monitor.tape
	@grep -q "updated: 12:00:00" vhs/monitor.ascii || (echo "FAIL: updated timestamp missing" && exit 1)
	@grep -q "1 Overview" vhs/monitor.ascii || (echo "FAIL: Overview tab missing" && exit 1)
	@grep -q "Top tools" vhs/monitor.ascii || (echo "FAIL: Top tools missing" && exit 1)
	@grep -q "Last act" vhs/monitor.ascii || (echo "FAIL: Sessions tab missing" && exit 1)
	@echo "vhs-test PASS"

vhs:
	@which vhs > /dev/null 2>&1 || (echo "vhs not installed" && exit 1)
	VHS_NO_SANDBOX=1 KAPM_UPDATED_AT=12:00:00 vhs vhs/monitor.tape
	git diff --exit-code vhs/monitor.ascii
