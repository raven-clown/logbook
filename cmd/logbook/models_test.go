package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/raven-clown/logbook/internal/testutil"
	"github.com/raven-clown/logbook/memory"
	"github.com/raven-clown/logbook/store/sqlite"
)

// fakeModels answers /v1/embeddings with bag-of-words vectors and
// /v1/chat/completions with fixed facts, and records the headers it saw.
type fakeModels struct {
	srv  *httptest.Server
	emb  *testutil.BagOfWords
	mu   sync.Mutex
	auth []string
	ids  []string
}

func newFakeModels(t *testing.T, facts []memory.Candidate) *fakeModels {
	t.Helper()
	f := &fakeModels{emb: &testutil.BagOfWords{Synonyms: map[string]string{"database": "postgres"}}}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.auth = append(f.auth, r.Header.Get("Authorization"))
		f.ids = append(f.ids, r.Header.Get("X-Request-ID"))
		f.mu.Unlock()
		switch r.URL.Path {
		case "/v1/embeddings":
			var req struct {
				Input []string `json:"input"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			vs, _ := f.emb.Embed(r.Context(), req.Input)
			data := make([]map[string]any, len(vs))
			for i, v := range vs {
				data[i] = map[string]any{"index": i, "embedding": v}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
		case "/v1/chat/completions":
			content, _ := json.Marshal(map[string]any{"facts": facts, "contradicts": false})
			_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": string(content)}}}})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeModels) env(model string) []string {
	base := f.srv.URL + "/v1"
	return []string{
		"LOGBOOK_EMBEDDER__TYPE=openai", "LOGBOOK_EMBEDDER__BASE_URL=" + base, "LOGBOOK_EMBEDDER__MODEL=" + model, "LOGBOOK_EMBEDDER__API_KEY=sk-fake",
		"LOGBOOK_EXTRACTOR__TYPE=openai", "LOGBOOK_EXTRACTOR__BASE_URL=" + base, "LOGBOOK_EXTRACTOR__MODEL=memory-extract", "LOGBOOK_EXTRACTOR__API_KEY=sk-fake",
	}
}

func TestServeWithFakeModels(t *testing.T) {
	models := newFakeModels(t, []memory.Candidate{
		{Content: "staging runs postgres 17", Kind: memory.KindFact, Evidence: "we moved staging to postgres 17"},
		{Content: "deploys freeze on fridays", Kind: memory.KindDecision, Evidence: "no deploys on fridays"},
	})
	c := newCLI(t)
	key := createKey(t, c.dsn())
	s := startServe(t, append(append(c.env, "LOGBOOK_DEFAULT_PROJECT=mothership"), models.env("bag")...))

	res, body := s.do(t, http.MethodPost, "/v1/memories", key, `{"content":"we moved staging to postgres 17, and no deploys on fridays","extract":true}`, "X-Request-ID", "req-7")
	if res.StatusCode != http.StatusCreated || strings.Count(body, `"id":"mem_`) != 2 || strings.Contains(body, "degraded") {
		t.Fatalf("remember extract %d %s", res.StatusCode, body)
	}
	res, body = s.do(t, http.MethodPost, "/v1/recall", key, `{"query":"which database"}`)
	if res.StatusCode != http.StatusOK || !strings.Contains(body, "staging runs postgres 17") || strings.Contains(body, "fridays") {
		t.Fatalf("recall %d %s", res.StatusCode, body)
	}
	if res, body := s.get(t, "/readyz", ""); res.StatusCode != http.StatusOK || !strings.Contains(body, `"embedder":"ok"`) || !strings.Contains(body, `"extractor":"configured"`) {
		t.Fatalf("readyz %d %s", res.StatusCode, body)
	}
	models.mu.Lock()
	defer models.mu.Unlock()
	if len(models.auth) < 3 {
		t.Fatalf("the fake saw %d calls", len(models.auth))
	}
	for i, a := range models.auth {
		if a != "Bearer sk-fake" {
			t.Fatalf("call %d sent Authorization %q, want the configured key and never the caller's", i, a)
		}
	}
	if models.ids[0] != "req-7" {
		t.Fatalf("X-Request-ID = %q", models.ids[0])
	}
}

func TestReindexCommand(t *testing.T) {
	models := newFakeModels(t, nil)
	c := newCLI(t)
	base := c.env
	c.env = append(base, models.env("model-a")...)
	c.ok("remember", "staging runs postgres 17")
	c.ok("remember", "lunch is at noon")
	c.env = append(base, models.env("model-b")...)
	out := c.ok("reindex")
	if !strings.Contains(out, "dropped 2 vectors, embedded 2 memories") {
		t.Fatalf("reindex printed %q", out)
	}
	st, err := sqlite.Open(context.Background(), c.dsn())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	for model, want := range map[string]int{"model-a": 0, "model-b": 2} {
		n := 0
		_ = st.Embeddings(context.Background(), memory.DefaultOrg, model, func(string, string, []float32) error { n++; return nil })
		if n != want {
			t.Fatalf("%s has %d vectors, want %d", model, n, want)
		}
	}
	if out := c.ok("reindex", "--all", "--json"); !strings.Contains(out, `"embedded": 2`) && !strings.Contains(out, `"embedded":2`) {
		t.Fatalf("reindex --all printed %q", out)
	}
}

func TestReindexWithoutEmbedder(t *testing.T) {
	c := newCLI(t)
	code, _, errOut := c.run("reindex")
	if code != 1 || !strings.Contains(errOut, "embedder.type") {
		t.Fatalf("reindex exited %d: %s", code, errOut)
	}
}

func TestUnknownModelType(t *testing.T) {
	for _, env := range [][]string{
		{"LOGBOOK_EMBEDDER__TYPE=cohere", "LOGBOOK_EMBEDDER__BASE_URL=http://x/v1", "LOGBOOK_EMBEDDER__MODEL=m"},
		{"LOGBOOK_EXTRACTOR__TYPE=cohere", "LOGBOOK_EXTRACTOR__BASE_URL=http://x/v1", "LOGBOOK_EXTRACTOR__MODEL=m"},
	} {
		c := newCLI(t)
		c.env = append(c.env, env...)
		code, _, errOut := c.run("list")
		if code != 1 || !strings.Contains(errOut, "cohere") || !strings.Contains(errOut, "openai") {
			t.Fatalf("unknown type exited %d: %s", code, errOut)
		}
	}
}
