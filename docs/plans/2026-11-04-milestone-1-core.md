# Milestone 1: Core implementation plan

**Goal:** `logbook mcp` works in any MCP client with no server and no model: five tools, trust states, corrections, conflicts, staleness and full-text recall on SQLite, plus CLI one-shot commands, JSONL import and export.

**Architecture:** Package `memory` holds the types, the `Store` interface it consumes, the pure trust and ranking rules, and `Service`, which implements the five operations on top of any `Store`. `store/sqlite` implements `Store` on `modernc.org/sqlite` with FTS5, migrated by `store/migrate`, and is checked by the shared suite in `store/storetest`. `internal/mcpserver` and `cmd/logbook` are thin layers over `Service`.

**Tech stack:** Go 1.26 module (built with 1.27 locally), `modernc.org/sqlite` v1.61.1, `github.com/mark3labs/mcp-go` v1.5.0, `github.com/oklog/ulid/v2` v2.1.2, `gopkg.in/yaml.v3` v3.0.1, standard library `log/slog`, `flag`, `database/sql`, `testing`.

**Spec:** [docs/design.md](../design.md). Issues: #1 to #6, #8, #9, #11, #15 on the [Core milestone](https://github.com/raven-clown/logbook/milestone/1). #7, #10, #12, #13, #14 and #16 are left for contributors and must stay possible after this plan: the interfaces they name are produced here.

## Global constraints

- Module path `github.com/raven-clown/logbook`, `go 1.26` in go.mod.
- Pure Go only, no cgo. Tests must pass with `go test ./...` on Windows and with `-race` on Linux and macOS.
- Coverage of 80% or more in total (`.testcoverage.yml`). Keep `main()` to one line that calls `run`.
- `golangci-lint run ./...` clean with the repository's `.golangci.yml` (gosec, revive, errorlint and others). Every exported name has a doc comment.
- No network in unit tests, no bare `time.Sleep`; time comes from an injected `func() time.Time`.
- Every table and every query carries `org_id`, which is `"default"` in this milestone.
- Content limit 8 KiB (8192 bytes of UTF-8). `recall` limit default 8, max 50. `list_memories` limit default 50, max 500.
- Review periods: observation 720h, fact 4320h, procedure 4320h, decision 8760h, preference 0 (never).
- Trust weights: verified 1.0, unverified 0.7, disputed 0.3, stale multiplier 0.5. RRF k = 60. Text search candidate count 50.
- IDs are `mem_` plus a ULID.
- Every type that is returned to a caller (`Memory`, `Edge`, `Verification`, `Remembered`, `RememberResult`, `Recalled`, `Correction`, `RecallResult`, `VerifyResult`, `ListResult`, `Record`) has snake_case JSON tags matching the field names in design section 4, with `omitempty` or `omitzero` on optional fields. `Recalled` embeds `Memory`, so its fields sit at the top level of each result.
- In stdio mode nothing but protocol goes to stdout. Logs are JSON through `slog` on stderr.
- No comments that restate code, no em-dashes, no mention of AI or assistants anywhere, no attribution trailers in commits.
- Commit after every task with a Conventional Commits message, update AGENTS.md in the same commit when commands, layout or rules change, add a CHANGELOG line under Unreleased for anything users will notice, and push to `main`.

## Review focus

1. Recall queries with FTS5 syntax in them (`c++`, `"quoted`, `ci.go-version`, `AND`, `-x`, `*`, a lone `(`) must return results or nothing, never an SQL error. Pinned by `SearchTextSyntax` in the Task 3 suite, run in Task 5.
2. Content that is empty, whitespace only, over 8 KiB in Thai (multi-byte), or not valid UTF-8 must be rejected with `invalid_argument` and `field: "content"`. Pinned by `TestRememberValidation` in Task 6.
3. Two MCP calls writing at the same moment must both succeed on SQLite instead of failing with `SQLITE_BUSY`. Pinned by `ConcurrentInserts` in the Task 3 suite, run in Task 5.
4. Correcting a memory that is already superseded must fail with `conflict` (Task 6, `TestRememberCorrects`), and a chain A corrected by B corrected by C must return C for a query that only matches A (Task 7, `TestRecallCorrectionChain`).
5. A database path on Windows with a drive letter and a space in a folder name must open and migrate. Pinned by `TestOpenWindowsStylePath` in Task 5.

---

### Task 1: memory types, scopes, principals and errors

Closes part of #1.

**Files:**
- Create: `memory/doc.go`, `memory/types.go`, `memory/scope.go`, `memory/errors.go`
- Test: `memory/scope_test.go`, `memory/errors_test.go`

**Interfaces:**
- Produces:
  - `type Kind string` with `KindFact`, `KindPreference`, `KindDecision`, `KindProcedure`, `KindObservation`; `func ParseKind(s string) (Kind, error)` where `""` gives `KindFact`.
  - `type Status string` with `StatusUnverified`, `StatusVerified`, `StatusDisputed`, `StatusSuperseded`, `StatusForgotten`; `func (s Status) Active() bool` (true for the first three).
  - `type Source string` with `SourceDirect`, `SourceExtracted`, `SourceImported`.
  - `type Channel string` with `ChannelMCP`, `ChannelREST`, `ChannelCLI`, `ChannelAdmin`.
  - `type EdgeType string` with `EdgeSupersedes`, `EdgeContradicts`, `EdgeRelated`, `EdgeDerivedFrom`.
  - `type Verdict string` with `VerdictConfirm`, `VerdictDispute`.
  - `type Author struct { Subject string; Channel Channel; Client string }`
  - `type Memory struct` with JSON tags in snake_case: `ID, OrgID, Scope, Content, Kind, Subject, Tags, Refs, Status, Source, Evidence, Author, CreatedAt, UpdatedAt, ValidUntil, ReviewAfter, LastConfirmedAt, Confirmations, Disputes, SupersededBy, SupersededAt, ForgottenAt, Purged, ContentHash, Version`. Times are `time.Time` with `omitzero`; a zero `ReviewAfter` means never.
  - `type Edge struct { OrgID, From, To string; Type EdgeType; CreatedBy string; CreatedAt time.Time }`
  - `type Verification struct { OrgID, MemoryID string; Verifier Author; Verdict Verdict; Note string; Counted bool; At time.Time }`
  - `type Event struct { OrgID, MemoryID, Action string; Actor Author; At time.Time; Detail json.RawMessage }`
  - `type Hit struct { ID string; Score float64 }`
  - `const DefaultOrg = "default"`
  - `type ScopeType string` with `ScopeUser`, `ScopeProject`, `ScopeTeam`; `type Scope struct { Type ScopeType; ID string }`; `func ParseScope(s string) (Scope, error)`; `func (s Scope) String() string`.
  - `type Principal struct { OrgID, Subject string; Teams []string; Project string; Channel Channel; Client string }`; `func (p Principal) CanRead(s Scope) bool`; `func (p Principal) CanWrite(s Scope) bool`; `func (p Principal) Author() Author`.
  - `type Code string` with `CodeInvalidArgument`, `CodeUnauthenticated`, `CodePermissionDenied`, `CodeNotFound`, `CodeConflict`, `CodeVersionConflict`, `CodeUnavailable`, `CodeInternal`; `type Error struct { Code Code; Message, Field string }` implementing `error`; `func Errorf(code Code, field, format string, args ...any) *Error`; `func CodeOf(err error) Code` (uses `errors.As`, returns `CodeInternal` for other errors and `""` for nil); sentinel `ErrNotFound = &Error{Code: CodeNotFound, Message: "memory not found"}` and `ErrVersionConflict`. `(*Error).Is` matches on code, so `errors.Is(Errorf(CodeNotFound, "", "x"), ErrNotFound)` is true.

