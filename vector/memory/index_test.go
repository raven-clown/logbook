package memory_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"

	"github.com/raven-clown/logbook/memory"
	"github.com/raven-clown/logbook/vector"
	vecmem "github.com/raven-clown/logbook/vector/memory"
)

type row struct {
	id, scope string
	v         []float32
}

type source struct {
	mu    sync.Mutex
	rows  map[string][]row
	calls int
	err   error
}

func (s *source) load(_ context.Context, org string, fn func(id, scope string, v []float32) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	if s.err != nil {
		return s.err
	}
	for _, r := range s.rows[org] {
		if err := fn(r.id, r.scope, r.v); err != nil {
			return err
		}
	}
	return nil
}

func hitIDs(hs []memory.Hit) []string {
	out := make([]string, len(hs))
	for i, h := range hs {
		out[i] = h.ID
	}
	return out
}

var ctx = context.Background()

func TestSearch(t *testing.T) {
	src := &source{rows: map[string][]row{"default": {
		{"mem_a", "team:platform", []float32{1, 0}},
		{"mem_b", "team:platform", []float32{0.8, 0.6}},
		{"mem_c", "user:alice", []float32{0.9, 0.1}},
		{"mem_d", "user:bob", []float32{1, 0}},
		{"mem_e", "team:platform", []float32{1, 0}},
		{"mem_f", "team:platform", []float32{0, 1, 0}},
	}}}
	idx := vecmem.New(src.load)
	got, err := idx.Search(ctx, "default", []string{"team:platform", "user:alice"}, []float32{1, 0}, 3)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(hitIDs(got), []string{"mem_a", "mem_e", "mem_c"}) {
		t.Fatalf("Search = %v", hitIDs(got))
	}
	if got[0].Score < 0.999 || got[2].Score >= got[0].Score {
		t.Fatalf("scores = %+v", got)
	}
	got, _ = idx.Search(ctx, "default", []string{"team:platform"}, []float32{1, 0}, 10)
	if !slices.Equal(hitIDs(got), []string{"mem_a", "mem_e", "mem_b"}) {
		t.Fatalf("a vector of another length came back, or order is wrong: %v", hitIDs(got))
	}
	if got, _ := idx.Search(ctx, "default", nil, []float32{1, 0}, 10); len(got) != 0 {
		t.Fatalf("no scopes = %v", hitIDs(got))
	}
	if got, _ := idx.Search(ctx, "default", []string{"team:platform"}, []float32{1, 0}, 0); len(got) != 0 {
		t.Fatalf("k 0 = %v", hitIDs(got))
	}
	if got, _ := idx.Search(ctx, "acme", []string{"team:platform"}, []float32{1, 0}, 10); len(got) != 0 {
		t.Fatalf("other org = %v", hitIDs(got))
	}
}

func TestLazyLoad(t *testing.T) {
	src := &source{rows: map[string][]row{"default": {{"mem_a", "user:alice", []float32{1, 0}}}}}
	idx := vecmem.New(src.load)
	if err := idx.Upsert(ctx, "default", "user:alice", "mem_a", []float32{1, 0}); err != nil {
		t.Fatal(err)
	}
	if err := idx.Upsert(ctx, "default", "user:alice", "mem_b", []float32{0, 1}); err != nil {
		t.Fatal(err)
	}
	if src.calls != 0 {
		t.Fatalf("loaded before the first search")
	}
	got, _ := idx.Search(ctx, "default", []string{"user:alice"}, []float32{1, 0}, 10)
	if !slices.Equal(hitIDs(got), []string{"mem_a"}) {
		t.Fatalf("an upsert before the load was kept apart from the store: %v", hitIDs(got))
	}
	if err := idx.Upsert(ctx, "default", "user:alice", "mem_c", []float32{1, 0.1}); err != nil {
		t.Fatal(err)
	}
	if err := idx.Delete(ctx, "default", "mem_a"); err != nil {
		t.Fatal(err)
	}
	got, _ = idx.Search(ctx, "default", []string{"user:alice"}, []float32{1, 0}, 10)
	if !slices.Equal(hitIDs(got), []string{"mem_c"}) || src.calls != 1 {
		t.Fatalf("after upsert and delete = %v, %d loads", hitIDs(got), src.calls)
	}
}

func TestLoadRetriesAfterError(t *testing.T) {
	src := &source{err: errors.New("disk on fire"), rows: map[string][]row{"default": {{"mem_a", "user:alice", []float32{1}}}}}
	idx := vecmem.New(src.load)
	if _, err := idx.Search(ctx, "default", []string{"user:alice"}, []float32{1}, 1); err == nil {
		t.Fatal("load error was swallowed")
	}
	src.err = nil
	got, err := idx.Search(ctx, "default", []string{"user:alice"}, []float32{1}, 1)
	if err != nil || len(got) != 1 || src.calls != 2 {
		t.Fatalf("retry = %v, %v, %d loads", hitIDs(got), err, src.calls)
	}
}

func TestRegisteredAsMemory(t *testing.T) {
	src := &source{}
	idx, err := vector.Open("memory", src.load)
	if err != nil || idx == nil {
		t.Fatalf("Open(memory) = %v, %v", idx, err)
	}
}

func TestConcurrent(t *testing.T) {
	src := &source{rows: map[string][]row{}}
	idx := vecmem.New(src.load)
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Go(func() {
			for j := range 50 {
				id := fmt.Sprintf("mem_%d_%d", i, j)
				_ = idx.Upsert(ctx, "default", "user:alice", id, []float32{float32(i), float32(j)})
				if _, err := idx.Search(ctx, "default", []string{"user:alice"}, []float32{1, 1}, 5); err != nil {
					t.Error(err)
				}
				_ = idx.Delete(ctx, "default", id)
			}
		})
	}
	wg.Wait()
}
