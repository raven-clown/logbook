# Milestone 2: Server

**Goal:** `logbook serve` answers MCP over Streamable HTTP at `/mcp` and REST under `/v1/`, checks OIDC tokens and API keys, applies role bindings to scope access, and serves `/healthz`, `/readyz`, `/metrics`, JSON logs, request IDs and OTLP traces.

**Architecture:** One `http.ServeMux` built in `cmd/logbook`. Every request passes request ID, tracing, metrics and identity middleware before reaching the MCP handler from `mcp-go` or the REST handlers. Identity turns a bearer token into a `memory.Principal` plus roles, using an OIDC verifier and the `api_keys` and `bindings` tables. MCP and REST share one set of JSON request types, so the two can't drift.

**Tech stack:** Go 1.26, `net/http` routing patterns, `github.com/mark3labs/mcp-go` v1.5.0 Streamable HTTP, `github.com/coreos/go-oidc/v3`, `github.com/go-jose/go-jose/v4` (tests), `github.com/prometheus/client_golang`, `go.opentelemetry.io/otel` with the OTLP HTTP trace exporter.

**Spec:** `docs/design.md`, sections 3 (Scope, Roles and bindings), 4 (MCP, REST, Errors, CLI), 5 (packages), 7, 8 and 9. Contract: `mothership/CONTRACT.md` at 53865f1, Identity, Headers and Operations.

## Global constraints

- Pure Go, no cgo. Tests do no network outside `httptest`. No bare `time.Sleep`.
- Errors everywhere: `{"error": {"code", "message", "field"}}`, statuses from design section 4 Errors.
- `org_id` on every new table, `default` in every query.
- Logs are JSON on stdout for `serve`, on stderr for `mcp` and the other commands.
- API keys are `lbk_` plus 43 base64url characters (32 random bytes), stored as a SHA-256 hex hash, shown once.
- Binding principals: `user:<subject>`, `group:<name>`, `key:<name>`. Roles: `admin`, `viewer`, `member`, `verifier`. Targets: `*`, `project:<id>`, `project:*`, `team:<id>`, `team:*`; `admin` and `viewer` take only `*`, `member` and `verifier` never take `*`.
- `project_mode` is `open` (default) or `restricted`. The teams claim defaults to `groups`.
- Request bodies over 256 KiB are `invalid_argument`.

## Review focus

1. OIDC subjects with characters the M1 scope regex rejects (`auth0|123`, URLs): the caller must still get a user scope.
2. A token or key that names one principal must never read another's user scope, including a JWT whose subject is `key:<name>`.
3. Restricted project mode with a current project the caller is not a member of: recall must leave that project out, not fail.
4. The identity provider is down at startup: the server still starts, key callers still work, JWT callers get `unavailable`.
5. MCP over HTTP without a token, with a bad token, and with `X-Project`: 401 with `WWW-Authenticate`, and the project reaches the tools.

---

### Task 1: Config for auth, telemetry and the public URL

**Files:** `internal/config/config.go`, `internal/config/config_test.go`

**Produces:**
```go
type Config struct {
    ListenAddr     string    `yaml:"listen_addr"`
    PublicURL      string    `yaml:"public_url"`
    Database       Database  `yaml:"database"`
    DefaultProject string    `yaml:"default_project"`
    Auth           Auth      `yaml:"auth"`
    Telemetry      Telemetry `yaml:"telemetry"`
}
type Auth struct {
    BootstrapAdmins []string `yaml:"bootstrap_admins"`
    OIDC            OIDC     `yaml:"oidc"`
    ProjectMode     string   `yaml:"project_mode"`
}
type OIDC struct{ Issuer, Audience, TeamsClaim, ProjectsClaim string } // yaml: issuer, audience, teams_claim, projects_claim
type Telemetry struct{ OTLPEndpoint string `yaml:"otlp_endpoint"` }
func (c Config) Validate() error
```

Tests: `TestDefaultsForAuth` (project_mode open, teams_claim groups); `TestEnvList` (`LOGBOOK_AUTH__BOOTSTRAP_ADMINS=user:a, key:ci` gives two trimmed entries); `TestValidate` table: bad project_mode, issuer without audience, bootstrap admin `alice` (no type), public_url not http(s), all name the key in the error; `TestReservedVariables` adds `LOGBOOK_SECRET_KEY` and `LOGBOOK_TEST_POSTGRES_DSN`. `Load` calls `Validate`.

### Task 2: Principal access from bindings

**Files:** `memory/scope.go`, `memory/rank.go`, `memory/scope_test.go`, `memory/rank_test.go`

