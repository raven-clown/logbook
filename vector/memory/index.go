// Package memory is the default vector index: a brute-force cosine scan over
// an in-process copy of the stored vectors. It works with one process only.
package memory

import (
	"cmp"
	"context"
	"math"
	"slices"
	"sync"

	lbmem "github.com/raven-clown/logbook/memory"
	"github.com/raven-clown/logbook/vector"
)

func init() {
	vector.Register("memory", func(load vector.Loader) (vector.Index, error) { return New(load), nil })
}

type entry struct {
	scope string
	v     []float32
	norm  float64
}

// Index keeps vectors per organisation, loaded on the first search.
type Index struct {
	load vector.Loader
	mu   sync.RWMutex
	orgs map[string]map[string]entry
}

// New returns an index that reads an organisation's vectors through load the
// first time it is searched.
func New(load vector.Loader) *Index {
	return &Index{load: load, orgs: map[string]map[string]entry{}}
}

func newEntry(scope string, v []float32) entry {
	var n float64
	for _, x := range v {
		n += float64(x) * float64(x)
	}
	return entry{scope: scope, v: slices.Clone(v), norm: math.Sqrt(n)}
}

// Upsert adds or replaces a vector. Before an organisation is loaded it does
// nothing, since the load reads the vector from the store.
func (x *Index) Upsert(_ context.Context, orgID, scope, id string, v []float32) error {
	x.mu.Lock()
	defer x.mu.Unlock()
	if org, ok := x.orgs[orgID]; ok {
		org[id] = newEntry(scope, v)
	}
	return nil
}

// Delete removes a memory's vector.
func (x *Index) Delete(_ context.Context, orgID, id string) error {
	x.mu.Lock()
	defer x.mu.Unlock()
	delete(x.orgs[orgID], id)
	return nil
}

func (x *Index) ensure(ctx context.Context, orgID string) error {
	x.mu.RLock()
	_, ok := x.orgs[orgID]
	x.mu.RUnlock()
	if ok {
		return nil
	}
	x.mu.Lock()
	defer x.mu.Unlock()
	if _, ok := x.orgs[orgID]; ok {
		return nil
	}
	org := map[string]entry{}
	if x.load != nil {
		err := x.load(ctx, orgID, func(id, scope string, v []float32) error {
			org[id] = newEntry(scope, v)
			return nil
		})
		if err != nil {
			return err
		}
	}
	x.orgs[orgID] = org
	return nil
}

// Search returns the k nearest vectors in scopes. Vectors of another length
// than v are skipped.
func (x *Index) Search(ctx context.Context, orgID string, scopes []string, v []float32, k int) ([]lbmem.Hit, error) {
	if k <= 0 || len(scopes) == 0 {
		return nil, nil
	}
	if err := x.ensure(ctx, orgID); err != nil {
		return nil, err
	}
	q := newEntry("", v)
	if q.norm == 0 {
		return nil, nil
	}
	x.mu.RLock()
	var hits []lbmem.Hit
	for id, e := range x.orgs[orgID] {
		if len(e.v) != len(q.v) || e.norm == 0 || !slices.Contains(scopes, e.scope) {
			continue
		}
		var dot float64
		for i := range e.v {
			dot += float64(e.v[i]) * float64(q.v[i])
		}
		hits = append(hits, lbmem.Hit{ID: id, Score: dot / (e.norm * q.norm)})
	}
	x.mu.RUnlock()
	slices.SortFunc(hits, func(a, b lbmem.Hit) int {
		if c := cmp.Compare(b.Score, a.Score); c != 0 {
			return c
		}
		return cmp.Compare(a.ID, b.ID)
	})
	if len(hits) > k {
		hits = hits[:k]
	}
	return hits, nil
}
