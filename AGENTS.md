# AGENTS.md

Instructions for coding agents working in this repository. Keep this file current: when a command, a package layout or a rule changes, update it in the same commit.

## Project

logbook is a shared memory for agents. Agents and people read and write short memories over MCP (stdio and Streamable HTTP) or REST under `/v1/`, and admins run it through a JSON admin API under `/admin/v1/`. Memories belong to a user, project or team scope taken from the caller's token, and carry a trust state (unverified, verified, disputed, superseded, forgotten), a staleness date and typed links. Storage is SQLite by default and Postgres for teams. Embeddings and fact extraction come from transponder or any OpenAI-compatible endpoint when configured. The design is in `docs/design.md`.

It is one of four projects described in the shared contract at https://github.com/raven-clown/mothership/blob/main/CONTRACT.md. Follow the contract. A change to how the projects connect goes to the contract first.

## Commands

```shell
go build ./...
go test -race ./...
golangci-lint run ./...
make check   # lint plus tests with the 80% coverage check
go run ./cmd/logbook mcp           # MCP over stdio on ./logbook.db
go run ./cmd/logbook recall "query"
```

`LOGBOOK_NOW` (an RFC 3339 time) fixes the clock of the `logbook` command. It exists for tests and recorded demos; do not use it anywhere else.

`-race` needs cgo. On a machine without a C compiler, run `go test ./...` locally and let CI run the race detector.

## Layout

The layout from `docs/design.md` section 5. Built so far: `cmd/logbook`, `memory`, `staleness`, `store`, `store/migrate`, `store/sqlite`, `store/storetest`, `importer`, `importer/jsonl`, `internal/config`, `internal/mcpserver`. The rest is planned.

| Package | Holds |
|---|---|
| `cmd/logbook` | flags, subcommands, wiring |
| `memory` | memory types, `Service`, scope, trust and ranking rules |
| `admin` | settings, bindings, API keys, scopes, audit, usage, jobs |
| `store`, `store/sqlite`, `store/postgres`, `store/migrate`, `store/storetest` | storage interface, backends, migrations, conformance suite |
| `vector`, `vector/memory`, `vector/pgvector` | vector index interface and backends |
| `embed`, `embed/openai` | embedder interface and the OpenAI-compatible client |
| `extract`, `extract/openai` | fact extraction interface and client |
| `staleness` | staleness rule interface and built-in rules |
| `importer`, `importer/jsonl`, `importer/markdown` | importers |
| `internal/identity`, `internal/settings`, `internal/mcpserver`, `internal/rest`, `internal/adminapi`, `internal/jobs`, `internal/obs` | server internals |
| `api/openapi.yaml` | OpenAPI file for `/v1/` and `/admin/v1/` |

Plugins register by name in `init`, like `database/sql` drivers, and are enabled with a blank import in `cmd/logbook`.

The design is `docs/design.md`, milestone issues are drafted in `docs/issues.md`, and implementation plans live in `docs/plans/`.

## Rules

- Follow CONTRIBUTING.md: tests with every change, a failing test first for a bug fix, table-driven tests, no network in unit tests, no bare `time.Sleep`.
- Commit after every change, with a Conventional Commits message, and push it.
- Never mention AI, assistants or code generation in code, comments, docs, commit messages, pull requests or issues.
- Never add `Co-Authored-By`, `Generated with` or similar trailers.
- No em-dashes. Keep comments short and about why.
- Every feature ships in the free build. Never add a license check or a paid-only code path.
- Update CHANGELOG.md under Unreleased for anything users will notice, and update README.md when usage changes.
