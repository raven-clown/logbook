package memory_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/raven-clown/logbook/internal/testutil"
	"github.com/raven-clown/logbook/memory"
	"github.com/raven-clown/logbook/vector"
	vecmem "github.com/raven-clown/logbook/vector/memory"
)

type models struct {
	emb *testutil.BagOfWords
	ext *testutil.FakeExtractor
	idx *vecmem.Index
}

func newModelFixture(t *testing.T, m *models) *fixture {
	t.Helper()
	if m.emb == nil {
		m.emb = &testutil.BagOfWords{}
	}
	var f *fixture
	m.idx = vecmem.New(func(ctx context.Context, org string, fn func(id, scope string, v []float32) error) error {
		return memory.VectorLoader(f.store, m.emb.Model())(ctx, org, fn)
	})
	f = newFixture(t, func(o *memory.Options) {
		o.Embedder, o.Index = m.emb, m.idx
		if m.ext != nil {
			o.Extractor = m.ext
		}
		o.Budgets = memory.Budgets{Embed: 200 * time.Millisecond, Query: 200 * time.Millisecond, Contradiction: 200 * time.Millisecond}
	})
	return f
}

func (f *fixture) rememberFull(t *testing.T, p memory.Principal, r memory.RememberRequest) *memory.RememberResult {
	t.Helper()
	res, err := f.svc.Remember(background, p, r)
	if err != nil {
		t.Fatalf("Remember(%q): %v", r.Content, err)
	}
	return res
}

