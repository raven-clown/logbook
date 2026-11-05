package rest_test

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/raven-clown/logbook/internal/identity"
	"github.com/raven-clown/logbook/internal/rest"
	"github.com/raven-clown/logbook/memory"
	"github.com/raven-clown/logbook/store/sqlite"
)

var day0 = time.Date(2026, 11, 5, 9, 0, 0, 0, time.UTC)

type env struct {
	t     *testing.T
	h     http.Handler
	store memory.Store
}

func newEnv(t *testing.T) *env {
	t.Helper()
	st, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "logbook.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	n := 0
	svc := memory.NewService(st, memory.Options{Now: func() time.Time { return day0 }, NewID: func() string { n++; return fmt.Sprintf("mem_%d", n) }})
	mux := http.NewServeMux()
	rest.Register(mux, rest.Options{Service: svc, DefaultProject: "mothership"})
	return &env{t: t, h: mux, store: st}
}

func caller(subject string, teams ...string) identity.Caller {
	return identity.Caller{Principal: memory.Principal{OrgID: memory.DefaultOrg, Subject: subject, Teams: teams}}
}

type reply struct {
	code   int
	header http.Header
	body   []byte
}

func (r reply) json(t *testing.T, v any) {
	t.Helper()
	if err := json.Unmarshal(r.body, v); err != nil {
		t.Fatalf("body %s: %v", r.body, err)
	}
}

func (r reply) errorCode(t *testing.T) (string, string) {
	t.Helper()
	var e struct {
		Error struct{ Code, Field string } `json:"error"`
	}
	r.json(t, &e)
	return e.Error.Code, e.Error.Field
}

func (e *env) do(c *identity.Caller, method, path, body string, header ...string) reply {
	e.t.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	ctx := context.Background()
	if c != nil {
		ctx = identity.WithCaller(ctx, *c)
	}
	req := httptest.NewRequestWithContext(ctx, method, path, rd)
	for i := 0; i+1 < len(header); i += 2 {
		req.Header.Set(header[i], header[i+1])
	}
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	return reply{code: rec.Code, header: rec.Header(), body: rec.Body.Bytes()}
}

func (e *env) remember(c identity.Caller, body string, header ...string) memory.Remembered {
	e.t.Helper()
	r := e.do(&c, http.MethodPost, "/v1/memories", body, header...)
	if r.code != http.StatusCreated && r.code != http.StatusOK {
		e.t.Fatalf("remember %s: %d %s", body, r.code, r.body)
	}
	var res memory.RememberResult
	r.json(e.t, &res)
	return res.Memories[0]
}

func TestRESTRemember(t *testing.T) {
	e := newEnv(t)
	alice := caller("alice")
	r := e.do(&alice, http.MethodPost, "/v1/memories", `{"content":"ci uses go 1.26","subject":"ci.go"}`, "User-Agent", "agent-x/1.0 (linux)")
	if r.code != http.StatusCreated {
		t.Fatalf("status %d %s", r.code, r.body)
	}
	var res memory.RememberResult
	r.json(t, &res)
	if res.Memories[0].Scope != "project:mothership" {
		t.Fatalf("default project not used: %+v", res)
	}
	m, _ := e.store.Get(context.Background(), memory.DefaultOrg, res.Memories[0].ID)
	if m.Author.Channel != memory.ChannelREST || m.Author.Client != "agent-x/1.0 (linux)" || m.Author.Subject != "alice" {
		t.Fatalf("author = %+v", m.Author)
	}
	r = e.do(&alice, http.MethodPost, "/v1/memories", `{"content":"ci uses go 1.26","subject":"ci.go"}`)
	if r.code != http.StatusOK {
		t.Fatalf("duplicate status %d", r.code)
	}
	got := e.remember(alice, `{"content":"in another project"}`, "X-Project", "api")
	if got.Scope != "project:api" {
		t.Fatalf("X-Project ignored: %+v", got)
	}
}