- [ ] **Step 1: Write the failing tests**

`TestParseScope` (table): `"user:alice"` gives `{ScopeUser, "alice"}`; `"project:mothership"`; `"team:platform"`; errors with `CodeInvalidArgument` for `""`, `"user"`, `"user:"`, `"org:x"`, `"team: spaced"` (IDs must match `^[A-Za-z0-9._@+-]{1,200}$`). Round trip: `ParseScope(s.String())` equals `s`.

`TestPrincipalAccess` (table, principal `{Subject:"alice", Teams:["platform"]}`): read and write true for `user:alice`, false for `user:bob`, true for any `project:x` (open mode is the only mode in this milestone), true for `team:platform`, false for `team:sales`.

`TestCodeOf`: nil gives `""`, `Errorf(CodeConflict, "", "x")` gives `CodeConflict`, a wrapped one through `fmt.Errorf("ctx: %w", e)` gives the same code, `io.EOF` gives `CodeInternal`; `errors.Is(Errorf(CodeNotFound, "", "gone"), ErrNotFound)` is true.

`TestParseKind`: empty gives fact, each of the five names parses, `"note"` fails with `CodeInvalidArgument` and `Field == "kind"`.

- [ ] **Step 2: Run the tests and see them fail**

Run: `go test ./memory/`
Expected: build failure, undefined names.

- [ ] **Step 3: Implement the types in the four files above**

- [ ] **Step 4: Run the tests and see them pass**

Run: `go test ./memory/ && golangci-lint run ./memory/`
Expected: `ok`, `0 issues.`

- [ ] **Step 5: Commit and push**

`feat(memory): add memory types, scopes and error codes`

---

### Task 2: trust verdicts and staleness rules

Closes #1, closes #6.

**Files:**
- Create: `memory/trust.go`, `memory/rule.go`, `staleness/staleness.go`
- Test: `memory/trust_test.go`, `staleness/staleness_test.go`

**Interfaces:**
- Consumes: Task 1 types.
- Produces:
  - `func ApplyVerdict(m *Memory, v *Verification, now time.Time, period time.Duration) error`: sets `v.Counted`, updates `m`. Returns `CodeConflict` when `m.Status` is superseded or forgotten.
  - `type Rule interface { Name() string; Check(ctx context.Context, m *Memory, now time.Time) (stale bool, reason string, err error) }`
  - `func CheckStale(ctx context.Context, rules []Rule, m *Memory, now time.Time) (bool, string, error)`: first rule that says stale wins.
  - `var DefaultReviewPeriods = map[Kind]time.Duration{...}` with the Global constraints values; `func ReviewAfter(created time.Time, k Kind, periods map[Kind]time.Duration) time.Time` (zero for a zero period).
  - `staleness.ReviewDue() memory.Rule`, named `review_due`, stale with reason `review_due` when `ReviewAfter` is non-zero and `!now.Before(ReviewAfter)`.
  - `staleness.Default() []memory.Rule` returning `[ReviewDue()]`. Issue #7 adds `Expiry()` here.

Rules for `ApplyVerdict`:
- confirm: `Counted` is false when `v.Verifier.Subject == m.Author.Subject && v.Verifier.Channel == m.Author.Channel`, true otherwise. When counted: `Confirmations++`, `Status = verified`, `LastConfirmedAt = now`, `ReviewAfter = now + period` (zero when period is zero). When not counted: nothing on `m` changes.
- dispute: always counted. `Disputes++`, `Status = disputed`.

- [ ] **Step 1: Write the failing tests**

`TestApplyVerdict` (table, author `{alice, mcp}`, now `2026-11-04T00:00:00Z`, period 180 days): confirm by `{alice, cli}` gives verified, counted, confirmations 1, review_after `2027-05-03`; confirm by `{alice, mcp}` gives unverified, not counted, confirmations 0; confirm by `{bob, mcp}` counted; dispute by `{alice, mcp}` gives disputed, counted, disputes 1; confirm after a dispute by `{bob, cli}` gives verified; any verdict on superseded or forgotten gives `CodeConflict` and leaves `m` unchanged; period 0 leaves `ReviewAfter` zero.

`TestReviewAfter`: each kind against `DefaultReviewPeriods`; preference gives zero.

`TestReviewDue` (table): zero `ReviewAfter` never stale; one second before not stale; exactly at stale with reason `review_due`; after stale.

`TestCheckStale`: no rules gives false; two rules where the second is stale returns the second reason; a rule error is returned.

- [ ] **Step 2: Run and see them fail**

Run: `go test ./memory/ ./staleness/`
Expected: build failure.

- [ ] **Step 3: Implement `ApplyVerdict`, `CheckStale`, `ReviewAfter`, `ReviewDue`, `Default`**

- [ ] **Step 4: Run and see them pass**

Run: `go test ./memory/ ./staleness/ && golangci-lint run ./...`
Expected: `ok` for both, `0 issues.`

