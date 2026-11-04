# Milestone 1: Core

The first milestone from [design.md](design.md) section 10: the memory model, trust states, corrections, staleness, SQLite with migrations, MCP over stdio and the CLI. At the end of it, `logbook mcp` works in any MCP client with no server and no model.

Posted on GitHub as #1 to #16 under the [Core milestone](https://github.com/raven-clown/logbook/milestone/1), with the same numbers as below. Issues marked `claimed` are being built by the maintainer now. The `good first issue` and `help wanted` ones are open for anyone. Where an issue depends on another, it says so, so nobody starts on an interface that is not there yet.

---

## 1. memory: types, scopes and trust states

Labels: `enhancement`, `claimed`

**Why:** Everything else builds on one set of types and one place that decides what a status change means.

**Proposed API:** package `memory` with `Memory`, `Draft`, `Edge`, `Verification`, `Event`, `Filter`, `Hit`, `Principal`, `Scope` (parsed from `user:<sub>`, `project:<id>`, `team:<id>`), `Kind`, `Status`. Functions `ParseScope`, `(Principal).CanRead(Scope)`, `(Principal).CanWrite(Scope)`, and `ApplyVerdict(m *Memory, v Verification) (counted bool)`.

**Behavior:**
- Five statuses: `unverified`, `verified`, `disputed`, `superseded`, `forgotten`.
- A confirm from the same subject over the same channel as the author is recorded but not counted.
- Status follows the latest counted verdict. Confirms reset `last_confirmed_at` and `review_after`.
- Superseded and forgotten memories cannot be verified.
- Every type carries `org_id`, `default` in v1.

**Tests:** table-driven transitions for every status and verdict pair, the counted-confirm rule across subjects and channels, scope parsing with bad input, read and write rules for each scope type.

---

## 2. store/migrate: forward-only migration runner

Labels: `enhancement`, `claimed`

**Why:** The contract requires a versioned schema that migrates at startup.

**Proposed API:** `migrate.Run(ctx, db *sql.DB, dialect string, files fs.FS) error`, a `schema_migrations` table with version and applied time.

**Behavior:** runs numbered `NNNN_name.sql` files in order, each in a transaction, skips applied ones, fails on a gap or on a file that changed after it was applied (checksum).

**Tests:** empty database, partial database, a changed file, a broken file rolls back.

---

## 3. store/sqlite: schema, CRUD and full-text search

Labels: `enhancement`, `claimed`

Depends on 1 and 2.

**Why:** The default backend, so one binary runs with no setup.

**Proposed API:** `sqlite.Open(ctx, dsn string) (*Store, error)` implementing `store.Store` from the design, registered as `sqlite`.

**Behavior:** WAL mode on `modernc.org/sqlite`; tables `memories`, `memories_fts` (FTS5), `edges`, `verifications`, `events`; every query filters by `org_id`; cursor pagination on `List`.

**Tests:** run the conformance suite from issue 4 against a temp file.

---

## 4. store/storetest: conformance suite for backends

Labels: `enhancement`, `claimed`

Depends on 1.

**Why:** Contributors adding a backend need one suite that tells them it behaves like the built-in one.

**Proposed API:** `storetest.Run(t *testing.T, open func(t *testing.T) store.Store)`.

**Behavior:** covers insert, get, update, list with every filter and cursor paging, text search ranking, edges, verifications, events, `org_id` isolation and transactions.

**Tests:** the suite itself runs against SQLite in issue 3.

---

## 5. memory.Service: remember, recall, forget, list, verify

Labels: `enhancement`, `claimed`

Depends on 1 and 4.

**Why:** The five operations with the rules from design sections 3 and 5: dedupe, corrections, subject conflicts, links, ranking.

**Proposed API:** `memory.NewService(store.Store, Options) *Service` with `Remember`, `Recall`, `Forget`, `List`, `Verify`, each taking a `Principal`.

**Behavior:** exact duplicates return the existing id; `corrects` supersedes and links; a subject collision marks the older memory `disputed` and adds a `contradicts` edge; recall fuses ranks, applies trust and staleness weights, swaps superseded hits for their replacement with `corrected_from`; `as_of` returns what was believed at that time.

**Tests:** each rule on a fake store with a fixed clock, plus the full correction story.

---

## 6. staleness: rule interface and the age rule

Labels: `enhancement`, `claimed`

Depends on 1.

**Why:** Memories need to be flagged when they are old enough to need another look.

**Proposed API:** `staleness.Rule` from the design, `staleness.Age(periods map[memory.Kind]time.Duration)`, and `staleness.Check(ctx, rules, m, now) (stale bool, reason string)`.

**Behavior:** defaults: observation 30 days, fact and procedure 180, decision 365, preference never. `review_after` is reset by a counted confirm.

**Tests:** each kind at, before and after its period, preference never stale.

---

## 7. staleness: `valid_until` rule

Labels: `good first issue`, `enhancement`

Depends on 6.

**Why:** A writer can say a memory stops being true at a given time, such as "the freeze lasts until Friday". Past that time it should show as stale.

**Proposed API:** `staleness.Expiry()` returning a `Rule` named `expiry`.

**Behavior:** stale with reason `expired` when `ValidUntil` is set and `now` is after it. Not stale when `ValidUntil` is zero.

**Tests:** zero value, before, exactly at, and after the expiry time.

**Where:** add `staleness/expiry.go` and `staleness/expiry_test.go`, then add the rule to the default list in `staleness/staleness.go`.