func TestRESTRecall(t *testing.T) {
	e := newEnv(t)
	alice := caller("alice")
	e.remember(alice, `{"content":"the staging database is postgres 17"}`)
	r := e.do(&alice, http.MethodPost, "/v1/recall", `{"query":"staging database"}`)
	var res memory.RecallResult
	r.json(t, &res)
	if r.code != http.StatusOK || len(res.Results) != 1 || res.Results[0].Content != "the staging database is postgres 17" {
		t.Fatalf("recall %d %s", r.code, r.body)
	}
}

func TestRESTList(t *testing.T) {
	e := newEnv(t)
	alice := caller("alice", "platform")
	e.remember(alice, `{"content":"one","scope":"team","team":"platform","kind":"decision"}`)
	e.remember(alice, `{"content":"two","scope":"user"}`)
	e.remember(alice, `{"content":"three","scope":"user"}`)
	var page memory.ListResult
	r := e.do(&alice, http.MethodGet, "/v1/memories?scope=user&limit=1", "")
	r.json(t, &page)
	if r.code != http.StatusOK || len(page.Items) != 1 || page.NextCursor == "" {
		t.Fatalf("page 1: %d %s", r.code, r.body)
	}
	r = e.do(&alice, http.MethodGet, "/v1/memories?scope=user&limit=1&cursor="+page.NextCursor, "")
	page = memory.ListResult{}
	r.json(t, &page)
	if len(page.Items) != 1 || page.NextCursor != "" {
		t.Fatalf("page 2: %s", r.body)
	}
	r = e.do(&alice, http.MethodGet, "/v1/memories?scope=team:platform&kind=decision&status=unverified&status=verified&order=review&stale=false", "")
	page = memory.ListResult{}
	r.json(t, &page)
	if len(page.Items) != 1 || page.Items[0].Content != "one" {
		t.Fatalf("filtered: %s", r.body)
	}
	if !strings.Contains(string(e.do(&alice, http.MethodGet, "/v1/memories?scope=user", "").body), `"items"`) {
		t.Fatal("no items key")
	}
}

func TestRESTDetailForgetVerifyLink(t *testing.T) {
	e := newEnv(t)
	alice, bob := caller("alice", "platform"), caller("bob", "platform")
	a := e.remember(alice, `{"content":"deploys run at noon","scope":"team","team":"platform"}`)
	b := e.remember(alice, `{"content":"the deploy window is one hour","scope":"team","team":"platform"}`)

	r := e.do(&bob, http.MethodPost, "/v1/memories/"+a.ID+"/verify", `{"verdict":"confirm","note":"checked the cron"}`)
	var v memory.VerifyResult
	r.json(t, &v)
	if r.code != http.StatusOK || v.Status != memory.StatusVerified || !v.Counted {
		t.Fatalf("verify %d %s", r.code, r.body)
	}

	r = e.do(&alice, http.MethodPost, "/v1/memories/"+a.ID+"/links", `{"to":"`+b.ID+`"}`)
	var edge memory.Edge
	r.json(t, &edge)
	if r.code != http.StatusCreated || edge.To != b.ID || edge.Type != memory.EdgeRelated {
		t.Fatalf("link %d %s", r.code, r.body)
	}

	r = e.do(&bob, http.MethodGet, "/v1/memories/"+a.ID, "")
	var d memory.Detail
	r.json(t, &d)
	if r.code != http.StatusOK || d.Memory.ID != a.ID || len(d.Edges) != 1 || len(d.Verifications) != 1 || len(d.History) < 3 {
		t.Fatalf("detail %d %s", r.code, r.body)
	}

	r = e.do(&alice, http.MethodDelete, "/v1/memories/"+b.ID+"?reason=old&purge=true", "")
	var gone map[string]any
	r.json(t, &gone)
	if r.code != http.StatusOK || gone["status"] != "forgotten" || gone["id"] != b.ID || gone["content"] != nil {
		t.Fatalf("forget %d %s", r.code, r.body)
	}
}

