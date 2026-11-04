// Package store keeps the registry of storage backends. A backend registers
// itself by name in an init function, and Open picks one by that name.
package store

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"sync"

	"github.com/raven-clown/logbook/memory"
)

// OpenFunc opens a backend from its data source name.
type OpenFunc func(ctx context.Context, dsn string) (memory.Store, error)

var (
	mu      sync.RWMutex
	drivers = map[string]OpenFunc{}
)

// Register makes a backend available under name. It panics if name is taken.
func Register(name string, open OpenFunc) {
	mu.Lock()
	defer mu.Unlock()
	if _, dup := drivers[name]; dup {
		panic(fmt.Sprintf("store: driver %q registered twice", name))
	}
	drivers[name] = open
}

// Open opens the backend registered as driver.
func Open(ctx context.Context, driver, dsn string) (memory.Store, error) {
	mu.RLock()
	open, ok := drivers[driver]
	mu.RUnlock()
	if !ok {
		return nil, memory.Errorf(memory.CodeInvalidArgument, "database.driver", "unknown database driver %q, have %v", driver, Drivers())
	}
	return open(ctx, dsn)
}

// Drivers returns the registered backend names, sorted.
func Drivers() []string {
	mu.RLock()
	defer mu.RUnlock()
	return slices.Sorted(maps.Keys(drivers))
}