- [ ] **Step 5: Commit and push**

`feat(memory): add trust verdicts and staleness rules`

---

### Task 3: Store interface and the conformance suite

Closes #4 once Task 5 runs it.

**Files:**
- Create: `memory/store.go`, `store/storetest/storetest.go`, `store/registry.go`
- Test: `store/registry_test.go` (the suite itself is exercised in Task 5)

**Interfaces:**
- Consumes: Task 1 types.
- Produces in `memory/store.go`:

```go
type Order string // OrderNewest, OrderReview

type Filter struct {
	OrgID    string
	Scopes   []string // full scope strings; empty means none, never all
	Statuses []Status // empty means every status except forgotten
	Kinds    []Kind
	Tags     []string // any of
	Subject  string
	Author   string // author subject
	StaleAt  time.Time // non-zero: only ReviewAfter non-zero and <= StaleAt
	AsOf     time.Time // non-zero: CreatedAt <= AsOf and not superseded or forgotten at AsOf, any status
	Order    Order
	Now      time.Time // used by OrderReview
	Limit    int
	Cursor   string
}

type Store interface {
	Insert(ctx context.Context, m *Memory) error
	Update(ctx context.Context, m *Memory) error // optimistic: WHERE version = m.Version, then m.Version++; ErrVersionConflict on mismatch
	Get(ctx context.Context, orgID, id string) (*Memory, error) // ErrNotFound
	FindByHash(ctx context.Context, orgID, scope, hash string) (*Memory, error) // non-forgotten only; ErrNotFound
	FindBySubject(ctx context.Context, orgID, scope, subject string) ([]*Memory, error) // active only
	List(ctx context.Context, f Filter) ([]*Memory, string, error)
	SearchText(ctx context.Context, query string, f Filter, limit int) ([]Hit, error) // best first, Score higher is better
	AddEdge(ctx context.Context, e Edge) error // duplicate (from, to, type) is a no-op
	Edges(ctx context.Context, orgID string, ids []string) ([]Edge, error) // edges where From or To is in ids
	AddVerification(ctx context.Context, v Verification) error
	Verifications(ctx context.Context, orgID, memoryID string) ([]Verification, error)
	AppendEvent(ctx context.Context, e Event) error
	Events(ctx context.Context, orgID, memoryID string) ([]Event, error) // oldest first
	Tx(ctx context.Context, fn func(Store) error) error // rolls back when fn returns an error
	Close() error
}
```

`OrderReview` sorts disputed first, then stale at `Now`, then unverified, then the rest, newest first inside each group. Cursors are opaque strings; this milestone encodes an offset, which is fine for the single-process SQLite store.

- Produces in `store/registry.go`: `type OpenFunc func(ctx context.Context, dsn string) (memory.Store, error)`; `func Register(name string, open OpenFunc)` (panics on duplicate); `func Open(ctx context.Context, driver, dsn string) (memory.Store, error)` (`CodeInvalidArgument` with `Field: "database.driver"` for an unknown name); `func Drivers() []string` sorted.
- Produces in `store/storetest`: `func Run(t *testing.T, open func(t *testing.T) memory.Store)` with subtests, each on a fresh store from `open`.

Suite subtests, each with exact assertions:
- `InsertGet`: every field of a fully populated memory survives a round trip (compare with `reflect.DeepEqual` after truncating times to milliseconds, UTC).
- `GetMissing`: `errors.Is(err, memory.ErrNotFound)`.
- `UpdateVersion`: update bumps `Version` from 1 to 2; a second update with a stale copy (Version 1) gives `ErrVersionConflict`.
- `OrgIsolation`: a memory in org `a` is not found from org `b` by `Get`, `List`, `SearchText`, `FindByHash`.
- `FindByHash`: finds an unverified one, not a forgotten one.
- `FindBySubject`: returns active ones only.
- `ListFilters`: one case per `Filter` field, including `Statuses` empty excluding forgotten, `Tags` any-of, `StaleAt`, `AsOf` (created after, superseded before, forgotten before, all excluded).
- `ListPaging`: 7 memories, limit 3 gives pages of 3, 3, 1 and an empty cursor on the last.
- `ListReviewOrder`: one each of verified, unverified, stale verified, disputed gives disputed, stale, unverified, verified.
- `SearchText`: three memories, the query `deploy` returns only the one containing it; scope filter applies; a purged memory is not found.
- `SearchTextSyntax`: the queries `c++`, `"quoted`, `ci.go-version`, `AND`, `-x`, `*`, `(` return no error.
- `Edges`: add, duplicate add is a no-op, lookup by either end.
- `VerificationsEvents`: stored and returned oldest first.
- `TxRollback`: an insert inside `Tx` whose fn returns an error is not visible after.
- `ConcurrentInserts`: 20 goroutines insert at once, all succeed, `List` returns 20.

- [ ] **Step 1: Write the failing registry test**

`TestRegistry`: registering `fake` then `Open(ctx, "fake", "x")` calls the func with `"x"`; `Open(ctx, "nope", "")` gives `CodeInvalidArgument` with field `database.driver`; registering `fake` twice panics; `Drivers()` is sorted.

- [ ] **Step 2: Run and see it fail**

Run: `go test ./store/`
Expected: build failure.

- [ ] **Step 3: Implement `memory/store.go`, the registry, and the suite**

- [ ] **Step 4: Run and see it pass**

Run: `go test ./store/ ./memory/ && go vet ./store/storetest/`
Expected: `ok`; vet clean.

- [ ] **Step 5: Commit and push**

`feat(store): add the store interface, registry and conformance suite`

---

### Task 4: migration runner

Closes #2.

**Files:**
- Create: `store/migrate/migrate.go`
- Test: `store/migrate/migrate_test.go` (uses `modernc.org/sqlite` in memory)

**Interfaces:**
- Produces: `func Run(ctx context.Context, db *sql.DB, files fs.FS) (applied []string, err error)`. Files are `NNNN_name.sql` at the root of `files`. Table `schema_migrations(version INTEGER PRIMARY KEY, name TEXT NOT NULL, checksum TEXT NOT NULL, applied_at TEXT NOT NULL)`. Checksum is hex SHA-256 of the file bytes with `\r\n` normalized to `\n` (so a Windows checkout does not look changed). The Postgres advisory lock comes in milestone 4.

- [ ] **Step 1: Write the failing tests**

