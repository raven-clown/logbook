# logbook design

Status: draft for review, 2026-11-04.

logbook is a shared memory for agents. Any agent can write what it learned and read what others learned, over MCP or a small REST API. It runs as one Go binary with embedded SQLite storage. It works with no model at all, and gets better search and fact extraction when it is pointed at transponder or any OpenAI-compatible endpoint.

It is one of the four projects in `mothership/CONTRACT.md` and follows that contract. Where this spec needs something the contract does not say yet, it is listed under "Contract questions" at the end.

## 1. Goals

- A memory that more than one agent and more than one person can share, with access decided by the caller's token, not by a parameter the caller fills in.
- Memories carry trust. Each one says whether someone checked it, whether it was corrected, whether it may be out of date, and what it is linked to. Recall uses all of that when it ranks and filters.
- One binary, one data file, a useful demo in under five minutes, on Windows, macOS and Linux.
- Clear plugin points so outside contributors can add storage backends, embedding providers, extractors, importers and staleness rules without touching the core.

Non-goals for v1: a web UI, clustering or replication, an entity graph built by a model (the graphiti and cognee approach), encryption at rest, and per-memory access lists below the scope level.

## 2. What logbook does that mem0 and engram don't

Both projects are good and worth learning from. This comparison is from their READMEs as of November 2026.

| | mem0 (open source) | engram | logbook |
|---|---|---|---|
| Runtime | Python library or Docker Compose stack | Go binary, SQLite | Go binary, SQLite |
| Needs a model to store anything | Yes, extraction runs on every add | No | No |
| Search | Vector and BM25 hybrid | SQLite FTS5 | FTS5 plus vectors when an embedder is set, fused |
| MCP transports | Through its server stack | stdio, plus a local HTTP API | stdio and Streamable HTTP |
| Who owns a memory | `user_id` passed by the caller | Project, detected from the working directory | Scope derived from a verified token (OIDC JWT or API key) and enforced |
| Trust | ADD-only, every fact has equal weight | Agent-driven review and judge tools | Explicit trust state, who confirmed it and how, recall can require it |
| Corrections | Old facts stay and accumulate | `topic_key` updates in place | Supersede chain kept; recall returns the current fact and says what it replaced |
| Staleness | Time-aware retrieval | Review of stale knowledge | Review date per kind, stale flag in results, review queue, pluggable staleness rules |
| Links | Entity linking for retrieval | Obsidian export | Typed edges between memories, returned by recall |

In short:

1. Team memory with real access control. engram is built for one developer on one machine. mem0 trusts whatever `user_id` it is given. logbook reads identity from the bearer token, so a team can run one instance behind airlock and each person sees their own memories, their project's and their teams'.
2. Trust is part of the data model. A memory is `unverified` until someone other than its writer confirms it. A team can set its scope to return only verified memories, which also limits the damage when an agent stores something wrong or something planted by a prompt injection.
3. Corrections are kept and used. When a fact is corrected, the old one is marked `superseded` and linked to the new one. If a query matches the old wording, recall still returns the new fact with a note about what changed. Nothing is silently overwritten and nothing silently piles up.
4. It notices when facts get old. Each kind of memory has a review period. Past it, a memory is flagged `stale` in results and shows up in a review queue. Confirming it resets the clock. Contributors can add rules, such as "stale when a referenced file changed in git".
5. It fits the mothership stack. It sits behind airlock for auth and audit, and calls transponder for embeddings and extraction, forwarding the caller's token so cost lands on the right person. It also runs fine alone.

engram already has conflict surfacing and review tools, so logbook does not claim to be the first to think about stale or conflicting memories. The difference is that logbook stores trust and corrections as state that ranking and filtering rely on, and does it for many callers at once.

## 3. Concepts

### Memory

A short piece of text that is worth remembering, plus metadata.

