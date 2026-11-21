// Package extract holds the fact extractor interface and the registry of
// extraction providers. A provider registers itself by name in an init function.
package extract

import (
	"context"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"sync"

	"github.com/raven-clown/logbook/memory"
)

// MaxFacts is the most facts kept from one extraction.
const MaxFacts = 10

// Extractor pulls facts out of raw text and checks two memories for a contradiction.
type Extractor interface {
	Extract(ctx context.Context, text string) ([]memory.Candidate, error)
	Contradicts(ctx context.Context, a, b string) (bool, error)
}

// Config configures a provider.
type Config struct {
	BaseURL      string
	Model        string
	APIKey       string
	ForwardToken bool
	HTTPClient   *http.Client
}

// Factory makes an extractor from its config.
type Factory func(Config) (Extractor, error)

var (
	mu        sync.RWMutex
	factories = map[string]Factory{}
)

// Register makes a provider available under name. It panics if name is taken.
func Register(name string, f Factory) {
	mu.Lock()
	defer mu.Unlock()
	if _, dup := factories[name]; dup {
		panic(fmt.Sprintf("extract: provider %q registered twice", name))
	}
	factories[name] = f
}

// Open makes the extractor registered as name.
func Open(name string, c Config) (Extractor, error) {
	mu.RLock()
	f, ok := factories[name]
	mu.RUnlock()
	if !ok {
		return nil, memory.Errorf(memory.CodeInvalidArgument, "extractor.type", "unknown extractor type %q, have %v", name, Names())
	}
	return f(c)
}

// Names returns the registered provider names, sorted.
func Names() []string {
	mu.RLock()
	defer mu.RUnlock()
	return slices.Sorted(maps.Keys(factories))
}
