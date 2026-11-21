package storetest

import (
	"context"
	"errors"
	"math"
	"slices"
	"testing"
	"time"

	"github.com/raven-clown/logbook/memory"
)

// RunEmbeddings runs the conformance checks for backends that store vectors.
func RunEmbeddings(t *testing.T, open func(t *testing.T) memory.Store) {
	checks := []struct {
		name string
		fn   func(t *testing.T, s memory.Store, e memory.EmbeddingStore)
	}{
		{"RoundTrip", embeddingRoundTrip},
		{"SkipsForgotten", embeddingsSkipForgotten},
		{"Missing", embeddingsMissing},
		{"Drop", embeddingsDrop},
		{"OrgIsolation", embeddingsOrgIsolation},
		{"InTx", embeddingsInTx},
	}
	for _, c := range checks {
		t.Run(c.name, func(t *testing.T) {
			s := open(t)
			e, ok := s.(memory.EmbeddingStore)
			if !ok {
				t.Fatal("the store does not implement memory.EmbeddingStore")
			}
			c.fn(t, s, e)
		})
	}
}

var errRollback = errors.New("roll back")

type stored struct {
	scope string
	v     []float32
}

func vectors(t *testing.T, e memory.EmbeddingStore, org, model string) map[string]stored {
	t.Helper()
	out := map[string]stored{}
	err := e.Embeddings(context.Background(), org, model, func(id, scope string, v []float32) error {
		out[id] = stored{scope: scope, v: v}
		return nil
	})
	if err != nil {
		t.Fatalf("Embeddings: %v", err)
	}
	return out
}

func put(t *testing.T, e memory.EmbeddingStore, id, model string, v []float32) {
	t.Helper()
	if err := e.PutEmbedding(context.Background(), memory.DefaultOrg, id, model, v); err != nil {
		t.Fatalf("PutEmbedding(%s): %v", id, err)
	}
}

func embeddingRoundTrip(t *testing.T, s memory.Store, e memory.EmbeddingStore) {
	insert(t, s, newMemory("mem_a", "team:platform", "a"), newMemory("mem_b", "user:alice", "b"))
	odd := []float32{0, -1.5, math.SmallestNonzeroFloat32, math.MaxFloat32, float32(math.Inf(1)), 0.1}
	put(t, e, "mem_a", "m1", odd)
	put(t, e, "mem_b", "m1", []float32{1, 2})
	put(t, e, "mem_b", "m1", []float32{3, 4, 5})
	got := vectors(t, e, memory.DefaultOrg, "m1")
	if len(got) != 2 || got["mem_a"].scope != "team:platform" || !bitsEqual(got["mem_a"].v, odd) {
		t.Fatalf("vectors = %+v", got)
	}
	if !slices.Equal(got["mem_b"].v, []float32{3, 4, 5}) {
		t.Fatalf("replace kept %v", got["mem_b"].v)
	}
	if err := e.DeleteEmbeddings(context.Background(), memory.DefaultOrg, "mem_a"); err != nil {
		t.Fatal(err)
	}
	if got := vectors(t, e, memory.DefaultOrg, "m1"); len(got) != 1 {
		t.Fatalf("after delete = %+v", got)
	}
	if got := vectors(t, e, memory.DefaultOrg, "m2"); len(got) != 0 {
		t.Fatalf("another model = %+v", got)
	}
}

func bitsEqual(a, b []float32) bool {
	return slices.EqualFunc(a, b, func(x, y float32) bool { return math.Float32bits(x) == math.Float32bits(y) })
}

func embeddingsSkipForgotten(t *testing.T, s memory.Store, e memory.EmbeddingStore) {
	gone := newMemory("mem_gone", "user:alice", "gone")
	gone.Status, gone.ForgottenAt = memory.StatusForgotten, base
	old := newMemory("mem_old", "user:alice", "old")
	old.Status = memory.StatusSuperseded
	insert(t, s, gone, old)
	put(t, e, "mem_gone", "m1", []float32{1})
	put(t, e, "mem_old", "m1", []float32{1})
	if got := vectors(t, e, memory.DefaultOrg, "m1"); len(got) != 1 || got["mem_old"].v == nil {
		t.Fatalf("vectors = %+v, want only the superseded one", got)
	}
	missing, err := e.MissingEmbeddings(context.Background(), memory.DefaultOrg, "m2", 10)
	if err != nil || !slices.Equal(ids(missing), []string{"mem_old"}) {
		t.Fatalf("MissingEmbeddings = %v, %v", ids(missing), err)
	}
}

