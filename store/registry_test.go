package store_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/raven-clown/logbook/memory"
	"github.com/raven-clown/logbook/store"
)

type nopStore struct{ memory.Store }

func TestRegistry(t *testing.T) {
	var gotDSN string
	store.Register("fake-registry", func(_ context.Context, dsn string) (memory.Store, error) {
		gotDSN = dsn
		return nopStore{}, nil
	})

	if _, err := store.Open(context.Background(), "fake-registry", "x"); err != nil {
		t.Fatal(err)
	}
	if gotDSN != "x" {
		t.Fatalf("dsn = %q, want x", gotDSN)
	}

	_, err := store.Open(context.Background(), "nope", "")
	var e *memory.Error
	if !errors.As(err, &e) || e.Code != memory.CodeInvalidArgument || e.Field != "database.driver" {
		t.Fatalf("unknown driver error = %v", err)
	}

	names := store.Drivers()
	if !slices.IsSorted(names) || !slices.Contains(names, "fake-registry") {
		t.Fatalf("Drivers() = %v", names)
	}

	defer func() {
		if recover() == nil {
			t.Fatal("registering a name twice should panic")
		}
	}()
	store.Register("fake-registry", nil)
}