**Produces:** new `Principal` fields `Projects []string`, `AllProjects, AllTeams, RestrictProjects bool`. `CanRead(project)` is true when not restricted, or `AllProjects`, or the id is in `Projects`. `CanRead(team)` is `AllTeams` or in `Teams`. `ReadableScopes` drops the current project when the principal cannot read it. User scope ids accept any printable non-space text up to 255 bytes, so `user:auth0|123` and `user:key:ci` parse; project and team ids keep the M1 rule.

Tests: `TestRestrictedProjects`, `TestWildcardAccess`, `TestReadableScopesSkipsUnreadableProject`, `TestParseUserScopeWide` (`user:auth0|123`, `user:https://idp/x`, `user:key:ci` parse; `user:a b`, `user:` with a control rune, 256 bytes fail).

### Task 3: admin types and storage for keys and bindings

**Files:** `admin/admin.go`, `admin/keys.go`, `admin/admin_test.go`, `store/sqlite/migrations/0002_access.sql`, `store/sqlite/access.go`, `store/storetest/access.go`, `store/sqlite/sqlite_test.go`

**Produces:**
```go
package admin
type Role string // RoleAdmin, RoleViewer, RoleMember, RoleVerifier
type APIKey struct { OrgID, ID, Name, Hash, Last4 string; CreatedAt, LastUsedAt time.Time; Version int }
type Binding struct { OrgID, ID, Principal string; Role Role; Target string; CreatedAt time.Time; Version int }
func (b Binding) Validate() error            // memory.Error invalid_argument naming principal, role or target
func ValidKeyName(name string) error         // [a-z0-9][a-z0-9._-]{0,63}
func NewKey() (plaintext, hash, last4 string, err error)
func HashKey(plaintext string) string
type Store interface {
    CreateAPIKey(ctx context.Context, k *APIKey) error         // conflict on a taken name
    APIKeyByHash(ctx context.Context, orgID, hash string) (*APIKey, error)
    ListAPIKeys(ctx context.Context, orgID string) ([]*APIKey, error)
    DeleteAPIKey(ctx context.Context, orgID, id string) error  // not_found when missing
    TouchAPIKey(ctx context.Context, orgID, id string, at time.Time) error
    CreateBinding(ctx context.Context, b *Binding) error       // conflict on the same principal, role and target
    Bindings(ctx context.Context, orgID string, principals []string) ([]*Binding, error) // nil principals: all
    DeleteBinding(ctx context.Context, orgID, id string) error
}
func storetest.RunAdmin(t *testing.T, open func(t *testing.T) admin.Store)
```
IDs are `key_` and `bnd_` plus a ULID. Lists are ordered by creation; paging arrives with the admin API in milestone 3.

Tests: `TestBindingValidate` table over the constraint list above; `TestNewKey` (prefix, length, hash matches `HashKey`, two keys differ); conformance checks `KeyRoundTrip`, `KeyNameTaken`, `KeyDelete`, `KeyTouch`, `BindingRoundTrip`, `BindingDuplicate`, `BindingsByPrincipal`, `BindingDelete`, `OrgSeparation`.

### Task 4: identity

**Files:** `internal/identity/identity.go`, `internal/identity/oidc.go`, `internal/identity/middleware.go`, tests beside them

**Consumes:** Tasks 1 to 3.

**Produces:**
```go
type Caller struct { Principal memory.Principal; Roles []admin.Role }
func (c Caller) Has(r admin.Role) bool
type Options struct {
    Store admin.Store; OIDC config.OIDC; ProjectMode string; BootstrapAdmins []string
    Now func() time.Time; HTTPClient *http.Client
}
func New(o Options) *Resolver
func (r *Resolver) Authenticate(ctx context.Context, token string) (Caller, error)
func Local() Caller                                   // subject "local", admin
func WithCaller(ctx context.Context, c Caller) context.Context
func FromContext(ctx context.Context) (Caller, bool)
func Bearer(h http.Header) string
func Middleware(r *Resolver, fail func(w http.ResponseWriter, req *http.Request, err error)) func(http.Handler) http.Handler
```
Keys: `lbk_` tokens are looked up by hash; the subject is `key:<name>`; last use is written at most once a minute. JWTs: discovery is lazy and retried on the next request after a failure, which returns `unavailable`; a bad signature, issuer, audience or expiry is `unauthenticated`; the teams and projects claims take a string or a list, and entries that are not valid scope ids are dropped; a subject that is empty or starts with `key:` is rejected. Bindings are loaded for the subject's principal form plus `group:<team>` for every team claim: `member` on `team:x`, `project:x`, `team:*`, `project:*` fill `Teams`, `Projects`, `AllTeams`, `AllProjects`; `admin` and `viewer` become roles; a bootstrap admin gets `admin`.