func embeddingsMissing(t *testing.T, s memory.Store, e memory.EmbeddingStore) {
	for i, id := range []string{"mem_3", "mem_1", "mem_2"} {
		m := newMemory(id, "user:alice", id)
		m.CreatedAt = base.Add(time.Duration(3-i) * time.Minute)
		insert(t, s, m)
	}
	put(t, e, "mem_2", "m1", []float32{1})
	put(t, e, "mem_1", "m0", []float32{1})
	got, err := e.MissingEmbeddings(context.Background(), memory.DefaultOrg, "m1", 10)
	if err != nil || !slices.Equal(ids(got), []string{"mem_1", "mem_3"}) {
		t.Fatalf("MissingEmbeddings = %v, %v, want oldest first", ids(got), err)
	}
	got, _ = e.MissingEmbeddings(context.Background(), memory.DefaultOrg, "m1", 1)
	if !slices.Equal(ids(got), []string{"mem_1"}) {
		t.Fatalf("limit 1 = %v", ids(got))
	}
}

func embeddingsDrop(t *testing.T, s memory.Store, e memory.EmbeddingStore) {
	insert(t, s, newMemory("mem_a", "user:alice", "a"), newMemory("mem_b", "user:alice", "b"))
	put(t, e, "mem_a", "old", []float32{1})
	put(t, e, "mem_b", "old", []float32{1})
	put(t, e, "mem_a", "new", []float32{1})
	n, err := e.DropEmbeddings(context.Background(), memory.DefaultOrg, "new")
	if err != nil || n != 2 {
		t.Fatalf("DropEmbeddings(keep new) = %d, %v", n, err)
	}
	if len(vectors(t, e, memory.DefaultOrg, "new")) != 1 || len(vectors(t, e, memory.DefaultOrg, "old")) != 0 {
		t.Fatal("drop removed the wrong vectors")
	}
	if n, err := e.DropEmbeddings(context.Background(), memory.DefaultOrg, ""); err != nil || n != 1 {
		t.Fatalf("DropEmbeddings(all) = %d, %v", n, err)
	}
}

func embeddingsOrgIsolation(t *testing.T, s memory.Store, e memory.EmbeddingStore) {
	other := newMemory("mem_x", "user:alice", "x")
	other.OrgID = "acme"
	insert(t, s, newMemory("mem_a", "user:alice", "a"), other)
	put(t, e, "mem_a", "m1", []float32{1})
	if err := e.PutEmbedding(context.Background(), "acme", "mem_x", "m1", []float32{2}); err != nil {
		t.Fatal(err)
	}
	if got := vectors(t, e, "acme", "m1"); len(got) != 1 || got["mem_x"].v[0] != 2 {
		t.Fatalf("acme vectors = %+v", got)
	}
	if n, _ := e.DropEmbeddings(context.Background(), "acme", ""); n != 1 {
		t.Fatalf("drop in acme removed %d", n)
	}
	if got := vectors(t, e, memory.DefaultOrg, "m1"); len(got) != 1 {
		t.Fatalf("default org lost its vector: %+v", got)
	}
	missing, _ := e.MissingEmbeddings(context.Background(), "acme", "m1", 10)
	if !slices.Equal(ids(missing), []string{"mem_x"}) {
		t.Fatalf("acme missing = %v", ids(missing))
	}
}

func embeddingsInTx(t *testing.T, s memory.Store, e memory.EmbeddingStore) {
	insert(t, s, newMemory("mem_a", "user:alice", "a"))
	err := s.Tx(context.Background(), func(tx memory.Store) error {
		te, ok := tx.(memory.EmbeddingStore)
		if !ok {
			t.Fatal("the transaction store does not implement memory.EmbeddingStore")
		}
		if err := te.PutEmbedding(context.Background(), memory.DefaultOrg, "mem_a", "m1", []float32{1}); err != nil {
			return err
		}
		return errRollback
	})
	if !errors.Is(err, errRollback) {
		t.Fatalf("Tx = %v", err)
	}
	if got := vectors(t, e, memory.DefaultOrg, "m1"); len(got) != 0 {
		t.Fatalf("rolled back vector kept: %+v", got)
	}
}