`TestRun` (table, `fstest.MapFS`): empty database applies `0001_a.sql`, `0002_b.sql` and returns both names; a second run returns none; adding `0003_c.sql` applies only it; a gap (`0001`, `0003`) fails with an error naming `0002`; an applied file whose content changed fails naming the file; a file with bad SQL fails and its table does not exist after (transaction rolled back); a file named `notes.txt` is ignored; CRLF and LF versions of the same file have the same checksum.

- [ ] **Step 2: Run and see them fail**

Run: `go test ./store/migrate/`
Expected: build failure.

- [ ] **Step 3: Implement `Run`**

- [ ] **Step 4: Run and see them pass**

Run: `go test ./store/migrate/ && golangci-lint run ./store/...`
Expected: `ok`, `0 issues.`

- [ ] **Step 5: Commit and push**

`feat(store): add a forward-only migration runner`

---

### Task 5: SQLite store

Closes #3 and #4.

**Files:**
- Create: `store/sqlite/sqlite.go`, `store/sqlite/memories.go`, `store/sqlite/search.go`, `store/sqlite/migrations/0001_init.sql`
- Test: `store/sqlite/sqlite_test.go`, `store/storetest/sqlite_test.go` (package `storetest_test`, so the suite package is covered)

**Interfaces:**
- Consumes: `memory.Store`, `storetest.Run`, `migrate.Run`.
- Produces: `func Open(ctx context.Context, path string) (*Store, error)` registered as `sqlite` in `init`; `*Store` implements `memory.Store`. `path` is a file path (not a URI); `Open` builds the DSN itself with `_pragma=busy_timeout(5000)`, `_pragma=journal_mode(WAL)`, `_pragma=foreign_keys(1)`, `_txlock=immediate`, and runs migrations. `":memory:"` is accepted for tests.

Schema in `0001_init.sql`: `memories` (columns for every `Memory` field; `tags` and `refs` as JSON text; times as RFC 3339 text with nanoseconds in UTC; `PRIMARY KEY (org_id, id)`; indexes on `(org_id, scope, content_hash)`, `(org_id, scope, subject)`, `(org_id, created_at)`), `memories_fts` as FTS5 over `content, subject, tags` with `tokenize='unicode61'` keyed by rowid of `memories` (kept in sync by the store code, not triggers, so purge can clear it), `edges` with a unique `(org_id, from_id, to_id, type)`, `verifications`, `events`.

`SearchText` turns the user query into FTS5 syntax by splitting on whitespace and Unicode punctuation except `.`, `_` and `-` inside words, wrapping each term in double quotes with inner quotes doubled, and joining with ` OR `. An empty result after that returns no hits and no error. Score is `-bm25(memories_fts)`.

- [ ] **Step 1: Write the failing tests**

`TestConformance` in both test files: `storetest.Run(t, func(t *testing.T) memory.Store { s, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "db", "logbook.db")); ...; t.Cleanup(func(){ s.Close() }); return s })`. Create the `db` folder first.

`TestOpenWindowsStylePath`: open under `filepath.Join(t.TempDir(), "my notes", "logbook.db")`, insert, close, reopen, get it back.

`TestRegistered`: `store.Open(ctx, "sqlite", path)` returns a working store.

`TestOpenBadPath`: a path whose parent is an existing file returns an error.

- [ ] **Step 2: Run and see them fail**

Run: `go test ./store/...`
Expected: build failure.

- [ ] **Step 3: Implement the store**

Use `db.SetMaxOpenConns(1)` only if `ConcurrentInserts` still sees `SQLITE_BUSY` with the busy timeout; prefer WAL plus busy timeout first. `Tx` passes a `*Store` bound to the `*sql.Tx` to `fn`. Build `IN (?, ?, ...)` lists with placeholders only, never by formatting values into SQL.

- [ ] **Step 4: Run and see them pass**

Run: `go test ./store/... && golangci-lint run ./store/...`
Expected: all `ok`, `0 issues.`

- [ ] **Step 5: Commit and push**

`feat(store): add the SQLite store with full-text search`. Add a CHANGELOG line: SQLite storage with full-text search. Update AGENTS.md Layout to say which packages now exist.

---

### Task 6: Service: remember

Part of #5.

**Files:**
- Create: `memory/service.go`, `memory/remember.go`, `memory/normalize.go`
- Test: `memory/remember_test.go`, `memory/service_test.go` (shared fixture: a SQLite store from `store/sqlite` in a temp dir, a fixed clock, sequential IDs `mem_1`, `mem_2`, ...; the test package is `memory_test` to avoid an import cycle)

**Interfaces:**
- Consumes: `Store`, `ApplyVerdict`, `ReviewAfter`, `CheckStale`, `Rule`.
- Produces:

```go
type Weights struct{ Verified, Unverified, Disputed, Stale float64 }
var DefaultWeights = Weights{1.0, 0.7, 0.3, 0.5}

type Options struct {
	Now           func() time.Time // default time.Now
	NewID         func() string    // default "mem_" + ULID
	Rules         []Rule           // default none; cmd passes staleness.Default()
	ReviewPeriods map[Kind]time.Duration // default DefaultReviewPeriods
	Weights       Weights          // zero value means DefaultWeights
}

func NewService(s Store, o Options) *Service

type RememberRequest struct {
	Content, Subject, Scope, Project, Team, Corrects, Evidence string
	Kind       Kind
	Tags, Refs, RelatedTo []string
	ValidUntil time.Time
	Extract    bool
	Source     Source // empty means direct
}
type Remembered struct {
	ID, Scope  string
	Status     Status
	Duplicate  bool
	Conflicts  []string
	Superseded []string
}
type RememberResult struct {
	Memories []Remembered
	Degraded []string
}
func (s *Service) Remember(ctx context.Context, p Principal, r RememberRequest) (*RememberResult, error)
func NormalizeContent(s string) string // trim, collapse runs of Unicode space to one space
func ContentHash(s string) string     // hex SHA-256 of strings.ToLower(NormalizeContent(s))
```

