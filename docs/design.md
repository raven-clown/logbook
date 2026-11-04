# logbook design

Status: draft for review, 2026-11-04. Revised the same day for the Storage, Admin API and "Everything is free" sections of the contract.

logbook is a shared memory for agents. Any agent can write what it learned and read what others learned, over MCP or a small REST API. It runs as one Go binary with embedded SQLite by default and Postgres for teams. It works with no model at all, and gets better search and fact extraction when it is pointed at transponder or any OpenAI-compatible endpoint.

It is one of the four projects in `mothership/CONTRACT.md` and follows that contract. Where this spec needs something the contract does not say yet, it is listed under "Open choices and contract questions" at the end.

## 1. Goals

- A memory that more than one agent and more than one person can share, with access decided by the caller's token, not by a parameter the caller fills in.
- Memories carry trust. Each one says whether someone checked it, whether it was corrected, whether it may be out of date, and what it is linked to. Recall uses all of that when it ranks and filters.
- One binary and one data file for a demo in under five minutes, on Windows, macOS and Linux. The same binary on Postgres for a team, with several replicas.
- A complete admin API, so the planned mothership console can set logbook up and run it without anyone editing YAML.
- Clear plugin points so outside contributors can add vector indexes, embedding providers, extractors, importers, staleness rules and more SQL backends without touching the core.
- Nothing held back. Roles, audit, multi-tenancy, Postgres replicas and the admin API are all in the Apache-2.0 build, and the README says so.

Non-goals for v1: any UI of its own (the console is a separate project), an entity graph built by a model (the graphiti and cognee approach), and per-memory access lists below the scope level.

## 2. What logbook does that mem0 and engram don't

Both projects are good and worth learning from. This comparison is from their READMEs as of November 2026.

| | mem0 (open source) | engram | logbook |
|---|---|---|---|
| Runtime | Python library or Docker Compose stack | Go binary, SQLite | Go binary, SQLite or Postgres |
| Needs a model to store anything | Yes, extraction runs on every add | No | No |
| Search | Vector and BM25 hybrid | SQLite FTS5 | Full-text plus vectors when an embedder is set, fused |
| MCP transports | Through its server stack | stdio, plus a local HTTP API | stdio and Streamable HTTP |
| Who owns a memory | `user_id` passed by the caller | Project, detected from the working directory | Scope derived from a verified token (OIDC JWT or API key) and enforced |
| Trust | ADD-only, every fact has equal weight | Agent-driven review and judge tools | Explicit trust state, who confirmed it and how, recall can require it |
| Corrections | Old facts stay and accumulate | `topic_key` updates in place | Supersede chain kept; recall returns the current fact and says what it replaced |
| Staleness | Time-aware retrieval | Review of stale knowledge | Review date per kind, stale flag in results, review queue, pluggable staleness rules |
| Links | Entity linking for retrieval | Obsidian export | Typed edges between memories, returned by recall |
| Admin and hosted extras | Managed platform has optimizations the open-source build lacks | Sharing and replication through Engram Cloud | Admin API, roles, audit and Postgres replicas in the same free build |

In short:

1. Team memory with real access control. engram is built for one developer on one machine. mem0 trusts whatever `user_id` it is given. logbook reads identity from the bearer token, so a team can run one instance behind airlock and each person sees their own memories, their project's and their teams'.
2. Trust is part of the data model. A memory is `unverified` until someone other than its writer confirms it. A team can set its scope to return only verified memories, which also limits the damage when an agent stores something wrong or something planted by a prompt injection.
3. Corrections are kept and used. When a fact is corrected, the old one is marked `superseded` and linked to the new one. If a query matches the old wording, recall still returns the new fact with a note about what changed. Nothing is silently overwritten and nothing silently piles up.
4. It notices when facts get old. Each kind of memory has a review period. Past it, a memory is flagged `stale` in results and shows up in a review queue. Confirming it resets the clock. Contributors can add rules, such as "stale when a referenced file changed in git".
5. It is built to be run by a team. Postgres for several replicas, roles, an audit log and an admin API that a console can drive end to end, all free.
6. It fits the mothership stack. It sits behind airlock for auth and audit, and calls transponder for embeddings and extraction, forwarding the caller's token so cost lands on the right person. It also runs fine alone.

