package embed_test

import (
	"context"
	"slices"
	"testing"

	"github.com/raven-clown/logbook/embed"
	"github.com/raven-clown/logbook/memory"
)

type fake struct{ model string }

func (f fake) Embed(context.Context, []string) ([][]float32, error) { return nil, nil }
func (f fake) Model() string                                        { return f.model }

func TestRegistry(t *testing.T) {
	embed.Register("fake-embed", func(c embed.Config) (embed.Embedder, error) { return fake{model: c.Model}, nil })
	e, err := embed.Open("fake-embed", embed.Config{Model: "m1"})
	if err != nil || e.Model() != "m1" {
		t.Fatalf("Open = %v, %v", e, err)
	}
	if !slices.Contains(embed.Names(), "fake-embed") {
		t.Fatalf("Names = %v", embed.Names())
	}
	_, err = embed.Open("nope", embed.Config{})
	if memory.CodeOf(err) != memory.CodeInvalidArgument {
		t.Fatalf("unknown = %v", err)
	}
	defer func() {
		if recover() == nil {
			t.Fatal("registering a name twice did not panic")
		}
	}()
	embed.Register("fake-embed", func(embed.Config) (embed.Embedder, error) { return fake{}, nil })
}