Tests (local JWKS from `httptest`, tokens signed with go-jose): `TestAPIKey`, `TestUnknownKey`, `TestJWT`, `TestJWTRejected` table (wrong audience, wrong issuer, expired, unknown signing key, `key:` subject, empty subject), `TestIssuerDownThenUp`, `TestClaimShapes`, `TestBindingsGrantAccess`, `TestGroupBinding`, `TestBootstrapAdmin`, `TestRestrictedMode`, `TestMiddleware` (no header, malformed header, good key).

### Task 5: observability

**Files:** `internal/obs/log.go`, `internal/obs/requestid.go`, `internal/obs/metrics.go`, `internal/obs/trace.go`, tests

**Produces:**
```go
func NewLogger(w io.Writer, level slog.Level) *slog.Logger // JSON; adds request_id and trace_id from the context
func RequestID(next http.Handler) http.Handler            // keeps a valid X-Request-ID (1 to 128 visible ASCII), else a new ULID; echoes it
func RequestIDFrom(ctx context.Context) string
type Metrics struct{ /* registry, counters */ }
func NewMetrics() *Metrics
func (m *Metrics) Wrap(next http.Handler) http.Handler    // logbook_http_requests_total{method,route,code}, logbook_http_request_duration_seconds{method,route}
func (m *Metrics) Handler() http.Handler
func SetupTracing(ctx context.Context, endpoint, version string) (shutdown func(context.Context) error, err error)
func Trace(next http.Handler) http.Handler                // extracts traceparent/tracestate, one server span per request
func AccessLog(l *slog.Logger, next http.Handler) http.Handler
```
`route` is the matched `ServeMux` pattern, or `unmatched`. With no endpoint, tracing installs the W3C propagator and no exporter.