// vectorCount counts the stored vectors of the bag-of-words fake.
func (f *fixture) vectorCount(t *testing.T) int {
	t.Helper()
	n := 0
	err := memory.VectorLoader(f.store, "bag-of-words")(background, memory.DefaultOrg, func(string, string, []float32) error { n++; return nil })
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func (f *fixture) edges(t *testing.T, id string, typ memory.EdgeType) []string {
	t.Helper()
	es, err := f.store.Edges(background, memory.DefaultOrg, []string{id})
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range es {
		if e.Type == typ && e.From == id {
			out = append(out, e.To)
		}
	}
	slices.Sort(out)
	return out
}

func TestRecallFindsByMeaning(t *testing.T) {
	m := &models{emb: &testutil.BagOfWords{Synonyms: map[string]string{"database": "postgres"}}}
	f := newModelFixture(t, m)
	pg := f.remember(t, alice, memory.RememberRequest{Content: "staging runs postgres 17"})
	f.remember(t, alice, memory.RememberRequest{Content: "lunch is at noon"})
	if f.vectorCount(t) != 2 {
		t.Fatalf("vectors stored = %d", f.vectorCount(t))
	}
	hits, _ := f.store.SearchText(background, "which database", memory.Filter{OrgID: memory.DefaultOrg, Scopes: []string{"user:alice"}}, 10)
	if len(hits) != 0 {
		t.Fatal("full-text search already finds it, so the test proves nothing")
	}
	res, err := f.svc.Recall(background, alice, memory.RecallRequest{Query: "which database"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Results) != 1 || res.Results[0].ID != pg.ID || len(res.Degraded) != 0 {
		t.Fatalf("recall = %v, degraded %v", resultIDs(res.Results), res.Degraded)
	}
	if f.svc.EmbedderState() != "ok" {
		t.Fatalf("EmbedderState = %q", f.svc.EmbedderState())
	}
}

func TestRecallVectorRespectsFilters(t *testing.T) {
	m := &models{emb: &testutil.BagOfWords{Synonyms: map[string]string{"database": "postgres"}}}
	f := newModelFixture(t, m)
	f.remember(t, bob, memory.RememberRequest{Content: "bob's laptop runs postgres 16"})
	gone := f.remember(t, alice, memory.RememberRequest{Content: "the old box runs postgres 12"})
	if _, err := f.svc.Forget(background, alice, memory.ForgetRequest{ID: gone.ID}); err != nil {
		t.Fatal(err)
	}
	decision := f.remember(t, alice, memory.RememberRequest{Content: "we picked postgres over mysql", Kind: memory.KindDecision, Tags: []string{"arch"}})
	f.advance(48 * time.Hour)
	later := f.remember(t, alice, memory.RememberRequest{Content: "staging runs postgres 17", Tags: []string{"ops"}})
	for _, tt := range []struct {
		name string
		r    memory.RecallRequest
		want []string
	}{
		{"own scope only, forgotten hidden", memory.RecallRequest{Query: "database"}, []string{decision.ID, later.ID}},
		{"kind", memory.RecallRequest{Query: "database", Kinds: []memory.Kind{memory.KindDecision}}, []string{decision.ID}},
		{"tag", memory.RecallRequest{Query: "database", Tags: []string{"ops"}}, []string{later.ID}},
		{"as of", memory.RecallRequest{Query: "database", AsOf: day0.Add(time.Hour)}, []string{decision.ID}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got := resultIDs(f.recall(t, alice, tt.r))
			slices.Sort(got)
			want := slices.Clone(tt.want)
			slices.Sort(want)
			if !slices.Equal(got, want) {
				t.Fatalf("recall = %v, want %v", got, want)
			}
		})
	}
}

func TestRememberEmbedderDown(t *testing.T) {
	m := &models{}
	f := newModelFixture(t, m)
	if f.svc.EmbedderState() != "unknown" {
		t.Fatalf("state before any call = %q", f.svc.EmbedderState())
	}
	m.emb.SetErr(errors.New("connection refused"))
	res := f.rememberFull(t, alice, memory.RememberRequest{Content: "the build cache lives on s3"})
	if !slices.Equal(res.Degraded, []string{"embeddings"}) || f.svc.EmbedderState() != "failing" {
		t.Fatalf("degraded %v, state %q", res.Degraded, f.svc.EmbedderState())
	}
	if f.get(t, res.Memories[0].ID).Content == "" || f.vectorCount(t) != 0 {
		t.Fatal("memory not stored, or a vector was stored")
	}
	if n, err := f.svc.Backfill(background, memory.DefaultOrg, 10); err == nil || n != 0 {
		t.Fatalf("Backfill while down = %d, %v", n, err)
	}
	m.emb.SetErr(nil)
	if n, err := f.svc.Backfill(background, memory.DefaultOrg, 10); err != nil || n != 1 {
		t.Fatalf("Backfill = %d, %v", n, err)
	}
	if f.vectorCount(t) != 1 || f.svc.EmbedderState() != "ok" {
		t.Fatalf("after backfill: %d vectors, state %q", f.vectorCount(t), f.svc.EmbedderState())
	}
	got := f.recall(t, alice, memory.RecallRequest{Query: "s3 cache"})
	if len(got) != 1 {
		t.Fatalf("recall after backfill = %v", resultIDs(got))
	}
}

func TestRememberEmbedderSlow(t *testing.T) {
	m := &models{}
	f := newModelFixture(t, m)
	m.emb.SetBlock(true)
	res := f.rememberFull(t, alice, memory.RememberRequest{Content: "slow embedder, still stored"})
	if !slices.Equal(res.Degraded, []string{"embeddings"}) {
		t.Fatalf("degraded = %v", res.Degraded)
	}
}

func TestRecallEmbedderDown(t *testing.T) {
	m := &models{}
	f := newModelFixture(t, m)
	id := f.remember(t, alice, memory.RememberRequest{Content: "the build cache lives on s3"}).ID
	m.emb.SetErr(errors.New("boom"))
	res, err := f.svc.Recall(background, alice, memory.RecallRequest{Query: "build cache"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Results) != 1 || res.Results[0].ID != id || !slices.Equal(res.Degraded, []string{"embeddings"}) {
		t.Fatalf("recall = %v, degraded %v", resultIDs(res.Results), res.Degraded)
	}
}

func TestRelatedNeighbours(t *testing.T) {
	m := &models{}
	f := newModelFixture(t, m)
	base := "the nightly build runs on the big arm runner pool in region one"
	near := []string{
		base + " today",
		base + " again",
		base + " still",
		base + " now",
	}
	far := f.remember(t, alice, memory.RememberRequest{Content: "lunch is at noon on fridays"}).ID
	other := f.remember(t, bob, memory.RememberRequest{Content: base + " too"}).ID
	var ids []string
	for _, c := range near {
		ids = append(ids, f.remember(t, alice, memory.RememberRequest{Content: c}).ID)
	}
	sim := vector.Cosine(testutil.Vector(near[0], 256), testutil.Vector(base+" here", 256))
	if sim < 0.80 || sim > 0.99 {
		t.Fatalf("test texts have similarity %v, want 0.80 to 0.99", sim)
	}
	newest := f.remember(t, alice, memory.RememberRequest{Content: base + " here"}).ID
	got := f.edges(t, newest, memory.EdgeRelated)
	if len(got) != 3 {
		t.Fatalf("related = %v, want three of %v", got, ids)
	}
	for _, id := range got {
		if id == far || id == other || !slices.Contains(ids, id) {
			t.Fatalf("related to %s, which is far or in another scope", id)
		}
	}
}

func TestContradictionCheck(t *testing.T) {
	ext := &testutil.FakeExtractor{Contradict: func(_, _ string) bool { return true }}
	m := &models{ext: ext}
	f := newModelFixture(t, m)
	old := f.remember(t, alice, memory.RememberRequest{Content: "the staging database runs postgres version 16 on the shared cluster"})
	sim := vector.Cosine(testutil.Vector("the staging database runs postgres version 16 on the shared cluster", 256),
		testutil.Vector("the staging database runs postgres version 17 on the shared cluster", 256))
	if sim <= 0.85 {
		t.Fatalf("test texts have similarity %v, want over 0.85", sim)
	}
	res := f.rememberFull(t, alice, memory.RememberRequest{Content: "the staging database runs postgres version 17 on the shared cluster"})
	got := res.Memories[0]
	if !slices.Equal(got.Conflicts, []string{old.ID}) || f.get(t, old.ID).Status != memory.StatusDisputed {
		t.Fatalf("conflicts = %v, old status %s", got.Conflicts, f.get(t, old.ID).Status)
	}
	if !slices.Equal(f.edges(t, got.ID, memory.EdgeContradicts), []string{old.ID}) || len(f.edges(t, got.ID, memory.EdgeRelated)) != 0 {
		t.Fatalf("edges: contradicts %v, related %v", f.edges(t, got.ID, memory.EdgeContradicts), f.edges(t, got.ID, memory.EdgeRelated))
	}
	if asked := ext.Asked(); len(asked) != 1 || asked[0][0] != "the staging database runs postgres version 16 on the shared cluster" {
		t.Fatalf("asked = %v", asked)
	}
}

func TestContradictionCheckSkips(t *testing.T) {
	t.Run("a subject uses the subject rule", func(t *testing.T) {
		ext := &testutil.FakeExtractor{Contradict: func(_, _ string) bool { return true }}
		f := newModelFixture(t, &models{ext: ext})
		f.remember(t, alice, memory.RememberRequest{Content: "the staging database runs postgres version 16 on the shared cluster"})
		f.remember(t, alice, memory.RememberRequest{Content: "the staging database runs postgres version 17 on the shared cluster", Subject: "staging.db"})
		if len(ext.Asked()) != 0 {
			t.Fatalf("asked = %v", ext.Asked())
		}
	})
	t.Run("a slow extractor is skipped", func(t *testing.T) {
		ext := &testutil.FakeExtractor{Block: true}
		f := newModelFixture(t, &models{ext: ext})
		old := f.remember(t, alice, memory.RememberRequest{Content: "the staging database runs postgres version 16 on the shared cluster"})
		res := f.rememberFull(t, alice, memory.RememberRequest{Content: "the staging database runs postgres version 17 on the shared cluster"})
		if !slices.Equal(res.Degraded, []string{"contradiction_check"}) || f.get(t, old.ID).Status != memory.StatusUnverified {
			t.Fatalf("degraded %v, old %s", res.Degraded, f.get(t, old.ID).Status)
		}
	})
	t.Run("a correction is not checked against what it corrects", func(t *testing.T) {
		ext := &testutil.FakeExtractor{Contradict: func(_, _ string) bool { return true }}
		f := newModelFixture(t, &models{ext: ext})
		old := f.remember(t, alice, memory.RememberRequest{Content: "the staging database runs postgres version 16 on the shared cluster"})
		res := f.rememberFull(t, alice, memory.RememberRequest{Content: "the staging database runs postgres version 17 on the shared cluster", Corrects: old.ID})
		if len(res.Memories[0].Conflicts) != 0 || f.get(t, old.ID).Status != memory.StatusSuperseded {
			t.Fatalf("result = %+v", res.Memories[0])
		}
	})
}

func TestForgetDropsVector(t *testing.T) {
	f := newModelFixture(t, &models{})
	id := f.remember(t, alice, memory.RememberRequest{Content: "the build cache lives on s3"}).ID
	if got := f.recall(t, alice, memory.RecallRequest{Query: "s3"}); len(got) != 1 {
		t.Fatalf("recall = %v", resultIDs(got))
	}
	if _, err := f.svc.Forget(background, alice, memory.ForgetRequest{ID: id}); err != nil {
		t.Fatal(err)
	}
	if f.vectorCount(t) != 0 {
		t.Fatal("vector kept after forget")
	}
	hits, err := f.svc.Recall(background, alice, memory.RecallRequest{Query: "build cache s3"})
	if err != nil || len(hits.Results) != 0 {
		t.Fatalf("forgotten memory recalled: %v, %v", hits, err)
	}
}

func TestDuplicateSkipsEmbedding(t *testing.T) {
	m := &models{}
	f := newModelFixture(t, m)
	f.remember(t, alice, memory.RememberRequest{Content: "the build cache lives on s3"})
	calls := m.emb.Calls()
	if got := f.remember(t, alice, memory.RememberRequest{Content: "The build cache lives on S3"}); !got.Duplicate {
		t.Fatalf("not a duplicate: %+v", got)
	}
	if m.emb.Calls() != calls {
		t.Fatal("a duplicate was embedded again")
	}
}

func TestReindex(t *testing.T) {
	f := newModelFixture(t, &models{})
	f.remember(t, alice, memory.RememberRequest{Content: "one"})
	f.remember(t, alice, memory.RememberRequest{Content: "two"})
	es := f.store.(memory.EmbeddingStore)
	if err := es.PutEmbedding(background, memory.DefaultOrg, "mem_1", "old-model", []float32{1}); err != nil {
		t.Fatal(err)
	}
	dropped, embedded, err := f.svc.Reindex(background, memory.DefaultOrg, false, 1)
	if err != nil || dropped != 1 || embedded != 0 {
		t.Fatalf("Reindex = %d, %d, %v", dropped, embedded, err)
	}
	dropped, embedded, err = f.svc.Reindex(background, memory.DefaultOrg, true, 1)
	if err != nil || dropped != 2 || embedded != 2 || f.vectorCount(t) != 2 {
		t.Fatalf("Reindex all = %d, %d, %v", dropped, embedded, err)
	}
}

func TestNoModelsNothingChanges(t *testing.T) {
	f := newFixture(t)
	if f.svc.EmbedderState() != "not configured" || f.svc.ExtractorState() != "not configured" {
		t.Fatalf("state = %q, %q", f.svc.EmbedderState(), f.svc.ExtractorState())
	}
	if _, _, err := f.svc.Reindex(background, memory.DefaultOrg, false, 10); !errors.Is(err, memory.ErrNoEmbedder) {
		t.Fatalf("Reindex without an embedder = %v", err)
	}
	if n, err := f.svc.Backfill(background, memory.DefaultOrg, 10); n != 0 || err != nil {
		t.Fatalf("Backfill = %d, %v", n, err)
	}
	res := f.rememberFull(t, alice, memory.RememberRequest{Content: "plain"})
	if len(res.Degraded) != 0 {
		t.Fatalf("degraded = %v", res.Degraded)
	}
}
