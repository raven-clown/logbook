package extract_test

import (
	"context"
	"slices"
	"testing"

	"github.com/raven-clown/logbook/extract"
	"github.com/raven-clown/logbook/memory"
)

type fake struct{}

func (fake) Extract(context.Context, string) ([]memory.Candidate, error) { return nil, nil }
func (fake) Contradicts(context.Context, string, string) (bool, error)   { return false, nil }

func TestRegistry(t *testing.T) {
	extract.Register("fake-extract", func(extract.Config) (extract.Extractor, error) { return fake{}, nil })
	if _, err := extract.Open("fake-extract", extract.Config{}); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(extract.Names(), "fake-extract") {
		t.Fatalf("Names = %v", extract.Names())
	}
	_, err := extract.Open("nope", extract.Config{})
	if memory.CodeOf(err) != memory.CodeInvalidArgument {
		t.Fatalf("unknown = %v", err)
	}
	defer func() {
		if recover() == nil {
			t.Fatal("registering a name twice did not panic")
		}
	}()
	extract.Register("fake-extract", func(extract.Config) (extract.Extractor, error) { return fake{}, nil })
}
