package rest_test

import (
	"context"
	"encoding/json"
	"fmt"
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

var fuzzRoutes = []struct{ method, path string }{
	{http.MethodPost, "/v1/memories"},
	{http.MethodPost, "/v1/recall"},
	{http.MethodPost, "/v1/memories/mem_1/verify"},
	{http.MethodPost, "/v1/memories/mem_1/links"},
	{http.MethodDelete, "/v1/memories/mem_2"},
}

// fuzzServer is a fresh store holding mem_1 and mem_2, so each input starts from the same state.
func fuzzServer(t *testing.T) (http.Handler, context.Context) {
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
	ctx := identity.WithCaller(context.Background(), caller("alice", "platform"))
	for _, body := range []string{`{"content":"first"}`, `{"content":"second"}`} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequestWithContext(ctx, http.MethodPost, "/v1/memories", strings.NewReader(body)))
		if rec.Code >= 300 {
			t.Fatalf("seeding %s: %d %s", body, rec.Code, rec.Body)
		}
	}
	return mux, ctx
}

// FuzzREST sends any body to the /v1/ routes that take one. A bad body is the caller's mistake: it must get a 4xx with a JSON error, never a 5xx.
func FuzzREST(f *testing.F) {
	f.Add(uint8(0), `{"content":"node 22 everywhere","kind":"decision","scope":"team","team":"platform","tags":["node"],"valid_until":"2027-01-01T00:00:00Z"}`)
	f.Add(uint8(0), `{"content":"x","corrects":"mem_1","related_to":["mem_1","mem_9"],"refs":["a.go"]}`)
	f.Add(uint8(0), `{"content":"raw notes","extract":true}`)
	f.Add(uint8(1), `{"query":"node","scopes":["team:platform","user"],"limit":50,"include_stale":false,"expand":1,"as_of":"2026-11-04T00:00:00+07:00"}`)
	f.Add(uint8(2), `{"verdict":"confirm","note":"checked the cron"}`)
	f.Add(uint8(3), `{"to":"mem_1","type":"supports"}`)
	f.Add(uint8(4), `{"reason":"wrong"}`)
	f.Add(uint8(1), `{"limit":1e400}`)
	f.Fuzz(func(t *testing.T, route uint8, body string) {
		h, ctx := fuzzServer(t)
		rt := fuzzRoutes[int(route)%len(fuzzRoutes)]
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequestWithContext(ctx, rt.method, rt.path, strings.NewReader(body)))
		if rec.Code >= 400 && !json.Valid(rec.Body.Bytes()) {
			t.Fatalf("%s %s %q: %d with a body that is not JSON: %s", rt.method, rt.path, body, rec.Code, rec.Body)
		}
		if rec.Code >= 500 && !extractUnavailable(rt.path, rec) {
			t.Fatalf("%s %s %q: %d %s", rt.method, rt.path, body, rec.Code, rec.Body)
		}
	})
}

// extractUnavailable reports the 503 a remember with extract gets when no extractor is configured, as in this fixture.
func extractUnavailable(path string, rec *httptest.ResponseRecorder) bool {
	var e struct {
		Error struct{ Code string } `json:"error"`
	}
	return path == "/v1/memories" && rec.Code == http.StatusServiceUnavailable &&
		json.Unmarshal(rec.Body.Bytes(), &e) == nil && e.Error.Code == "unavailable"
}