Rules, in order:
1. `Extract` true gives `CodeUnavailable` "no extractor configured".
2. Content: must be valid UTF-8, non-empty after `NormalizeContent`, at most 8192 bytes, else `CodeInvalidArgument` with `Field: "content"`. Stored content is `strings.TrimSpace` of the input, so line breaks inside survive.
3. Scope resolution: `Scope` empty means `project` when `Project` or `p.Project` is set, else `user`. `user` means `user:<p.Subject>`. `project` means `project:<Project or p.Project>`, `CodeInvalidArgument` field `project` when both are empty. `team` needs `Team`, field `team`. A full scope string such as `team:platform` is also accepted. No write access gives `CodePermissionDenied`.
4. Duplicate: `FindByHash` in that scope; if found, return it with `Duplicate: true` and write nothing.
5. `Corrects`: must exist (`CodeNotFound`), be writable (`CodePermissionDenied`) and active (`CodeConflict` "memory is already superseded" or forgotten). `RelatedTo`: each must exist and be readable.
6. In one `Tx`: insert the new memory (status unverified, version 1, `ReviewAfter` from the kind, author from `p.Author()`); event `created`; for `Corrects`, set the old one to superseded with `SupersededBy` and `SupersededAt`, update it, add edge new to old `supersedes`, event `superseded` on the old one; otherwise when `Subject` is set, every active memory with that subject in the scope and a different hash becomes disputed (if not already), gets an edge new to old `contradicts`, an event `disputed`, and its ID goes into `Conflicts`; for each `RelatedTo`, an edge new to it `related`.

- [ ] **Step 1: Write the failing tests**

`TestRememberValidation` (table): empty, `"   \n "`, 8193 bytes of ASCII, 2731 Thai characters (3 bytes each, 8193 bytes), and `"\xff"` all give `CodeInvalidArgument` and `Field == "content"`; exactly 8192 bytes succeeds; kind `"note"` gives field `kind`; scope `project` with no project gives field `project`; `Extract: true` gives `CodeUnavailable`.

`TestRememberScopes`: default with no project goes to `user:alice`; with `p.Project = "mothership"` goes to `project:mothership`; `Scope: "team", Team: "sales"` for a principal without that team gives `CodePermissionDenied`.

`TestRememberDuplicate`: the same content with different case and spacing returns the first ID with `Duplicate: true`, and `List` shows one memory.

`TestRememberCorrects`: B corrects A gives A superseded with `SupersededBy == B` and `SupersededAt == now`, an edge `B supersedes A`, and `Superseded == [A]` in the result; correcting A again gives `CodeConflict`; correcting a missing ID gives `CodeNotFound`.

`TestRememberSubjectConflict`: A and B with subject `ci.go-version` and different content give A disputed, `Conflicts == [A]`, an edge `B contradicts A`; C with the same content as B in another scope is not a duplicate of B.

`TestRememberReviewAfter`: an observation at now gets `ReviewAfter == now + 720h`; a preference gets zero.

- [ ] **Step 2: Run and see them fail**

Run: `go test ./memory/`
Expected: build failure.

- [ ] **Step 3: Implement `NewService`, `Remember`, `NormalizeContent`, `ContentHash`**

- [ ] **Step 4: Run and see them pass**

Run: `go test ./memory/ && golangci-lint run ./memory/`
Expected: `ok`, `0 issues.`

- [ ] **Step 5: Commit and push**

`feat(memory): remember with dedupe, corrections and conflicts`

---

### Task 7: Service: recall

Part of #5.

**Files:**
- Create: `memory/recall.go`, `memory/rank.go`
- Test: `memory/recall_test.go`, `memory/rank_test.go`

**Interfaces:**
- Produces:

```go
type RecallRequest struct {
	Query, Project, Team string
	Scopes       []string // "user", "project", "team" or full scope strings
	Kinds        []Kind
	Tags         []string
	Limit        int
	MinStatus    Status // "" or unverified means any active; verified means verified only
	ExcludeStale bool
	Expand       int // 0 or 1
	AsOf         time.Time
}
type Correction struct {
	ID, Content string
	At          time.Time
}
type Recalled struct {
	Memory
	Stale         bool
	StaleReason   string
	Score         float64
	CorrectedFrom *Correction
	Links         []Edge
}
type RecallResult struct {
	Results  []Recalled
	Degraded []string
}
func (s *Service) Recall(ctx context.Context, p Principal, r RecallRequest) (*RecallResult, error)
func (p Principal) ReadableScopes(project, team string) []string // user:<sub>, project:<project or p.Project> if any, team:<t> for each team (or only team when set)
func Fuse(k float64, lists ...[]Hit) []Hit // reciprocal rank fusion, sorted by score, ties by ID
```

Rules:
1. Query empty after trimming gives `CodeInvalidArgument` field `query`. `Limit` 0 means 8; over 50 is capped at 50; negative gives field `limit`. `Expand` outside 0 and 1 gives field `expand`.
2. Scopes: requested entries map to the caller's readable scopes of that type; full scopes must be readable (`CodePermissionDenied`). Empty means every readable scope.
3. Without `AsOf`: `SearchText` with statuses active plus superseded, limit 50. `Fuse(60, hits)`. For a superseded hit, follow `SupersededBy` up to 10 steps to the first active memory; if it is readable, it replaces the hit with `CorrectedFrom` set to the original hit's ID, content and `SupersededAt`; otherwise the hit is dropped. When several hits land on the same memory, keep the best score and the `CorrectedFrom` of the best one; a direct hit on the memory itself beats a corrected one.
4. With `AsOf`: `Filter.AsOf` set, no swapping, all statuses allowed.
5. Weight: score times the status weight, times `Weights.Stale` when `CheckStale` says stale. Set `Stale` and `StaleReason`.
6. Filter by `MinStatus` and `ExcludeStale`, sort by score then newer `CreatedAt`, cut to `Limit`. When `Expand` is 1, attach `Edges` for the result IDs to each result.

- [ ] **Step 1: Write the failing tests**

`TestFuse`: `Fuse(60, [a b c])` gives scores `1/61, 1/62, 1/63`; two lists `[a b]` and `[b a]` give equal scores ordered by ID; empty input gives empty output.

`TestRecallValidation` (table): empty query, limit `-1`, expand `2` give `CodeInvalidArgument` with the matching field; limit `100` returns at most 50.

`TestRecallCorrectionChain`: A "deploys go through Jenkins", B corrects A "deploys go through GitHub Actions", C corrects B "deploys go through Argo CD". Recall `Jenkins` returns C with `CorrectedFrom.ID == A`. Recall `Argo` returns C with `CorrectedFrom == nil`.