Tests: `TestRequestIDKept`, `TestRequestIDReplaced` (empty, 129 bytes, control rune), `TestLoggerAddsIDs`, `TestMetricsCount` (two requests on one pattern, one unmatched, scrape shows the counts), `TestTraceparentContinues` (the span's trace id equals the incoming one, using an in-memory span recorder), `TestSetupTracingNoEndpoint`.

### Task 6: shared request types

**Files:** `internal/wire/wire.go`, `internal/wire/wire_test.go`, `internal/mcpserver/tools.go`

Move the five MCP argument structs to `internal/wire` as `Remember`, `Recall`, `Verify`, `Forget`, `List` with `ToRequest() (memory.XRequest, error)` methods (time parsing and enum conversion move with them). MCP behaviour does not change: the existing `internal/mcpserver` tests are the check, plus `TestToRequestTimes` for the RFC 3339 errors naming the field.

### Task 7: memory detail and links

**Files:** `memory/detail.go`, `memory/detail_test.go`

**Produces:**
```go
type Detail struct {
    Memory        *Memory        `json:"memory"`
    Edges         []Edge         `json:"edges"`
    Verifications []Verification `json:"verifications"`
    History       []Event        `json:"history"`
}
func (s *Service) Detail(ctx context.Context, p Principal, id string) (*Detail, error)
func (s *Service) Link(ctx context.Context, p Principal, from, to string) (*Edge, error)
```
`Detail` hides edges whose other end the caller can't read. `Link` needs write access to `from`'s scope and read access to `to`, rejects `from == to`, adds a `related` edge and a `linked` event, and is idempotent.

Tests: `TestDetail`, `TestDetailHidesEdges`, `TestDetailNotFoundAcrossScopes`, `TestLink`, `TestLinkRules` table (self link, unreadable target is not_found on `to`, no write on `from` is permission_denied, forgotten target is not_found).

### Task 8: REST under /v1/

**Files:** `internal/httpx/httpx.go`, `internal/rest/rest.go`, `internal/rest/rest_test.go`

**Produces:**
```go
package httpx
func Status(c memory.Code) int
func WriteJSON(w http.ResponseWriter, status int, v any)
func WriteError(w http.ResponseWriter, r *http.Request, err error) // internal errors logged, message masked
func DecodeJSON(r *http.Request, v any) error                       // 256 KiB, unknown fields are invalid_argument naming the field
package rest
type Options struct { Service *memory.Service; DefaultProject string }
func Register(mux *http.ServeMux, o Options)
```
Routes and statuses: `POST /v1/memories` (201, 200 when every memory was a duplicate), `POST /v1/recall`, `GET /v1/memories` (query parameters `scope` and `status` repeatable, the rest as in `list_memories`), `GET /v1/memories/{id}` (Detail), `DELETE /v1/memories/{id}` (`purge`, `reason`), `POST /v1/memories/{id}/verify`, `POST /v1/memories/{id}/links` (`{"to": id}`, 201), `GET /v1/export` (`application/x-ndjson`). The principal is the caller from the context with channel `rest`, the client from `User-Agent` (first 100 printable bytes), the project from `X-Project` (an invalid one is 400 naming `X-Project`) or `DefaultProject`.

Tests through `httptest` with a caller put in the context: one per route, `TestRESTErrors` table (unknown field, bad JSON, body too big, bad `X-Project`, bad `limit`, not found, permission denied, internal masked), `TestRESTNoCaller` (401).

### Task 9: MCP over Streamable HTTP

**Files:** `internal/mcpserver/http.go`, `internal/mcpserver/http_test.go`, `internal/mcpserver/server.go`

**Produces:**
```go
type HTTPOptions struct { DefaultProject string; ResourceMetadata *server.ProtectedResourceMetadataConfig }
func HTTPHandler(s *server.MCPServer, o HTTPOptions) http.Handler
func HTTPPrincipal(ctx context.Context) memory.Principal // for New: caller from context, channel mcp, project from X-Project or the default
```
Stateless Streamable HTTP. The client name comes from the MCP client info when the session has it, else from `User-Agent`. The stdio command also fills the client name from client info.

Tests with the `mcp-go` client over `httptest`: `TestHTTPTools` (remember then recall with a key caller), `TestHTTPProjectHeader`, `TestHTTPClientName`.

### Task 10: `logbook serve`

**Files:** `cmd/logbook/serve.go`, `cmd/logbook/serve_test.go`, `cmd/logbook/run.go`, `store/sqlite/sqlite.go` (`Ping`)

Wires Tasks 1 to 9: `/healthz` (`{"status":"ok"}`), `/readyz` (200 `{"status":"ready","database":"ok","transponder":"not configured"}`, 503 when the database ping fails), `/metrics`, `/mcp`, `/v1/`, the protected resource metadata when `public_url` and an issuer are set. Identity applies to `/mcp` and `/v1/`; a failure is 401 with `WWW-Authenticate: Bearer` (and `resource_metadata` when served) or 503 for `unavailable`. `--listen` overrides `listen_addr`. Logs JSON on stdout, one `listening` line with the bound address. Shuts down within 10 seconds of SIGINT or SIGTERM. `ReadHeaderTimeout` 10s.

Tests: `TestServeRoutes` runs `serve` in-process on `127.0.0.1:0` with a key created in the same database and checks health, ready, metrics, a REST call with and without the key, a request ID echo and shutdown on context cancel; `TestReadyzDatabaseDown`.

### Task 11: keys and bindings from the command line

**Files:** `cmd/logbook/access.go`, `cmd/logbook/access_test.go`

`logbook key create <name>` prints the key once (with `--json`, `{"id","name","key"}`), `key list`, `key delete <id>`; `logbook binding add <principal> <role> <target>`, `binding list`, `binding delete <id>`. They run on the local database as the local admin. Tests: `TestKeyCommands`, `TestBindingCommands`, `TestBindingRejected`.

### Task 12: `--server` for the memory commands

**Files:** `cmd/logbook/client.go`, `cmd/logbook/client_test.go`, `cmd/logbook/commands.go`

`remember`, `recall`, `list`, `verify`, `forget` and `export` take `--server <url>` and `--token` (else `LOGBOOK_TOKEN`) and call `/v1/`, printing the same output as local mode. `--project` becomes `X-Project`. `import` and `migrate` with `--server` are usage errors. A server error prints its code and message. Tests against an `httptest` server running the real handlers: `TestServerRemember`, `TestServerRecall`, `TestServerList`, `TestServerVerifyForget`, `TestServerExport`, `TestServerNeedsToken`, `TestServerErrorShown`.

### Task 13: end-to-end and docs

**Files:** `e2e/serve_test.go`, `README.md`, `AGENTS.md`, `CHANGELOG.md`, `docs/design.md`

`TestServeEndToEnd` builds the binary, creates a key, starts `logbook serve` on `127.0.0.1:0`, reads the bound address from the `listening` log line, plays the demo story over MCP HTTP for one agent and the CLI with `--server` for the person, and checks the review queue, `/metrics` and a 401 without a token. Docs: README gets a "Run it as a server" section (serve, keys, bindings, OIDC, `X-Project`), AGENTS.md gets the new commands and packages, CHANGELOG lines, and design.md records the rulings that changed it (`public_url`, user scope ids, the `key:` subject rule, lazy discovery).