engram already has conflict surfacing and review tools, so logbook does not claim to be the first to think about stale or conflicting memories. The difference is that logbook stores trust and corrections as state that ranking and filtering rely on, and does it for many callers at once.

## 3. Concepts

### Memory

A short piece of text that is worth remembering, plus metadata.

| Field | Meaning |
|---|---|
| `id` | `mem_` plus a ULID, sortable by creation time |
| `tenant` | Tenant id, `default` unless multi-tenancy is on |
| `scope` | `user:<subject>`, `project:<id>` or `team:<id>` |
| `content` | The text, up to 8 KiB |
| `kind` | `fact`, `preference`, `decision`, `procedure` or `observation` |
| `subject` | Optional topic key such as `ci.go-version`. Two active memories with the same subject in the same scope are treated as competing answers |
| `tags` | Free-form labels |
| `refs` | Optional file paths or URLs the memory is about. Used by staleness rules |
| `status` | `unverified`, `verified`, `disputed`, `superseded` or `forgotten` |
| `source` | `direct`, `extracted` or `imported` |
| `evidence` | Optional text: a command output, a link, the sentence it was extracted from |
| `author` | Subject, channel (`mcp`, `rest`, `cli`, `admin`) and client name of the writer |
| `created_at`, `updated_at` | Timestamps |
| `valid_until` | Optional hard expiry given by the writer |
| `review_after` | When the memory becomes stale. Computed from the kind, reset on confirm |
| `last_confirmed_at`, `confirmations`, `disputes` | Verification summary |

### Scope

Every memory lives in exactly one scope. Read and write rules:

- `user:<sub>`: only that subject.
- `project:<id>`: in `open` mode (the default) any authenticated caller in the tenant. In `restricted` mode only callers whose token lists the project in the configured claim, or who hold a `member` binding for it.
- `team:<id>`: only callers whose token lists the team in the configured claim (default `groups`), or who hold a `member` binding for it.

If the caller does not name a scope, `remember` writes to the current project when one is known and to the user scope otherwise. `recall` searches every scope the caller can read, and each result says which scope it came from.

The current project comes from the `project` argument, then the `X-Logbook-Project` header, then the `default_project` setting. In stdio mode the default is the name of the enclosing git repository, or the working directory name.

### Roles and bindings

Access beyond token claims is granted with role bindings, stored in SQL and managed through the admin API so the console can give someone access in a few clicks.

A binding is `{principal, role, target}`:

- `principal` is `user:<subject>`, `group:<name>` (matched against the teams claim) or `key:<name>` (an API key).
- `role` is one of `admin` (whole admin API), `viewer` (read-only admin API), `member` (read and write a project or team scope) or `verifier` (counted confirms where a scope requires verifiers).
- `target` is `*`, `project:<id>`, `project:*`, `team:<id>` or `team:*`. `admin` and `viewer` only take `*`.

The first admins come from `auth.bootstrap_admins` in YAML or the environment, so a new install has someone who can log in to the console. With `--insecure-local`, the local identity is an admin.

### Tenants

Every row carries a tenant id. With `auth.tenant_claim` unset there is one tenant, `default`. When it is set, the claim's value picks the tenant, and no read or write crosses tenants. Admins and viewers are per tenant. Bootstrap admins are admins in every tenant.

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

- A confirm counts unless it comes from the same subject over the same channel as the author. So an agent cannot confirm its own write in the same breath, but a person can confirm through the CLI or the console what their agent wrote over MCP, and two agents with different keys can confirm each other. This is a trust signal and a guard against rubber-stamping. It is not access control, and the README will say so.
- Each scope type has a verify policy: `writers` (anyone with write access, the default) or `verifiers` (only callers with a `verifier` binding on that scope). Under `verifiers`, other confirms are recorded but not counted.
- Status follows the latest counted verdict. Counts of confirms and disputes are kept for display.
- `stale` is not a status. It is computed at read time from `review_after`, `valid_until` and any staleness rules, so a verified memory can also be stale.