func TestRESTExport(t *testing.T) {
	e := newEnv(t)
	alice := caller("alice")
	e.remember(alice, `{"content":"first","scope":"user"}`)
	e.remember(alice, `{"content":"second","scope":"user"}`)
	r := e.do(&alice, http.MethodGet, "/v1/export?scope=user", "")
	if r.code != http.StatusOK || r.header.Get("Content-Type") != "application/x-ndjson" {
		t.Fatalf("export %d %v", r.code, r.header)
	}
	n := 0
	sc := bufio.NewScanner(strings.NewReader(string(r.body)))
	for sc.Scan() {
		var rec memory.Record
		if err := json.Unmarshal(sc.Bytes(), &rec); err != nil {
			t.Fatalf("line %q: %v", sc.Text(), err)
		}
		n++
	}
	if n != 2 {
		t.Fatalf("%d lines: %s", n, r.body)
	}
	r = e.do(&alice, http.MethodGet, "/v1/export?scope=team:sales", "")
	if code, field := r.errorCode(t); r.code != http.StatusForbidden || code != "permission_denied" || field != "scopes" {
		t.Fatalf("export of an unreadable scope: %d %s", r.code, r.body)
	}
}

func TestRESTErrors(t *testing.T) {
	e := newEnv(t)
	alice, bob := caller("alice"), caller("bob")
	priv := e.remember(alice, `{"content":"alice only","scope":"user"}`)
	for _, tt := range []struct {
		name, method, path, body string
		header                   []string
		status                   int
		code, field              string
	}{
		{"unknown field", "POST", "/v1/memories", `{"content":"x","contnet":"y"}`, nil, 400, "invalid_argument", "contnet"},
		{"bad json", "POST", "/v1/memories", `{"content":`, nil, 400, "invalid_argument", ""},
		{"too big", "POST", "/v1/memories", `{"content":"` + strings.Repeat("a", 300<<10) + `"}`, nil, 400, "invalid_argument", ""},
		{"bad project header", "POST", "/v1/memories", `{"content":"x"}`, []string{"X-Project", "a b"}, 400, "invalid_argument", "X-Project"},
		{"bad limit", "GET", "/v1/memories?limit=ten", "", nil, 400, "invalid_argument", "limit"},
		{"bad stale", "GET", "/v1/memories?stale=maybe", "", nil, 400, "invalid_argument", "stale"},
		{"bad purge", "DELETE", "/v1/memories/" + priv.ID + "?purge=2", "", nil, 400, "invalid_argument", "purge"},
		{"not found", "GET", "/v1/memories/mem_nope", "", nil, 404, "not_found", "id"},
		{"other user", "GET", "/v1/memories/" + priv.ID, "", nil, 404, "not_found", "id"},
		{"denied scope", "POST", "/v1/recall", `{"query":"x","scopes":["team:sales"]}`, nil, 403, "permission_denied", "scopes"},
		{"bad verdict", "POST", "/v1/memories/" + priv.ID + "/verify", `{"verdict":"maybe"}`, nil, 404, "not_found", "id"},
		{"link to a hidden memory", "POST", "/v1/memories/" + priv.ID + "/links", `{"to":"mem_nope"}`, nil, 404, "not_found", "id"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r := e.do(&bob, tt.method, tt.path, tt.body, tt.header...)
			code, field := r.errorCode(t)
			if r.code != tt.status || code != tt.code || field != tt.field {
				t.Fatalf("%d %s, want %d %s on %q", r.code, r.body, tt.status, tt.code, tt.field)
			}
		})
	}
	r := e.do(&alice, http.MethodPost, "/v1/memories/"+priv.ID+"/links", `{}`)
	if code, field := r.errorCode(t); code != "invalid_argument" || field != "to" {
		t.Fatalf("link without to: %s", r.body)
	}
}

func TestRESTNoCaller(t *testing.T) {
	e := newEnv(t)
	r := e.do(nil, http.MethodGet, "/v1/memories", "")
	if code, _ := r.errorCode(t); r.code != http.StatusUnauthorized || code != "unauthenticated" {
		t.Fatalf("%d %s", r.code, r.body)
	}
}

func TestRESTInternalMasked(t *testing.T) {
	e := newEnv(t)
	alice := caller("alice")
	_ = e.store.Close()
	r := e.do(&alice, http.MethodGet, "/v1/memories", "")
	if code, _ := r.errorCode(t); r.code != http.StatusInternalServerError || code != "internal" || strings.Contains(string(r.body), "sql") {
		t.Fatalf("%d %s", r.code, r.body)
	}
}
