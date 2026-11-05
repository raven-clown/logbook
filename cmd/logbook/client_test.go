package main

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/raven-clown/logbook/internal/config"
	"github.com/raven-clown/logbook/internal/obs"
	"github.com/raven-clown/logbook/memory"
	"github.com/raven-clown/logbook/store/sqlite"
)

type remoteEnv struct {
	*cli
	url string
	key string
}

// newRemote starts the real handlers on a server database and returns a CLI
// whose own database path must stay unused.
func newRemote(t *testing.T) *remoteEnv {
	t.Helper()
	dsn := filepath.Join(t.TempDir(), "server.db")
	key := createKey(t, dsn, "ci")
	st, err := sqlite.Open(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	cfg := config.Default()
	cfg.DefaultProject = "mothership"
	now := func() time.Time { return time.Date(2026, 11, 5, 9, 0, 0, 0, time.UTC) }
	svc := memory.NewService(st, memory.Options{Now: now})
	srv := httptest.NewServer(newHandler(deps{cfg: cfg, store: st, access: st, svc: svc, metrics: obs.NewMetrics(), logger: obs.NewLogger(io.Discard, slog.LevelInfo), now: now}))
	t.Cleanup(srv.Close)
	c := newCLI(t)
	c.env = append(c.env, "LOGBOOK_TOKEN="+key)
	return &remoteEnv{cli: c, url: srv.URL, key: key}
}

func (r *remoteEnv) localDBUnused(t *testing.T) {
	t.Helper()
	if _, err := os.Stat(r.dsn()); err == nil {
		t.Fatal("the local database was opened in --server mode")
	}
}

func TestServerRemember(t *testing.T) {
	r := newRemote(t)
	out := r.ok("remember", "the queue is nats", "--server", r.url, "--subject", "queue", "--tag", "infra")
	if !strings.HasPrefix(out, "mem_") {
		t.Fatalf("remember printed %q", out)
	}
	var res memory.RememberResult
	if err := json.Unmarshal([]byte(r.ok("remember", "the api is in go", "--server", r.url, "--project", "api", "--json")), &res); err != nil {
		t.Fatal(err)
	}
	if res.Memories[0].Scope != "project:api" {
		t.Fatalf("--project did not reach the server: %+v", res)
	}
	if out := r.ok("remember", "the queue is nats", "--server", r.url, "--subject", "queue", "--tag", "infra"); !strings.Contains(out, "already stored") {
		t.Fatalf("duplicate printed %q", out)
	}
	r.localDBUnused(t)
}

func TestServerRecall(t *testing.T) {
	r := newRemote(t)
	r.ok("remember", "the staging database is postgres", "--server", r.url)
	out := r.ok("recall", "staging", "database", "--server", r.url)
	if !strings.Contains(out, "the staging database is postgres") {
		t.Fatalf("recall printed %q", out)
	}
	r.localDBUnused(t)
}

func TestServerList(t *testing.T) {
	r := newRemote(t)
	for _, c := range []string{"one", "two", "three"} {
		r.ok("remember", "note "+c, "--server", r.url)
	}
	out := r.ok("list", "--server", r.url, "--limit", "2")
	if strings.Count(out, "note ") != 2 || !strings.Contains(out, "next: --cursor ") {
		t.Fatalf("list printed %q", out)
	}
	cursor := strings.TrimSpace(out[strings.Index(out, "--cursor ")+len("--cursor "):])
	if out := r.ok("list", "--server", r.url, "--limit", "2", "--cursor", cursor); strings.Count(out, "note ") != 1 {
		t.Fatalf("second page %q", out)
	}
	if out := r.ok("list", "--server", r.url, "--order", "review", "--status", "unverified", "--json"); !strings.Contains(out, `"items"`) {
		t.Fatalf("list --json %q", out)
	}
}

func TestServerVerifyForget(t *testing.T) {
	r := newRemote(t)
	id := strings.TrimSpace(r.ok("remember", "builds take ten minutes", "--server", r.url))
	if out := r.ok("verify", id, "confirm", "--server", r.url, "--note", "timed it"); !strings.Contains(out, "not counted") {
		t.Fatalf("verify by the author printed %q", out)
	}
	if out := r.ok("forget", id, "--server", r.url, "--reason", "wrong", "--purge"); !strings.Contains(out, "purged") {
		t.Fatalf("forget printed %q", out)
	}
	r.localDBUnused(t)
}

func TestServerExport(t *testing.T) {
	r := newRemote(t)
	r.ok("remember", "exported note", "--server", r.url, "--scope", "user")
	out := r.ok("export", "--server", r.url, "--scope", "user")
	var rec memory.Record
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &rec); err != nil || rec.Memory.Content != "exported note" {
		t.Fatalf("export printed %q: %v", out, err)
	}
}

func TestServerNeedsToken(t *testing.T) {
	r := newRemote(t)
	r.env = r.env[:1]
	code, _, errOut := r.run("recall", "x", "--server", r.url)
	if code != 1 || !strings.Contains(errOut, "LOGBOOK_TOKEN") {
		t.Fatalf("no token: %d %s", code, errOut)
	}
	if out := r.ok("recall", "x", "--server", r.url, "--token", r.key); out != "" {
		t.Fatalf("--token: %q", out)
	}
	code, _, errOut = r.run("recall", "x", "--server", r.url, "--token", "lbk_wrong")
	if code != 1 || !strings.Contains(errOut, "unknown API key") {
		t.Fatalf("bad token: %d %s", code, errOut)
	}
}

func TestServerErrorShown(t *testing.T) {
	r := newRemote(t)
	code, _, errOut := r.run("verify", "mem_nope", "confirm", "--server", r.url)
	if code != 1 || !strings.Contains(errOut, "not found") {
		t.Fatalf("not found: %d %s", code, errOut)
	}
	code, _, errOut = r.run("recall", "x", "--server", "http://127.0.0.1:1")
	if code != 1 || errOut == "" {
		t.Fatalf("unreachable server: %d %s", code, errOut)
	}
	for _, args := range [][]string{{"import", "jsonl", "x.jsonl", "--server", r.url}, {"migrate", "--server", r.url}, {"recall", "x", "--server", "ftp://x"}} {
		if code, _, _ := r.run(args...); code == 0 {
			t.Fatalf("%v accepted --server", args)
		}
	}
}
