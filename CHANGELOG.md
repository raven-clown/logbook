# Changelog

All notable changes are listed here. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions follow [Semantic Versioning](https://semver.org/).

## [Unreleased]

### Added

- `logbook import --dry-run` reads a file and prints how many records would be imported and which lines would be skipped. It writes nothing, and it does not call `--server`.

## [0.2.0] - 2026-11-21

### Added

- Embeddings through any OpenAI-compatible `/v1/embeddings` endpoint (`embedder` settings). Recall fuses full-text and vector results, so it finds memories by meaning.
- `remember` with `extract: true` stores up to 10 facts pulled out of raw text through any OpenAI-compatible `/v1/chat/completions` endpoint (`extractor` settings), each with `source: extracted` and its sentence as evidence.
- New memories get `related` links to up to three near neighbours in their scope. A memory without a `subject` that the extractor finds contradicts a close neighbour marks the older one `disputed`.
- A background loop in `logbook serve` and `logbook mcp` embeds memories whose embedding failed, and `logbook reindex` embeds every memory again after a model change.
- Verify policies per scope type: under `trust.verify_policy.team: verifiers`, only confirms from a `verifier` binding count.
- A default `min_status` per scope type, so a team scope can return verified memories only.
- Trust weights, review periods and `forget_retention` can be set in the config file and environment.
- Forgotten memories are purged after `forget_retention`, 30 days by default.
- The embedder and extractor forward `X-Request-ID` and the W3C trace context, and the caller's OIDC token when `forward_token` is set. logbook API keys are never forwarded.
- Each release attaches `logbook` binaries for Linux, macOS and Windows on amd64 and arm64 with `checksums.txt`, and pushes a container image to `ghcr.io/raven-clown/logbook` that runs `logbook serve` on port 8420 with SQLite in `/data`.

### Changed

- `/readyz` reports `embedder` and `extractor` in place of `transponder`.

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

[Unreleased]: https://github.com/raven-clown/logbook/compare/v0.2.0...HEAD
[0.2.0]: https://github.com/raven-clown/logbook/compare/v0.1.1...v0.2.0
[0.1.1]: https://github.com/raven-clown/logbook/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/raven-clown/logbook/releases/tag/v0.1.0