| Field | Meaning |
|---|---|
| `id` | `mem_` plus a ULID, sortable by creation time |
| `scope` | `user:<subject>`, `project:<id>` or `team:<id>` |
| `content` | The text, up to 8 KiB |
| `kind` | `fact`, `preference`, `decision`, `procedure` or `observation` |
| `subject` | Optional topic key such as `ci.go-version`. Two active memories with the same subject in the same scope are treated as competing answers |
| `tags` | Free-form labels |
| `refs` | Optional file paths or URLs the memory is about. Used by staleness rules |
| `status` | `unverified`, `verified`, `disputed`, `superseded` or `forgotten` |
| `source` | `direct`, `extracted` or `imported` |
| `evidence` | Optional text: a command output, a link, the sentence it was extracted from |
| `author` | Subject, channel (`mcp`, `rest`, `cli`) and client name of the writer |
| `created_at`, `updated_at` | Timestamps |
| `valid_until` | Optional hard expiry given by the writer |
| `review_after` | When the memory becomes stale. Computed from the kind, reset on confirm |
| `last_confirmed_at`, `confirmations`, `disputes` | Verification summary |

### Scope

Every memory lives in exactly one scope. Read and write rules:

- `user:<sub>`: only that subject.
- `project:<id>`: in `open` mode (the default) any authenticated caller; in `claim` mode only callers whose token lists the project in the configured claim.
- `team:<id>`: only callers whose token lists the team in the configured claim (default `groups`). Static API keys list their teams in config.

If the caller does not name a scope, `remember` writes to the current project when one is known and to the user scope otherwise. `recall` searches every scope the caller can read, and each result says which scope it came from.

The current project comes from the `project` argument, then the `X-Logbook-Project` header, then the server's `default_project`. In stdio mode the default is the name of the enclosing git repository, or the working directory name.

### Trust states

```
            confirm (counted)
 unverified ----------------> verified
     |  \                      |   ^
     |   \ dispute             |   | confirm (counted)
     |    v                    v   |
     |   disputed <------------+---+
     |      (dispute, or a conflicting memory arrives)
     |
     +-- corrected by another memory --> superseded   (from any active state)
     +-- forget ----------------------> forgotten    (from any state)
```

- A confirm counts unless it comes from the same subject over the same channel as the author. So an agent cannot confirm its own write in the same breath, but a person can confirm through the CLI what their agent wrote over MCP, and two agents with different keys can confirm each other. This is a trust signal and a guard against rubber-stamping. It is not access control, and the README will say so.
- Status follows the latest counted verdict. Counts of confirms and disputes are kept for display.
- `stale` is not a status. It is computed at read time from `review_after`, `valid_until` and any staleness rules, so a verified memory can also be stale.
- Who may call verify is configurable per scope type: anyone with write access (default), or members of listed groups.

### Corrections

`remember` with `corrects: <id>` stores the new memory, marks the old one `superseded`, and adds a `supersedes` edge. The old content is kept. When a recall query matches a superseded memory better than its replacement, the replacement is returned with `corrected_from` holding the old text and the date it changed. That is how a correction keeps paying off: the next agent that asks the question the old way still gets the new answer and learns that the old one was wrong.

### Conflicts

When a new memory has the same `subject` as an active memory in the same scope and different content, and `corrects` is not set, both stay, the older one becomes `disputed`, and a `contradicts` edge links them. The `remember` result lists the conflict so the agent can decide whether it meant to correct.

When an extractor is configured, memories without a subject are also checked: the top three vector neighbours above a similarity of 0.85 are sent to the extractor, which answers whether they contradict the new memory. This check has a two-second budget and is skipped on timeout.

### Links

Edges are typed and directed: `supersedes`, `contradicts`, `related`, `derived_from`. `related` edges come from the caller (`related_to` on remember, or the REST links endpoint) and, when embeddings are on, from near neighbours above a similarity of 0.80, capped at three per memory. `recall` with `expand: 1` returns one hop of neighbours for each result.

### Staleness

Default review periods, set in config:

| kind | review after |
|---|---|
| `observation` | 30 days |
| `fact` | 180 days |
| `procedure` | 180 days |
| `decision` | 365 days |
| `preference` | never |

A staleness rule is a plugin that can mark a memory stale with a reason. v1 ships the time rule above and the `valid_until` rule. The interface is there so a contributor can add a git rule ("a file in `refs` changed after `last_confirmed_at`") or a URL rule.