`TestRecallTrustOrder`: three memories matching equally; confirmed (by another channel), plain, disputed. Order is verified, unverified, disputed. `MinStatus: verified` returns only the first.

`TestRecallStale`: an observation created 31 days before now is `Stale` with reason `review_due` and ranks below a fresh one; `ExcludeStale` drops it.

`TestRecallScopes`: a memory in `team:sales` is not returned to a principal outside sales; requesting `team:sales` explicitly gives `CodePermissionDenied`; requesting `["user"]` returns only the user scope.

`TestRecallAsOf`: A at day 1, B corrects A at day 5. `AsOf` day 3 returns A and not B. `AsOf` day 6 returns B.

`TestRecallExpand`: with `Expand: 1`, a result that contradicts another has that edge in `Links`.

- [ ] **Step 2: Run and see them fail**

Run: `go test ./memory/ -run 'Fuse|Recall'`
Expected: build failure.

- [ ] **Step 3: Implement `Fuse`, `ReadableScopes`, `Recall`**

- [ ] **Step 4: Run and see them pass**

Run: `go test ./memory/ && golangci-lint run ./memory/`
Expected: `ok`, `0 issues.`

- [ ] **Step 5: Commit and push**

`feat(memory): recall with fusion, trust weights and corrections`

---

### Task 8: Service: verify, forget, list, get

Closes #5.

**Files:**
- Create: `memory/verify.go`, `memory/forget.go`, `memory/list.go`
- Test: `memory/verify_test.go`, `memory/forget_test.go`, `memory/list_test.go`

**Interfaces:**
- Produces:

```go
type VerifyRequest struct {
	ID, Note string
	Verdict  Verdict
}
type VerifyResult struct {
	ID                     string
	Status                 Status
	Confirmations, Disputes int
	Counted                bool
}
func (s *Service) Verify(ctx context.Context, p Principal, r VerifyRequest) (*VerifyResult, error)

type ForgetRequest struct {
	ID, Reason string
	Purge      bool
}
func (s *Service) Forget(ctx context.Context, p Principal, r ForgetRequest) (*Memory, error)

type ListRequest struct {
	Scopes                []string
	Project, Team         string
	Statuses              []Status
	Kind                  Kind
	Subject, Tag, Author  string
	Stale                 bool
	Order                 Order
	Limit                 int
	Cursor                string
}
type ListResult struct {
	Items      []*Memory
	NextCursor string
}
func (s *Service) List(ctx context.Context, p Principal, r ListRequest) (*ListResult, error)
func (s *Service) Get(ctx context.Context, p Principal, id string) (*Memory, error)
```

Rules:
- Verify: missing gives `CodeNotFound`; not writable gives `CodePermissionDenied`; verdict other than confirm or dispute gives field `verdict`. `ApplyVerdict` with the period for the memory's kind; in one `Tx`: `AddVerification`, `Update`, event `confirmed` or `disputed`.
- Forget: allowed when `p.Subject == m.Author.Subject` or the scope is writable, else `CodePermissionDenied`. Already forgotten without purge returns it unchanged. Sets `Status` forgotten and `ForgottenAt`; with `Purge`, also clears `Content`, `Evidence`, `Tags`, `Refs`, sets `Purged`, and the store drops the FTS row. Event `forgotten` or `purged` with `{"reason": ...}`.
- List: scopes as in Recall. `Limit` 0 means 50, over 500 capped. `Stale` sets `Filter.StaleAt` to now. `Order` empty means newest; `review` sets `Filter.Now`. Unknown order gives field `order`.
- Get: not readable is reported as `CodeNotFound`, so IDs in other scopes do not leak.

- [ ] **Step 1: Write the failing tests**

`TestVerify` (table): agent `{alice, mcp}` writes; confirm by the same gives `Counted == false` and status unverified; confirm by `{alice, cli}` gives verified with confirmations 1; dispute by `{bob, mcp}` in a project scope gives disputed; verify on a superseded memory gives `CodeConflict`; verdict `"maybe"` gives field `verdict`; a user-scope memory verified by bob gives `CodePermissionDenied`.

`TestForget`: forget hides from `Recall` and `List`; `List` with `Statuses: [forgotten]` still shows it with content; purge clears content and `Recall` of a word from it finds nothing; bob forgetting alice's user memory gives `CodePermissionDenied`; the author can forget their project memory.

`TestListReviewQueue`: with a disputed, a stale and an unverified memory, `Order: review` returns them in that order; `Stale: true` returns only the stale one; paging with limit 1 walks all three and ends with an empty cursor.

`TestGetHidesOtherScopes`: bob gets alice's user memory and receives `CodeNotFound`.

- [ ] **Step 2: Run and see them fail**

Run: `go test ./memory/ -run 'Verify|Forget|List|Get'`
Expected: build failure.

- [ ] **Step 3: Implement the four methods**

- [ ] **Step 4: Run and see them pass**

Run: `go test ./... && golangci-lint run ./...`
Expected: all `ok`, `0 issues.`

- [ ] **Step 5: Commit and push**

`feat(memory): verify, forget, list and get`. CHANGELOG: memory service with trust states, corrections and review queue.

---

### Task 9: importer interface, JSONL import and export

Closes #11.

**Files:**
- Create: `importer/importer.go`, `importer/jsonl/jsonl.go`, `memory/import.go`
- Test: `importer/importer_test.go`, `importer/jsonl/jsonl_test.go`, `memory/import_test.go`

**Interfaces:**
- Produces:
  - In `memory`: `type Record struct { Memory Memory; Edges []Edge; Verifications []Verification }` with JSON tags `memory`, `edges`, `verifications`; `func (s *Service) Import(ctx context.Context, p Principal, scope string, r Record) (id string, err error)`; `func (s *Service) Export(ctx context.Context, p Principal, scopes []string, fn func(Record) error) error` (all statuses, purged ones skipped).
  - In `importer`: `type Importer interface { Import(ctx context.Context, r io.Reader, emit func(memory.Record) error, skip func(line int, err error)) error }`; `func Register(name string, imp Importer)`; `func Get(name string) (Importer, error)`; `func Names() []string`; `type Report struct { Imported int; Skipped []LineError }`; `type LineError struct { Line int; Err string }`; `func Run(ctx context.Context, svc *memory.Service, p memory.Principal, scope string, imp Importer, r io.Reader) (*Report, error)`.
  - `importer/jsonl`: registered as `jsonl`; `func Write(w io.Writer, rec memory.Record) error` writes one line.