---

## 8. internal/mcpserver: the five tools over stdio

Labels: `enhancement`, `claimed`

Depends on 5.

**Why:** The main way agents use logbook.

**Proposed API:** tools `remember`, `recall`, `verify`, `forget`, `list_memories` with the inputs and outputs in design section 4, on `github.com/mark3labs/mcp-go`.

**Behavior:** results as JSON text plus structured content; errors carry the shared error code with `isError: true`; logs go to stderr.

**Tests:** each tool through the `mcp-go` in-process client, including bad input and permission errors.

---

## 9. cmd/logbook: `mcp`, `migrate` and one-shot commands

Labels: `enhancement`, `claimed`

Depends on 3, 5 and 8.

**Why:** One binary that starts the MCP server and lets a person run single operations from a shell.

**Proposed API:** `logbook mcp`, `logbook migrate`, `logbook remember | recall | list | forget | verify`, with `--config` and `LOGBOOK_*` overrides using `__` for nested keys.

**Behavior:** with no config, uses `./logbook.db`. One-shot commands use the `cli` channel, so a person can confirm what an agent wrote.

**Tests:** each subcommand against a temp database, driven through a `run(args, stdout, stderr) int` function.

---

## 10. Default project from the git repository in stdio mode

Labels: `good first issue`, `enhancement`

Depends on 9.

**Why:** In stdio mode most people run logbook inside a repository, so memories should land in that project without any setting.

**Proposed API:** `func DefaultProject(dir string) string` in `internal/project`.

**Behavior:** walk up from `dir` to the first folder that has a `.git` entry (a folder or a file, for worktrees) and return its base name. With no repository, return the base name of `dir`. A `--project` flag or the `default_project` setting wins over it.

**Tests:** a nested folder inside a repository, a worktree where `.git` is a file, no repository at all, use `t.TempDir()` for all of them.

**Where:** new `internal/project/project.go` and `project_test.go`, called from `cmd/logbook` where the stdio principal is built.

---

## 11. importer: interface, JSONL import and export

Labels: `enhancement`, `claimed`

Depends on 5.

**Why:** Backups, moving between instances, and a round-trip format other importers can be tested against.

**Proposed API:** `importer.Importer` from the design, a registry by name, `importer/jsonl`, `logbook import jsonl <path>` and `logbook export`.

**Behavior:** one memory per line with its edges and verifications; import keeps ids when they do not collide and reports skipped lines with their line number.

**Tests:** export then import gives the same memories, a bad line is reported and skipped.

---

## 12. importer/markdown

Labels: `good first issue`, `adapter`

Depends on 11.

**Why:** Many teams keep notes for agents in Markdown files at the root of a repository. Importing them is the fastest way to fill logbook on day one.

**Proposed API:** registered as `markdown`, so `logbook import markdown NOTES.md` works.

**Behavior:** each list item becomes a memory with `source: imported`. The nearest heading above it becomes the `subject`, lowercased with spaces turned into dots. Nested list items are joined to their parent with a space. Code blocks are skipped. Empty items are skipped.

**Tests:** headings at several levels, nested lists, code blocks, a file with no headings, an empty file.

**Where:** new `importer/markdown/markdown.go` and `markdown_test.go`, plus a blank import in `cmd/logbook/main.go`.

---

## 13. `logbook review`: walk the review queue

Labels: `help wanted`, `enhancement`

Depends on 9.

**Why:** People need a quick way to confirm, dispute or skip what agents wrote, before the console exists.

**Proposed API:** `logbook review [--scope ...] [--limit N]`.

**Behavior:** shows memories in review order (disputed, then stale, then unverified), one at a time with their links and evidence, and reads a single key: `c` confirm, `d` dispute (asks for a note), `s` skip, `q` quit. Works with piped input so it can be tested.

**Tests:** a scripted session through piped input covering every key, an empty queue, and quitting midway.

---

## 14. Trigram search for languages without spaces

Labels: `help wanted`, `enhancement`

Depends on 3.

**Why:** Word-based full-text search finds nothing in Thai, Chinese or Japanese text, because those languages do not put spaces between words.

**Proposed API:** setting `search.tokenizer` with values `unicode` (default) and `trigram`.

**Behavior:** `trigram` builds the FTS5 table with the `trigram` tokenizer. Changing the setting rebuilds the index. Queries shorter than three characters fall back to a `LIKE` match.

**Tests:** Thai, Japanese and English queries under both tokenizers, a short query, switching tokenizers on an existing database.

---

## 15. End-to-end test of the demo story

Labels: `enhancement`, `claimed`

Depends on 9.

**Why:** One test that proves the whole first milestone works the way the README says.

**Behavior:** build the binary, run `logbook mcp` over stdio, remember a fact, remember a conflicting one, correct it, recall with the old wording and get the new answer, confirm it from the CLI, move the clock past the review date and find it in the review queue.

**Tests:** this issue is the test.

---

## 16. README quickstart

Labels: `good first issue`, `docs`

Depends on 9.

**Why:** Once `logbook mcp` works, the README should show a newcomer how to try it in a minute.

**Behavior:** a "Quickstart" section with install (`go install github.com/raven-clown/logbook/cmd/logbook@latest`), the MCP client config, and a short sample exchange: remember, recall, correct, recall again.

**Tests:** follow the steps on a clean machine and paste the output into the pull request.

**Where:** `README.md`, replacing the "How it will look" section.