### Duplicates

Exact duplicates (same normalized content in the same scope) are not stored twice. `remember` returns the existing id with `duplicate: true`.

## 4. Interfaces

### MCP tools

Served over stdio (`logbook mcp`) and Streamable HTTP at `/mcp`, built on the official Go MCP SDK (`github.com/modelcontextprotocol/go-sdk`). Tool results are JSON in a text content block, plus `structuredContent` with an output schema.

`remember`
- in: `content` (required), `kind`, `subject`, `scope` (`user`, `project`, `team`), `project`, `team`, `tags`, `refs`, `corrects`, `related_to`, `valid_until`, `evidence`, `extract` (bool)
- with `extract: true`, `content` is raw text such as a conversation turn. The extractor pulls out up to 10 facts and each one is stored with `source: extracted` and the sentence it came from as `evidence`. Without an extractor this returns an error that says so.
- out: `memories: [{id, scope, status, duplicate, conflicts, superseded}]`, `degraded`

`recall`
- in: `query` (required), `scopes`, `project`, `team`, `kinds`, `tags`, `limit` (default 8, max 50), `min_status` (`unverified` or `verified`), `include_stale` (default true), `expand` (0 or 1), `as_of` (RFC 3339)
- `as_of` answers "what did we believe then": memories created before that time and not yet superseded or forgotten at that time.
- out: `results: [{id, content, kind, subject, scope, status, stale, stale_reason, score, confirmations, last_confirmed_at, created_at, author, corrected_from, links}]`, `degraded`

`forget`
- in: `id` (required), `reason`, `purge` (bool)
- soft delete by default: hidden from recall and list, content kept for `forget_retention` (default 30 days) and then purged by a background sweep. `purge: true` removes the content now. Allowed for the author and for anyone with write access to a project or team scope.
- out: `{id, status}`

`list_memories`
- in: `scopes`, `project`, `team`, `status`, `kind`, `subject`, `tag`, `author`, `stale` (bool), `order` (`newest` or `review`), `limit` (default 50, max 200), `cursor`
- `order: review` puts disputed first, then stale, then unverified. That is the review queue.
- out: `{items, next_cursor}`