Rules for `Service.Import`: scope is `r.Memory.Scope` when set, else the `scope` argument, else resolved like `Remember`; must be writable. An empty or already used ID gets a new one. Empty status becomes unverified, empty source becomes imported, empty author becomes `p.Author()`, empty kind becomes fact. Content is validated as in `Remember`; a duplicate hash in the scope is skipped with `CodeConflict` "duplicate". `importer.Run` inserts every memory first, keeping a map from old IDs to new ones, then the edges and verifications whose ends were imported or already exist.

- [ ] **Step 1: Write the failing tests**

`TestJSONLRoundTrip`: remember three memories, correct one, confirm one, export to a buffer, import into a fresh service, then `List` gives the same contents, statuses and the `supersedes` edge.

`TestJSONLBadLines`: input with a valid line, `{not json`, an empty line, and a line with empty content gives `Imported == 1` and `Skipped` lines 2 and 4 (empty lines are ignored, not reported).

`TestImportIDCollision`: importing a record whose ID exists gives a new ID and keeps both.

`TestImporterRegistry`: `Get("jsonl")` works; `Get("nope")` gives `CodeInvalidArgument` with field `format`; `Names()` is sorted.

- [ ] **Step 2: Run and see them fail**

Run: `go test ./importer/... ./memory/ -run 'Import|JSONL'`
Expected: build failure.

- [ ] **Step 3: Implement**

- [ ] **Step 4: Run and see them pass**

Run: `go test ./... && golangci-lint run ./...`
Expected: all `ok`, `0 issues.`

- [ ] **Step 5: Commit and push**

`feat(importer): add the importer interface and JSONL import and export`

---

### Task 10: MCP server

Closes #8.

**Files:**
- Create: `internal/mcpserver/server.go`, `internal/mcpserver/tools.go`
- Test: `internal/mcpserver/server_test.go`

**Interfaces:**
- Consumes: `memory.Service`.
- Produces: `func New(svc *memory.Service, principal func(ctx context.Context) memory.Principal, version string) *server.MCPServer` with server name `logbook` and tools `remember`, `recall`, `verify`, `forget`, `list_memories`.

Tool inputs are structs with `json` and `jsonschema` tags matching design section 4 names (`content`, `kind`, `subject`, `scope`, `project`, `team`, `tags`, `refs`, `corrects`, `related_to`, `valid_until`, `evidence`, `extract`; `query`, `scopes`, `kinds`, `limit`, `min_status`, `include_stale`, `expand`, `as_of`; `id`, `verdict`, `note`; `reason`, `purge`; `status`, `kind`, `subject`, `tag`, `author`, `stale`, `order`, `cursor`). `include_stale` is a `*bool`, nil meaning true. Times are RFC 3339 strings; a bad one gives `invalid_argument` with that field. Handlers use `mcp.NewStructuredToolHandler` so results carry `structuredContent` plus JSON text. Service errors become `mcp.NewToolResultError` whose text is `{"error":{"code":...,"message":...,"field":...}}`, never a protocol error. Each tool has a one-sentence description that tells an agent when to call it, for example recall: "Search shared memories before answering a question that may have been answered before."

- [ ] **Step 1: Write the failing tests**

Use `client.NewInProcessClient(srv)`, `Initialize`, then `CallTool`.

`TestListTools`: exactly the five names.

`TestRememberRecallOverMCP`: remember "the staging database is postgres 17", recall "staging" returns it in `structuredContent.results[0].content`.

`TestToolErrors` (table): remember with empty content, recall with empty query, verify a missing ID: each result has `IsError` true and the text parses to the matching code and field.

`TestIncludeStaleDefault`: a stale memory is returned when `include_stale` is omitted and not when it is false.

- [ ] **Step 2: Run and see them fail**

Run: `go test ./internal/mcpserver/`
Expected: build failure.

- [ ] **Step 3: Implement `New` and the five handlers**

- [ ] **Step 4: Run and see them pass**

Run: `go test ./internal/mcpserver/ && golangci-lint run ./internal/...`
Expected: `ok`, `0 issues.`

- [ ] **Step 5: Commit and push**

`feat(mcp): serve the five memory tools`

---

### Task 11: configuration

Part of #9.

**Files:**
- Create: `internal/config/config.go`
- Test: `internal/config/config_test.go`

**Interfaces:**
- Produces:

```go
type Config struct {
	ListenAddr     string   `yaml:"listen_addr"`
	Database       Database `yaml:"database"`
	DefaultProject string   `yaml:"default_project"`
}
type Database struct {
	Driver string `yaml:"driver"`
	DSN    string `yaml:"dsn"`
}
func Default() Config // ":8420", sqlite, "logbook.db", ""
func Load(path string, environ []string) (Config, error)
```

`Load` starts from `Default()`, applies the YAML file when `path` is not empty (unknown keys are an error naming the key), then every `LOGBOOK_*` variable in `environ`: strip the prefix, lowercase, split on `__` for nesting, so `LOGBOOK_DATABASE__DSN` sets `database.dsn`. An environment key that matches no field is an error naming the variable. Implementation hint: decode the file into a `yaml.Node`, set env values as plain scalar nodes at their paths, then decode the node into `Config` with `KnownFields(true)`, so types resolve the same way for file and environment.

- [ ] **Step 1: Write the failing tests**

`TestLoad` (table): no file and no env gives `Default()`; a file setting `database.dsn: /tmp/x.db` sets it; `LOGBOOK_DATABASE__DSN=C:\data\lb.db` overrides the file; `LOGBOOK_LISTEN_ADDR=:9000` sets it; `LOGBOOK_NOPE=1` fails naming `LOGBOOK_NOPE`; a file with `databse:` fails naming `databse`; a missing file fails; variables without the prefix are ignored.

- [ ] **Step 2: Run and see them fail**

Run: `go test ./internal/config/`
Expected: build failure.

- [ ] **Step 3: Implement `Default` and `Load`**

- [ ] **Step 4: Run and see them pass**

Run: `go test ./internal/config/ && golangci-lint run ./internal/...`
Expected: `ok`, `0 issues.`

- [ ] **Step 5: Commit and push**

`feat(config): load YAML with environment overrides`