### Corrections

`remember` with `corrects: <id>` stores the new memory, marks the old one `superseded`, and adds a `supersedes` edge. The old content is kept. When a recall query matches a superseded memory better than its replacement, the replacement is returned with `corrected_from` holding the old text and the date it changed. That is how a correction keeps paying off: the next agent that asks the question the old way still gets the new answer and learns that the old one was wrong.

### Conflicts

When a new memory has the same `subject` as an active memory in the same scope and different content, and `corrects` is not set, both stay, the older one becomes `disputed`, and a `contradicts` edge links them. The `remember` result lists the conflict so the agent can decide whether it meant to correct.

When an extractor is configured, memories without a subject are also checked: the top three vector neighbours above a similarity of 0.85 are sent to the extractor, which answers whether they contradict the new memory. This check has a two-second budget and is skipped on timeout.

### Links

Edges are typed and directed: `supersedes`, `contradicts`, `related`, `derived_from`. `related` edges come from the caller (`related_to` on remember, or the REST links endpoint) and, when embeddings are on, from near neighbours above a similarity of 0.80, capped at three per memory. `recall` with `expand: 1` returns one hop of neighbours for each result. The admin API can return a wider neighbourhood for the console's graph view.

### Staleness

Default review periods, editable as settings:

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
- soft delete by default: hidden from recall and list, content kept for the `forget_retention` setting (default 30 days) and then purged by a background job. `purge: true` removes the content now. Allowed for the author and for anyone with write access to a project or team scope.
- out: `{id, status}`

`list_memories`
- in: `scopes`, `project`, `team`, `status`, `kind`, `subject`, `tag`, `author`, `stale` (bool), `order` (`newest` or `review`), `limit` (default 50, max 200), `cursor`
- `order: review` puts disputed first, then stale, then unverified. That is the review queue.
- out: `{items, next_cursor}`

