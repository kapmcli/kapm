# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.1.0] - TBD

### Added
- `SessionMetric.AgentKey` — composite key `{sid}|{agent}` for multi-agent sessions (#7)
- `SessionMetric.Title` — first userPromptSubmit prompt excerpt, 120 chars (#15)
- New route `GET /sessions/{id}/{agent}` — per-agent session detail view
- `internal/monitor/merge.go` — `MergeSessionDetails` helper for per-sid aggregation

### Changed
- `kapm serve` and `kapm monitor` now log a warning at startup when
  `.kiro` is a symlink. This is informational only; startup proceeds.
- hook-handler now emits a structured `slog.Warn` when rejecting oversized events in addition to the existing stderr message.
- `kapm agent generate` now writes the agent JSON and prompt files atomically as a pair; partial failures no longer leave half-written files.
- **BREAKING**: `/api/metrics?session=<sid>` now returns a merged SessionDetail (Agent="(all)") instead of a single-agent detail. Use `?session=<sid>` alone for merged, or filter by `?agent=<name>` for per-agent narrowing. (#7)
- **BREAKING**: `kapm monitor --json --session=<sid>` now emits the merged SessionDetail. Combine with `--agent=<name>` to narrow to a single agent. (#7)
- Aggregation is now keyed by `(session, agent)`; same sid with multiple agents produces multiple SessionDetail entries
- Web UI timestamps are emitted as UTC ISO strings and rendered in the viewer's browser locale (#17)
- Web UI durations now use integer-second formatting (`12s`, `1m 3s`) instead of Go's default fractional seconds (#16)
- Empty agent values in records are normalized to `"(unknown)"` at ingest

### Notes
- `JSONDuration.MarshalJSON` output is **unchanged** (still nanosecond Go-duration strings) — JSON API consumers reading duration values see no difference
- TUI output behavior for timestamps and durations is unchanged
