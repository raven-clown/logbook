// Package vector holds the vector index interface and the registry of index
// backends. A backend registers itself by name in an init function.
package vector

import (
	"context"
	"fmt"
	"maps"
	"math"
	"slices"
	"sync"

	"github.com/raven-clown/logbook/memory"
)

// Index finds the memories whose vectors are nearest to a query vector.
type Index interface {
	Upsert(ctx context.Context, orgID, scope, id string, v []float32) error
	Delete(ctx context.Context, orgID, id string) error
	// Search returns the k nearest vectors in scopes, best first. Hit.Score is
	// the cosine similarity.
	Search(ctx context.Context, orgID string, scopes []string, v []float32, k int) ([]memory.Hit, error)
}

// Loader calls fn for every stored vector of an organisation, for indexes that
// keep a copy in memory.
type Loader func(ctx context.Context, orgID string, fn func(id, scope string, v []float32) error) error

// Factory makes an index. Backends that keep their own data ignore load.
type Factory func(load Loader) (Index, error)

var (
	mu        sync.RWMutex
	factories = map[string]Factory{}
)

// Register makes a backend available under name. It panics if name is taken.
func Register(name string, f Factory) {
	mu.Lock()
	defer mu.Unlock()
	if _, dup := factories[name]; dup {
		panic(fmt.Sprintf("vector: index %q registered twice", name))
	}
	factories[name] = f
}

// Open makes the index registered as name.
func Open(name string, load Loader) (Index, error) {
	mu.RLock()
	f, ok := factories[name]
	mu.RUnlock()
	if !ok {
		return nil, memory.Errorf(memory.CodeInvalidArgument, "vector", "unknown vector index %q, have %v", name, Names())
	}
	return f(load)
}

// Names returns the registered backend names, sorted.
func Names() []string {
	mu.RLock()
	defer mu.RUnlock()
	return slices.Sorted(maps.Keys(factories))
}

// Cosine returns the cosine similarity of a and b, or 0 when either is zero or
// their lengths differ.
func Cosine(a, b []float32) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		x, y := float64(a[i]), float64(b[i])
		dot += x * y
		na += x * x
		nb += y * y
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}
