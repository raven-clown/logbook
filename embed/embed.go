// Package embed holds the embedder interface and the registry of embedding
// providers. A provider registers itself by name in an init function.
package embed

import (
	"context"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"sync"

	"github.com/raven-clown/logbook/memory"
)

// Embedder turns texts into vectors.
type Embedder interface {
	// Embed returns one vector per text, in the same order.
	Embed(ctx context.Context, texts []string) ([][]float32, error)
	// Model names the vectors it makes. Vectors with different model names are
	// never compared.
	Model() string
}

// Config configures a provider.
type Config struct {
	BaseURL string
	Model   string
	APIKey  string
	// Dimensions asks for shorter vectors. Zero keeps the model's size.
	Dimensions int
	// ForwardToken sends the caller's OIDC token instead of APIKey.
	ForwardToken bool
	HTTPClient   *http.Client
}

// Factory makes an embedder from its config.
type Factory func(Config) (Embedder, error)

var (
	mu        sync.RWMutex
	factories = map[string]Factory{}
)

// Register makes a provider available under name. It panics if name is taken.
func Register(name string, f Factory) {
	mu.Lock()
	defer mu.Unlock()
	if _, dup := factories[name]; dup {
		panic(fmt.Sprintf("embed: provider %q registered twice", name))
	}
	factories[name] = f
}

// Open makes the embedder registered as name.
func Open(name string, c Config) (Embedder, error) {
	mu.RLock()
	f, ok := factories[name]
	mu.RUnlock()
	if !ok {
		return nil, memory.Errorf(memory.CodeInvalidArgument, "embedder.type", "unknown embedder type %q, have %v", name, Names())
	}
	return f(c)
}

// Names returns the registered provider names, sorted.
func Names() []string {
	mu.RLock()
	defer mu.RUnlock()
	return slices.Sorted(maps.Keys(factories))
}