`verify` (addition to the contract's list, see section 13)
- in: `id` (required), `verdict` (`confirm` or `dispute`), `note`
- out: `{id, status, confirmations, disputes, counted}`

### REST under `/v1/`

Same service, same rules, JSON in and out. This is the caller's view: only scopes the caller can read.

| Method and path | Operation |
|---|---|
| `POST /v1/memories` | remember |
| `POST /v1/recall` | recall |
| `GET /v1/memories` | list_memories, filters as query parameters, `cursor` in, `next_cursor` out |
| `GET /v1/memories/{id}` | one memory with its edges, verifications and history |
| `DELETE /v1/memories/{id}` | forget, `?purge=true` |
| `POST /v1/memories/{id}/verify` | verify |
| `POST /v1/memories/{id}/links` | add a `related` edge |
| `GET /v1/export` | JSONL stream of the caller's readable memories |

### Admin API under `/admin/v1/`

JSON, same identity rules as the rest of the API, `admin` role for changes and `viewer` or `admin` for reads. Every list takes `cursor` and `limit` and returns `next_cursor`. It covers everything the console needs to show or change, so a new user can go from an empty install to a working setup without a YAML file.

| Area | Endpoints |
|---|---|
| Status | `GET status`: version, uptime, database and schema version, embedder and extractor reachability, counts by scope and status, warnings (no secret key set, Postgres without pgvector, and so on) |
| Settings | `GET settings`: every setting with its value, default, source (`default`, `stored`, `file`, `env`) and `locked`. `PATCH settings`. Secrets are never returned, only `set: true` |
| Connection checks | `POST checks/embedder`, `POST checks/extractor`, `POST checks/oidc`: test a candidate setting before saving it |
| API keys | `GET api-keys`, `POST api-keys` (the key is shown once), `DELETE api-keys/{id}` |
| Access | `GET bindings`, `POST bindings`, `DELETE bindings/{id}`, `GET principals` (subjects, groups and keys seen recently, for pickers) |
| Scopes | `GET scopes`: every project, team and user scope with counts and last activity. `PATCH scopes/{scope}`: per-scope overrides of `min_status` and verify policy |
| Memories | `GET memories` across all scopes with the same filters as `/v1/`, `GET memories/{id}`, `PATCH memories/{id}` (kind, subject, tags, refs, valid_until), `DELETE memories/{id}`, `POST memories/{id}/verify`, `POST memories/bulk` (`forget`, `purge`, `confirm`, `dispute`, `move` to another scope) |
| Review | `GET review`: the review queue across scopes |
| Graph | `GET memories/{id}/graph?depth=2`: nodes and edges around a memory |
| Audit | `GET audit`: every change with actor, channel, request id, target and before and after values, filterable by actor, action, target and time |
| Usage | `GET usage`: operations, embedding and extraction calls, and token and cost totals per day, per principal and per scope |
| Import and export | `POST imports` (upload with a format name, runs as a job), `GET export` |
| Jobs | `GET jobs`, `GET jobs/{id}`, `POST jobs/reindex`, `POST jobs/purge` |

Usage numbers come from transponder's `X-Usage-Input-Tokens`, `X-Usage-Output-Tokens` and `X-Usage-Cost-USD` headers on each embedding and extraction call, so the console can show what memory costs per person without asking transponder.

The OpenAPI 3.0 file `api/openapi.yaml` describes both `/v1/` and `/admin/v1/`. It is written by hand and is the source of truth. A test checks that every registered route is in the file and that handler responses validate against it.

### Errors

One shape everywhere: `{"error": {"code": "not_found", "message": "..."}}` with codes `invalid_argument`, `unauthenticated`, `permission_denied`, `not_found`, `conflict`, `unavailable`, `internal`. Changing a locked setting returns `conflict` and names the file or variable that locks it. MCP tool errors carry the same code and message with `isError: true`.

Also `/healthz`, `/readyz` and `/metrics` as the contract requires.

### CLI

```
logbook serve   --config logbook.yaml        HTTP: MCP at /mcp, REST at /v1, admin at /admin/v1, health, metrics
logbook mcp     --config logbook.yaml        MCP over stdio, local identity
logbook remember | recall | list | forget | verify   one operation, against the local database or --server URL
logbook review                               walk the review queue and confirm, dispute or skip each item
logbook import  <format> <path>              built-in formats: jsonl, markdown
logbook export  > memories.jsonl
logbook reindex                              re-embed after changing the embedding model or tokenizer
logbook migrate                              run migrations and exit, for deploy pipelines
```

Local commands open the database directly with the local identity. With `--server` they call the REST API with `--token` or `LOGBOOK_TOKEN`.

## 5. Architecture

```
 MCP stdio   MCP HTTP   REST   Admin API   CLI
     \          |        |        |        /
      +----------- identity --------------+     token -> principal {tenant, subject, teams, projects, roles, channel, client}
                       |
                 memory.Service   admin.Service
                  /    |     |     \      |
          Store  VectorIndex Embedder Extractor StalenessRule   settings, bindings, keys, audit, usage, jobs
            |         |          |        |
     sqlite/postgres  memory/pgvector   openai-compatible HTTP (transponder by default)
```

Packages. The plugin interfaces live in public packages so out-of-tree code can implement them.

| Package | Purpose |
|---|---|
| `cmd/logbook` | flags, subcommands, wiring |
| `memory` | types, `Service`, scope rules, trust rules, ranking. No I/O of its own |
| `admin` | `Service` for settings, bindings, API keys, scopes, audit, usage, jobs |
| `store` | `Store` interface, registry and the shared conformance suite `store/storetest` |
| `store/sqlite` | default backend on `modernc.org/sqlite` (pure Go) with FTS5 |
| `store/postgres` | team backend on `pgx` with `tsvector` full-text search |
| `store/migrate` | small forward-only migration runner over embedded SQL files, one set per dialect |
| `vector` | `Index` interface and registry; `vector/memory` (brute force in process) and `vector/pgvector` |
| `embed` | `Embedder` interface and registry; `embed/openai` for any OpenAI-compatible `/v1/embeddings` |
| `extract` | `Extractor` interface and registry; `extract/openai` using `/v1/chat/completions` with a JSON schema response |
| `staleness` | `Rule` interface; built-in age and expiry rules |
| `importer` | `Importer` interface and registry; `importer/jsonl`, `importer/markdown` |
| `internal/identity` | OIDC JWT checks (`github.com/coreos/go-oidc/v3`), API keys, bindings, local identity |
| `internal/settings` | layered settings: defaults, stored, file, env; hot reload |
| `internal/mcpserver` | tool definitions on top of `memory.Service` |
| `internal/rest` | `/v1/` handlers |
| `internal/adminapi` | `/admin/v1/` handlers |
| `internal/jobs` | background jobs: embedding backfill, purge, import, reindex |
| `internal/obs` | JSON logging (`log/slog`), Prometheus metrics, OTLP traces, request ID and trace header handling |

Plugins register themselves by name in an `init` function, the same way `database/sql` drivers do, and are picked by name in settings. A contributor adds a package and one blank import in `cmd/logbook`. Go's runtime `plugin` package is not used because it does not work on Windows.

### Plugin interfaces (sketch)

```go
type Store interface {
    Insert(ctx context.Context, m *memory.Memory) error
    Update(ctx context.Context, m *memory.Memory) error
    Get(ctx context.Context, tenant, id string) (*memory.Memory, error)
    List(ctx context.Context, f memory.Filter) ([]*memory.Memory, string, error)
    SearchText(ctx context.Context, q string, f memory.Filter, limit int) ([]memory.Hit, error)
    AddEdge(ctx context.Context, e memory.Edge) error
    Edges(ctx context.Context, tenant string, ids []string) ([]memory.Edge, error)
    AddVerification(ctx context.Context, v memory.Verification) error
    AppendEvent(ctx context.Context, e memory.Event) error
    Tx(ctx context.Context, fn func(Store) error) error
    Close() error
}

type Index interface {
    Upsert(ctx context.Context, tenant, scope, id string, v []float32) error
    Delete(ctx context.Context, tenant, id string) error
    Search(ctx context.Context, tenant string, scopes []string, v []float32, k int) ([]memory.Hit, error)
}

type Embedder interface {
    Embed(ctx context.Context, texts []string) ([][]float32, Usage, error)
    Model() string
}

type Extractor interface {
    Extract(ctx context.Context, text string) ([]memory.Candidate, Usage, error)
    Contradicts(ctx context.Context, a, b string) (bool, Usage, error)
}

type Importer interface {
    Import(ctx context.Context, r io.Reader, emit func(memory.Draft) error) error
}

type Rule interface {
    Name() string
    Check(ctx context.Context, m *memory.Memory, now time.Time) (stale bool, reason string, err error)
}
```

The admin data (settings, bindings, keys, audit, usage, jobs) goes through a second interface on the same backend. These signatures will be adjusted during planning, but the set of plugin points is fixed by this spec.

### Storage

All state that must survive a restart lives in SQL, as the contract requires.

- SQLite (default): one file, WAL mode, `modernc.org/sqlite`. One process at a time. Full-text search through FTS5.
- Postgres (teams and replicas): `pgx`. Full-text search through a `tsvector` column with a GIN index. Several replicas can share one database.

Tables on both: `memories`, a full-text index over `content`, `subject` and `tags`, `embeddings` (memory id, model, dimensions, vector), `edges`, `verifications`, `events` (append-only history of every change, which backs audit, `as_of` and the per-memory history view), `settings`, `bindings`, `api_keys`, `usage_daily`, `jobs`, `schema_migrations`.

Migrations are plain SQL files embedded in the binary, one numbered set per dialect, forward only. They run at startup before the server listens. On Postgres the runner holds an advisory lock so replicas starting together do not race. `logbook migrate` runs them on their own for deploy pipelines.

Full-text tokenizing: the default splits on word boundaries, which does not work for languages written without spaces, such as Thai, Chinese or Japanese. The `search.tokenizer` setting switches to trigram matching (FTS5 `trigram` on SQLite, `pg_trgm` on Postgres). Changing it rebuilds the index through a reindex job. Vector search covers those languages well either way.

Vector search:
- `vector/memory`, the default on SQLite: a brute-force cosine scan over an in-process copy of the embeddings for the scopes being searched, loaded lazily and updated on write. It keeps the build pure Go and is fast enough for about 50,000 active memories (around 300 MB at 1,536 dimensions, less with the `dimensions` setting). It only works with a single process.
- `vector/pgvector`, the default on Postgres when the `vector` extension is installed, with an HNSW index built for the configured dimensions. Safe with replicas.
- Postgres without pgvector: text search only, with a warning in the admin status.
- A Qdrant index is a natural first contribution.

Background jobs (embedding backfill, purge, import, reindex) are rows in `jobs`. On Postgres, replicas claim them with `FOR UPDATE SKIP LOCKED`.

### Settings

Settings are layered, lowest first: built-in defaults, values stored in the `settings` table, the YAML file, environment variables. A key set in the file or the environment is locked, and the admin API shows where the lock comes from. Everything else can be changed through the admin API and applies without a restart. Each replica checks a settings version number every five seconds and reloads on change.

Only three things are bootstrap-only and must come from the file or environment: `listen_addr`, `database`, and `auth.bootstrap_admins`.

Secret settings, such as an embedder API key or an OIDC client secret, are encrypted with AES-GCM when `LOGBOOK_SECRET_KEY` is set. Without it they are stored as plain text and the admin status shows a warning.

### Recall ranking

1. Run full-text search (top 50) and, if an embedder is set, vector search (top 50) over the readable scopes, with filters applied in the query.
2. Fuse with reciprocal rank fusion (k = 60).
3. Multiply by a trust weight: verified 1.0, unverified 0.7, disputed 0.3. Stale results are multiplied by 0.5. Weights are settings.
4. Swap superseded hits for their current replacement and fill `corrected_from`. Drop forgotten ones. Remove duplicates.
5. Apply `min_status`, cut to `limit`, add links when `expand` is 1.

Each scope type, and each individual scope through the admin API, can set a default `min_status`, so a team can say that its scope returns only verified memories unless the caller asks otherwise.

### transponder integration

The embedder and extractor speak the OpenAI-compatible API, so transponder is just a base URL in settings, and the same code also works with OpenAI, Ollama or vLLM directly. Every call forwards the caller's `Authorization` header unchanged, along with `traceparent`, `tracestate` and `X-Request-ID`, as the contract requires. Background jobs and stdio mode have no caller token to forward, so they use the API key from settings. Usage headers on each response are added to `usage_daily`.

## 6. Data flow

remember:
1. Identity resolves the principal. The service picks the scope and checks write access.
2. Validate: size, kind, `corrects` and `related_to` point at memories the caller can read.
3. If `extract` is set, call the extractor and turn each candidate into a draft.
4. For each draft: dedupe by content hash; embed with a three-second budget (on failure the memory is stored with no vector and a backfill job retries); insert the memory, its full-text row, its embedding and a `created` event in one transaction.
5. Handle `corrects` (supersede), subject conflicts, the optional extractor contradiction check, and `related` neighbours.
6. Return ids, statuses, conflicts and anything that degraded.

recall:
1. Identity, readable scopes, filters.
2. Embed the query with a two-second budget; on failure search text only and report `degraded: ["embeddings"]`.
3. Rank as above, compute staleness, attach links.

Admin changes go through `admin.Service`, which writes the change and an audit event in one transaction.

## 7. Errors and degraded mode

- No embedder configured: text search only, nothing reported as degraded.
- Embedder configured but failing: writes succeed without vectors and are backfilled later; reads fall back to text search and say so in `degraded`.
- Extractor failing: `remember` with `extract: true` returns `unavailable`. Plain `remember` is unaffected.
- Database unavailable: every operation returns `unavailable`, `/readyz` returns 503.
- Migrations fail at startup: the process logs the failing file and exits non-zero before listening.
- `/readyz` returns 200 while the database works and lists transponder's state in the body, because logbook keeps serving without it.
- Requests over the size limits return `invalid_argument` with the limit in the message.
- Recall output always returns memory content as data inside a JSON field and never as tool instructions. Scopes that require verified memories are the main defence against a planted memory reaching other agents.

## 8. Configuration

The YAML file is optional apart from the bootstrap keys. Everything else can live in the database and be set through the admin API.

```yaml
listen_addr: ":8420"
database:
  driver: "sqlite"
  dsn: "./logbook.db"

auth:
  bootstrap_admins: ["user:alice@example.com"]
  oidc:
    issuer: "https://idp.example.com"
    audience: "logbook"
    teams_claim: "groups"
    projects_claim: ""
  tenant_claim: ""
  project_mode: "open"

embedder:
  type: "openai"
  base_url: "http://transponder:8080/v1"
  model: "text-embedding-3-small"
  dimensions: 0

extractor:
  type: "openai"
  base_url: "http://transponder:8080/v1"
  model: "memory-extract"

search:
  tokenizer: "unicode"

telemetry:
  otlp_endpoint: ""
```

Trust weights, default `min_status`, verify policies, review periods, `forget_retention`, `default_project` and API keys are settings too, with the defaults given earlier in this spec. Every key can be overridden from the environment, for example `LOGBOOK_LISTEN_ADDR` and `LOGBOOK_EMBEDDER_BASE_URL`. With no OIDC issuer and no API keys, `serve` refuses to start unless `--insecure-local` is given, which accepts any request on loopback as the local admin, so a first-time user can open the console and finish setup there.

## 9. Testing

- Unit tests for `memory`: scope resolution, trust transitions including the counted-confirm rule and verify policies, conflict and supersede handling, ranking and staleness, all against an in-memory fake store and a deterministic fake embedder (hashed bag of words), so similarity is predictable.
- `store/storetest`: one conformance suite that every backend runs. SQLite runs it on a temp file in every `go test`. Postgres runs it when `LOGBOOK_TEST_POSTGRES_DSN` is set, and CI provides a Postgres service with pgvector. Migrations are tested from an empty database and from each earlier version.
- `admin` and `internal/settings` tests: layering, locks, hot reload, secret encryption, bindings, audit records for every change.
- Identity tests with a local JWKS served from `httptest` and signed test tokens, including tenant separation.
- transponder integration tested against an `httptest` server that checks forwarded headers and returns usage headers.
- MCP tests through the SDK's in-memory transport. REST and admin tests through `httptest`, with every response validated against `api/openapi.yaml`.
- One end-to-end test that builds the binary, runs `logbook mcp` over stdio, and plays the demo story: remember a fact, remember a conflicting one, correct it, recall with the old wording and get the new answer, confirm from the CLI channel, move the clock past the review date, see it in the review queue.
- No `-race` on the development machine (no cgo). CI on Linux runs with `-race`.

## 10. Milestones, release and demo

The work is split so there is something to demo early:

1. Core: memory model, SQLite store with migrations, MCP over stdio, CLI. Demo-able alone.
2. Server: Streamable HTTP, REST, identity, bindings, metrics and traces.
3. Admin API: settings, keys, audit, usage, jobs, OpenAPI file.
4. Postgres with pgvector, replicas.
5. transponder: embeddings, extraction, contradiction checks, usage tracking.

Release:
- goreleaser builds for Windows, macOS and Linux on amd64 and arm64, plus a distroless container image.
- The README opens with a 60-second demo: add `logbook mcp` to an MCP client config, tell one agent a fact, ask a second agent, correct it, and show the review queue in the CLI. It also states that every feature is in the free build.
- Good first issues seeded at launch, one per plugin point: Qdrant vector index, mem0 importer, engram importer, Obsidian importer, git-refs staleness rule, a MySQL or libSQL store.

## 11. About the name

`logbook` is also the name of zalando/logbook, a widely used Java library for HTTP request and response logging, and of Logbook, an older Python logging library. Both are logging tools, which is close enough to cause some confusion in search results, and someone skimming may assume logbook is a log shipper. It is unlikely to cause trouble with package names in Go, since the module path is `github.com/raven-clown/logbook`, but a Homebrew or Scoop formula named `logbook` may need a different name, and that should be checked before the first release. The mitigation is to always say "logbook, shared memory for agents" in the repository description and README title, and to tag the repository `mcp`, `agent-memory` and `memory`. If the overlap turns out to matter, `flightlog` and `blackbox` fit the theme, though `blackbox` is crowded too. The recommendation is to keep `logbook`.

## 12. Decisions made in this spec

1. Single binary. SQLite through `modernc.org/sqlite` by default, Postgres through `pgx` for teams, both pure Go, with forward-only embedded migrations that run at startup.
2. Vectors: brute force in process on SQLite, pgvector on Postgres, text search only on Postgres without pgvector.
3. Official Go MCP SDK for both transports.
4. Hybrid search with reciprocal rank fusion; text search alone when no embedder is set. A trigram tokenizer setting for languages without spaces.
5. Five trust states, stale computed rather than stored, the counted-confirm rule (same subject over the same channel does not count), and a per-scope verify policy.
6. Corrections keep history and redirect recall; subject collisions mark the older memory disputed.
7. Typed edges between memories, one hop in recall, a wider neighbourhood in the admin API. No model-built entity graph.
8. Soft forget with a 30-day purge, plus immediate purge on request.
9. Plugins are compiled in and registered by name. No runtime plugin loading.
10. One OpenAI-compatible client covers transponder, OpenAI, Ollama and vLLM.
11. `/readyz` fails only on the database, since logbook keeps working without transponder.
12. Project scope is open to any authenticated caller in the tenant by default, with a `restricted` mode. Team scope always needs membership, from a claim or a binding.
13. Roles and access are role bindings stored in SQL. Bootstrap admins come from the file or environment.
14. Settings are layered and editable through the admin API, with file and environment values locked. Only the listen address, database and bootstrap admins need a file.
15. Multi-tenancy through a tenant claim, off by default.
16. No UI in this repository. Human review in v1 is the CLI `review` command and, later, the mothership console.
17. A hand-written OpenAPI 3.0 file is the source of truth for `/v1/` and `/admin/v1/`, checked by tests.

## 13. Open choices and contract questions

1. The contract lists the MCP tools as `remember`, `logbook`, `forget`, `list_memories`. The second one looks like a typo for `recall`, which is what this spec uses. The contract should be fixed.
2. This spec adds a fifth tool, `verify`. Verification is central to the design, and folding it into `remember` would make that tool confusing. It is an addition, so nothing else in the stack breaks, but the contract should list it.
3. Should "verified" mean a different subject or channel confirmed it (this spec's default), or should only people be able to verify, for example through the CLI, REST or the console but never over MCP? The per-scope `verifiers` policy already allows the stricter setup, so this is only about the default.
4. The current project travels as a tool argument or an `X-Logbook-Project` header. If airlock should inject it, the header needs to go into the contract.
5. The extraction model name in the example config, `memory-extract`, assumes transponder can expose a stable alias for a cheap model. That depends on transponder's design.
6. Role names (`admin`, `viewer`, `member`, `verifier`) and the binding shape would be easier for the console if all four projects used the same ones. That could become a shared section in the contract.
7. Usage is tracked per principal from transponder's headers. If the console should show cost in one place, it may be simpler for the console to read it from transponder alone, and logbook would then only track operation counts.
