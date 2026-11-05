# Changelog

All notable changes are listed here. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions follow [Semantic Versioning](https://semver.org/).

## [Unreleased]

## [0.1.1] - 2026-11-05

### Fixed

- A forgotten memory is no longer returned by `GET /v1/memories/{id}`, so its content stays hidden during the retention period.
- `SQLITE_BUSY` and `SQLITE_LOCKED`, caused by another process writing to the same file, are reported as unavailable (503) instead of internal, and recording an API key's last use waits at most 500 ms.
- The server has a 30 second read timeout, so a client that trickles a request body can't hold a connection open forever.

## [0.1.0] - 2026-11-05

### Added

- SQLite storage with full-text search and schema migrations that run at startup.
- Memory service with trust states, corrections, conflict detection, staleness and a review queue.
- MCP tools `remember`, `recall`, `verify`, `forget` and `list_memories`.
- JSONL import and export.
- The `logbook` command: `mcp`, `remember`, `recall`, `list`, `verify`, `forget`, `import`, `export`, `migrate`, `version`.
- `logbook serve`: MCP over Streamable HTTP at `/mcp`, REST under `/v1/`, `/healthz`, `/readyz` and `/metrics`.
- Sign-in with OIDC tokens or logbook API keys, role bindings for teams and projects, and a restricted project mode.
- `logbook key` and `logbook binding` to manage API keys and bindings.
- `--server` and `--token` on the memory commands to work against a running server.
- JSON logs with request and trace IDs, Prometheus metrics, and OTLP trace export.
- `GET /v1/memories/{id}` shows a memory with its links, verifications and history; `POST /v1/memories/{id}/links` links two memories.

[Unreleased]: https://github.com/raven-clown/logbook/compare/v0.1.1...HEAD
[0.1.1]: https://github.com/raven-clown/logbook/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/raven-clown/logbook/releases/tag/v0.1.0