---

### Task 12: the `logbook` command

Closes #9.

**Files:**
- Create: `cmd/logbook/main.go`, `cmd/logbook/run.go`, `cmd/logbook/commands.go`, `cmd/logbook/output.go`
- Test: `cmd/logbook/run_test.go`

**Interfaces:**
- Consumes: everything above.
- Produces: `func run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer, environ []string) int`. `main` is `os.Exit(run(context.Background(), os.Args[1:], os.Stdin, os.Stdout, os.Stderr, os.Environ()))`.

Subcommands and flags (each with its own `flag.FlagSet`, `--config` and `--project` on all):
- `logbook mcp`: stdio MCP via `server.NewStdioServer(srv).Listen(ctx, stdin, stdout)`; principal `{OrgID: default, Subject: "local", Channel: mcp, Project: --project or config default_project}`.
- `logbook migrate`: opens the store (which migrates) and prints the applied file names.
- `logbook remember <content> [--kind --subject --scope --team --tag (repeatable) --corrects --evidence]`
- `logbook recall <query> [--limit --verified-only --exclude-stale --scope (repeatable)]`
- `logbook list [--status (repeatable) --stale --order newest|review --limit --cursor]`
- `logbook verify <id> confirm|dispute [--note]`
- `logbook forget <id> [--reason --purge]`
- `logbook import <format> <path> [--scope]`, `logbook export [--scope (repeatable)]` to stdout as JSONL
- `logbook version`
- One-shot commands use `Channel: cli` and the same `Subject: "local"`, which is what lets a person's CLI confirm count against an agent's MCP write.
- Every one-shot command takes `--json` for JSON output; without it, one line per memory: `<id>  <status>[ stale]  <scope>  <content>`, and a `corrected from: <old content>` line under a corrected recall result.
- Errors print `error: <message>` to stderr and exit 1; usage errors exit 2.
- `LOGBOOK_NOW` (RFC 3339), when set, fixes the clock. It exists for the end-to-end test and for recorded demos, and AGENTS.md says so.
- Logs: `slog.NewJSONHandler(stderr, ...)`, level warn by default.
- Blank imports in `main.go`: `store/sqlite`, `importer/jsonl`.

- [ ] **Step 1: Write the failing tests**

Each test sets `LOGBOOK_DATABASE__DSN` to a temp file in `environ`.

`TestRunRememberRecall`: `remember "use pnpm, not npm"` prints an ID; `recall pnpm` prints a line containing that ID and `unverified`.

`TestRunVerifyCounts`: write through the service with `Channel: mcp` and `Subject: local`, then `verify <id> confirm` through `run` prints status `verified`.

`TestRunJSON`: `recall pnpm --json` output parses as `memory.RecallResult`.

`TestRunErrors` (table): no subcommand exits 2 with usage on stderr; unknown subcommand exits 2; `remember ""` exits 1 with `error:`; `verify x maybe` exits 1; a bad `--config` path exits 1.

`TestRunExportImport`: remember two, `export` to a buffer, run `import jsonl <file>` against a second database, `list` shows two.

`TestRunMCP`: run `mcp` with `stdin` an `io.Pipe`, write an `initialize` request and a `tools/list` request as JSON lines, read two responses from `stdout`, assert the tool names, close the pipe, and assert `run` returns 0 and nothing but JSON lines reached `stdout`.

`TestRunNowOverride`: with `LOGBOOK_NOW=2030-01-01T00:00:00Z`, an observation remembered then listed with `--stale` after setting `LOGBOOK_NOW=2030-03-01T00:00:00Z` is returned.

- [ ] **Step 2: Run and see them fail**

Run: `go test ./cmd/logbook/`
Expected: build failure.

- [ ] **Step 3: Implement `run` and the subcommands**

- [ ] **Step 4: Run and see them pass**

Run: `go test ./... && golangci-lint run ./... && go build -o logbook.exe ./cmd/logbook && ./logbook.exe version`
Expected: all `ok`, `0 issues.`, `dev`.

- [ ] **Step 5: Commit and push**

`feat(cli): add the logbook command`. Update AGENTS.md Commands with `go run ./cmd/logbook mcp`, the Layout table with the packages that now exist, and the `LOGBOOK_NOW` note. CHANGELOG: `logbook mcp`, one-shot commands, import and export.

---

### Task 13: end-to-end demo story

Closes #15.

**Files:**
- Create: `e2e/demo_test.go` (package `e2e`, behind no build tag: it only needs `go build`)

**Interfaces:**
- Consumes: the built binary and `github.com/mark3labs/mcp-go/client` (`client.NewStdioMCPClient(path, env, "mcp")`).

- [ ] **Step 1: Write the test**

`TestMain` builds `./cmd/logbook` into a temp dir (with `.exe` on Windows) once. `TestDemoStory`, with `LOGBOOK_DATABASE__DSN` in a temp dir and `LOGBOOK_NOW=2026-11-04T00:00:00Z`:
1. MCP `remember` "CI runs on Go 1.25" with subject `ci.go-version`, kind `fact`.
2. MCP `remember` "CI runs on Go 1.26" with the same subject: result lists the first ID in `conflicts`.
3. MCP `remember` "CI runs on Go 1.27" with `corrects` set to the second ID.
4. MCP `recall` "1.26": the top result is the third memory with `corrected_from.content` "CI runs on Go 1.26".
5. CLI `verify <third> confirm --json`: `counted` is true and status is `verified`.
6. Close the MCP client. With `LOGBOOK_NOW=2027-06-01T00:00:00Z`, CLI `list --order review --json`: the first memory is the disputed first one, the second is the third memory, now stale.
7. Assert the MCP server's stderr had no line that failed to parse as JSON.

- [ ] **Step 2: Run it**

Run: `go test ./e2e/`
Expected: `ok`. If a step fails, fix the code in the task that owns it, not the test.

- [ ] **Step 3: Full check**

Run: `go test ./... && golangci-lint run ./... && go test -coverprofile=cover.out ./... && go run github.com/vladopajic/go-test-coverage/v2@v2.21.0 --config=.testcoverage.yml`
Expected: all `ok`, `0 issues.`, total coverage at or above 80%.

- [ ] **Step 4: Commit and push**

`test(e2e): play the demo story over stdio and the CLI`. Then check that CI is green on Linux, macOS and Windows.