`verify` (addition to the contract's list, see section 13)
- in: `id` (required), `verdict` (`confirm` or `dispute`), `note`
- out: `{id, status, confirmations, disputes, counted}`

### REST under `/v1/`

Same service, same rules, JSON in and out.

| Method and path | Operation |
|---|---|
| `POST /v1/memories` | remember |
| `POST /v1/recall` | recall |
| `GET /v1/memories` | list_memories, filters as query parameters |
| `GET /v1/memories/{id}` | one memory with its edges, verifications and history |
| `DELETE /v1/memories/{id}` | forget, `?purge=true` |
| `POST /v1/memories/{id}/verify` | verify |
| `POST /v1/memories/{id}/links` | add a `related` edge |
| `GET /v1/export` | JSONL stream of the caller's readable memories |

Errors use one shape: `{"error": {"code": "not_found", "message": "..."}}` with codes `invalid_argument`, `unauthenticated`, `permission_denied`, `not_found`, `conflict`, `unavailable`, `internal`. MCP tool errors carry the same code and message with `isError: true`.

Also `/healthz`, `/readyz` and `/metrics` as the contract requires.

### CLI

```
logbook serve   --config logbook.yaml        HTTP: MCP at /mcp, REST at /v1, health, metrics
logbook mcp     --config logbook.yaml        MCP over stdio, local identity
logbook remember | recall | list | forget | verify   one operation, against the local file or --server URL
logbook review                               walk the review queue and confirm, dispute or skip each item
logbook import  <format> <path>              built-in formats: jsonl, markdown
logbook export  > memories.jsonl
logbook reindex                              re-embed after changing the embedding model
```

Local commands open the SQLite file directly with the local identity. With `--server` they call the REST API with `--token` or `LOGBOOK_TOKEN`.

## 5. Architecture

```
 MCP stdio   MCP HTTP   REST   CLI
     \          |        |     /
      +------ identity ------+          token -> principal {subject, teams, projects, channel, client}
                |
            memory.Service               remember, recall, forget, list, verify, link
          /     |      |      \
     Store  Embedder Extractor  StalenessRule
       |        |        |
    sqlite   openai-compatible HTTP (transponder by default)
```

Packages. The plugin interfaces live in public packages so out-of-tree code can implement them.

| Package | Purpose |
|---|---|
| `cmd/logbook` | flags, subcommands, wiring |
| `memory` | types, `Service`, scope rules, trust rules, ranking. No I/O of its own |
| `store` | `Store` interface and registry |
| `store/sqlite` | default backend on `modernc.org/sqlite` (pure Go, so no cgo) with FTS5 and an in-process vector index |
| `embed` | `Embedder` interface and registry; `embed/openai` for any OpenAI-compatible `/v1/embeddings` |
| `extract` | `Extractor` interface and registry; `extract/openai` using `/v1/chat/completions` with a JSON schema response |
| `staleness` | `Rule` interface; built-in age and expiry rules |
| `importer` | `Importer` interface and registry; `importer/jsonl`, `importer/markdown` |
| `internal/identity` | OIDC JWT checks (`github.com/coreos/go-oidc/v3`), static API keys, local identity |
| `internal/mcpserver` | tool definitions on top of `memory.Service` |
| `internal/rest` | HTTP handlers on top of `memory.Service` |
| `internal/config` | YAML loading and `LOGBOOK_*` environment overrides |
| `internal/obs` | JSON logging (`log/slog`), Prometheus metrics, OTLP traces, request ID and trace header handling |

Plugins register themselves by name in an `init` function, the same way `database/sql` drivers do, and are picked by name in config. A contributor adds a package and one blank import in `cmd/logbook`. Go's runtime `plugin` package is not used because it does not work on Windows.

### Plugin interfaces (sketch)

```go
type Store interface {
    Insert(ctx context.Context, m *memory.Memory) error
    Update(ctx context.Context, m *memory.Memory) error
    Get(ctx context.Context, id string) (*memory.Memory, error)
    List(ctx context.Context, f memory.Filter) ([]*memory.Memory, string, error)
    SearchText(ctx context.Context, q string, f memory.Filter, limit int) ([]memory.Hit, error)
    SearchVector(ctx context.Context, v []float32, f memory.Filter, limit int) ([]memory.Hit, error)
    AddEdge(ctx context.Context, e memory.Edge) error
    Edges(ctx context.Context, ids []string) ([]memory.Edge, error)
    AddVerification(ctx context.Context, v memory.Verification) error
    AppendEvent(ctx context.Context, e memory.Event) error
    Close() error
}

type Embedder interface {
    Embed(ctx context.Context, texts []string) ([][]float32, error)
    Model() string
}

type Extractor interface {
    Extract(ctx context.Context, text string) ([]memory.Candidate, error)
    Contradicts(ctx context.Context, a, b string) (bool, error)
}

type Importer interface {
    Import(ctx context.Context, r io.Reader, emit func(memory.Draft) error) error
}

type Rule interface {
    Name() string
    Check(ctx context.Context, m *memory.Memory, now time.Time) (stale bool, reason string, err error)
}
```

These will be adjusted during planning, but the set of plugin points is fixed by this spec.

### Storage

SQLite in WAL mode, one file. Tables: `memories`, `memories_fts` (FTS5, external content over `content`, `subject`, `tags`), `embeddings` (memory id, model, dimensions, float32 blob), `edges`, `verifications`, `events` (append-only history of every change, which also backs `as_of` and the per-memory history view).

Vector search in v1 is a brute-force cosine scan over an in-memory copy of the embeddings for the scopes being searched, loaded lazily and updated on write. That keeps the binary pure Go and is fast enough for the v1 target of about 50,000 active memories per instance (around 300 MB of vectors at 1,536 dimensions, less with the `dimensions` option). Larger deployments are what the Postgres and pgvector backend, or a Qdrant index, are for.

### Recall ranking

1. Run FTS5 (top 50) and, if an embedder is set, vector search (top 50) over the readable scopes, with filters applied in the query.
2. Fuse with reciprocal rank fusion (k = 60).
3. Multiply by a trust weight: verified 1.0, unverified 0.7, disputed 0.3. Stale results are multiplied by 0.5. Weights are in config.
4. Swap superseded hits for their current replacement and fill `corrected_from`. Drop forgotten ones. Remove duplicates.
5. Apply `min_status`, cut to `limit`, add links when `expand` is 1.

Each scope type can set a default `min_status` in config, so a team can say that `team:` recall returns only verified memories unless the caller asks otherwise.

### transponder integration

The embedder and extractor speak the OpenAI-compatible API, so transponder is just a base URL in config, and the same code also works with OpenAI, Ollama or vLLM directly. Every call forwards the caller's `Authorization` header unchanged, along with `traceparent`, `tracestate` and `X-Request-ID`, as the contract requires. In stdio mode, or when a static key has no token to forward, logbook uses the `api_key` from its own config.

## 6. Data flow

remember:
1. Identity resolves the principal. The service picks the scope and checks write access.
2. Validate: size, kind, `corrects` and `related_to` point at memories the caller can read.
3. If `extract` is set, call the extractor and turn each candidate into a draft.
4. For each draft: dedupe by content hash; embed with a three-second budget (on failure the memory is stored with no vector and a background worker retries); insert memory, FTS row, embedding and a `created` event in one transaction.
5. Handle `corrects` (supersede), subject conflicts, the optional extractor contradiction check, and `related` neighbours.
6. Return ids, statuses, conflicts and anything that degraded.

recall:
1. Identity, readable scopes, filters.
2. Embed the query with a two-second budget; on failure search text only and report `degraded: ["embeddings"]`.
3. Rank as above, compute staleness, attach links.

## 7. Errors and degraded mode

- No embedder configured: text search only, nothing reported as degraded.
- Embedder configured but failing: writes succeed without vectors and are backfilled later; reads fall back to text search and say so in `degraded`.
- Extractor failing: `remember` with `extract: true` returns `unavailable`. Plain `remember` is unaffected.
- Database unavailable: every operation returns `unavailable`, `/readyz` returns 503.
- `/readyz` returns 200 while the database works and lists transponder's state in the body, because logbook keeps serving without it.
- Requests over the size limits return `invalid_argument` with the limit in the message.
- Recall output always returns memory content as data inside a JSON field and never as tool instructions. Team scopes that require verified memories are the main defence against a planted memory reaching other agents.

## 8. Configuration

```yaml
listen_addr: ":8420"
data_path: "./logbook.db"
default_project: ""

auth:
  oidc:
    issuer: "https://idp.example.com"
    audience: "logbook"
    teams_claim: "groups"
    projects_claim: ""
  api_keys:
    - name: "ci-agent"
      key_sha256: "..."
      teams: ["platform"]
      projects: ["mothership"]
  project_mode: "open"

embedder:
  type: "openai"
  base_url: "http://transponder:8080/v1"
  model: "text-embedding-3-small"
  dimensions: 0
  api_key: ""

extractor:
  type: "openai"
  base_url: "http://transponder:8080/v1"
  model: "gpt-4o-mini"

trust:
  weights: { verified: 1.0, unverified: 0.7, disputed: 0.3, stale: 0.5 }
  min_status: { user: "unverified", project: "unverified", team: "unverified" }
  verifiers: { team: [] }

staleness:
  review_after: { observation: "720h", fact: "4320h", procedure: "4320h", decision: "8760h", preference: "0" }

forget_retention: "720h"

telemetry:
  otlp_endpoint: ""
```

Every key can be overridden from the environment, for example `LOGBOOK_LISTEN_ADDR` and `LOGBOOK_EMBEDDER_BASE_URL`. With no `auth` section, `serve` refuses to start unless `--insecure-local` is given, which accepts any request as the local identity on loopback only.

## 9. Testing

- Unit tests for `memory`: scope resolution, trust transitions including the counted-confirm rule, conflict and supersede handling, ranking and staleness, all against an in-memory fake store and a deterministic fake embedder (hashed bag of words), so similarity is predictable.
- `store/sqlite` tests on a temp file, plus a shared conformance suite in `store/storetest` that any backend plugin can run against itself. The same idea for embedders and importers where it makes sense.
- Identity tests with a local JWKS served from `httptest` and signed test tokens.
- transponder integration tested against an `httptest` server that checks forwarded headers.
- MCP tests through the SDK's in-memory transport; REST tests through `httptest`.
- One end-to-end test that builds the binary, runs `logbook mcp` over stdio, and plays the demo story: remember a fact, remember a conflicting one, correct it, recall with the old wording and get the new answer, confirm from the CLI channel, move the clock past the review date, see it in the review queue.
- No `-race` on the development machine (no cgo). CI on Linux runs with `-race`.

## 10. Release and demo

- goreleaser builds for Windows, macOS and Linux on amd64 and arm64, plus a distroless container image.
- The README opens with a 60-second demo: add `logbook mcp` to an MCP client config, tell one agent a fact, ask a second agent, correct it, and show the review queue in the CLI.
- Good first issues seeded at launch, one per plugin point: Postgres and pgvector store, Qdrant vector index, mem0 importer, engram importer, Obsidian importer, git-refs staleness rule.

## 11. About the name

`logbook` is also the name of zalando/logbook, a widely used Java library for HTTP request and response logging, and of Logbook, an older Python logging library. Both are logging tools, which is close enough to cause some confusion in search results, and someone skimming may assume logbook is a log shipper. It is unlikely to cause trouble with package names in Go, since the module path is `github.com/raven-clown/logbook`, but a Homebrew or Scoop formula named `logbook` may need a different name, and that should be checked before the first release. The mitigation is to always say "logbook, shared memory for agents" in the repository description and README title, and to tag the repository `mcp`, `agent-memory` and `memory`. If the overlap turns out to matter, `flightlog` and `blackbox` fit the theme, though `blackbox` is crowded too. The recommendation is to keep `logbook`.

## 12. Decisions made in this spec

1. Single binary, SQLite through `modernc.org/sqlite`, brute-force vectors in memory for v1.
2. Official Go MCP SDK for both transports.
3. Hybrid search with reciprocal rank fusion; text search alone when no embedder is set.
4. Five trust states, stale computed rather than stored, and the counted-confirm rule (same subject over the same channel does not count).
5. Corrections keep history and redirect recall; subject collisions mark the older memory disputed.
6. Typed edges between memories, one hop of expansion. No model-built entity graph.
7. Soft forget with a 30-day purge, plus immediate purge on request.
8. Plugins are compiled in and registered by name. No runtime plugin loading.
9. One OpenAI-compatible client covers transponder, OpenAI, Ollama and vLLM.
10. `/readyz` fails only on the database, since logbook keeps working without transponder.
11. Project scope is open to any authenticated caller by default, with a `claim` mode for stricter setups. Team scope always needs membership.
12. No web UI in v1. The CLI `review` command covers human review.

## 13. Open choices and contract questions

1. The contract lists the MCP tools as `remember`, `logbook`, `forget`, `list_memories`. The second one looks like a typo for `recall`, which is what this spec uses. The contract should be fixed.
2. This spec adds a fifth tool, `verify`. Verification is central to the design, and folding it into `remember` would make that tool confusing. It is an addition, so nothing else in the stack breaks, but the contract should list it.
3. Should "verified" mean a different subject or channel confirmed it (this spec's default), or should only people be able to verify, for example through the CLI and REST but never over MCP? The second is stricter and slower to fill up.
4. Should a small read-and-review web page ship in v1? It would make a better screenshot for the README, at the cost of about a week of extra work.
5. The current project travels as a tool argument or an `X-Logbook-Project` header. If airlock should inject it, the header needs to go into the contract.
6. The extraction model in the example config is a placeholder. The real default should be whatever cheap model transponder exposes under a stable alias, which depends on transponder's design.
