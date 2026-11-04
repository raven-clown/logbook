package sqlite_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/raven-clown/logbook/memory"
	"github.com/raven-clown/logbook/store"
	"github.com/raven-clown/logbook/store/sqlite"
	"github.com/raven-clown/logbook/store/storetest"
)

func openTemp(t *testing.T) memory.Store {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "db")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	s, err := sqlite.Open(context.Background(), filepath.Join(dir, "logbook.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestConformance(t *testing.T) {
	storetest.Run(t, openTemp)
}

func TestOpenWindowsStylePath(t *testing.T) {
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "my notes")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "logbook.db")
	s, err := sqlite.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	m := &memory.Memory{ID: "mem_1", OrgID: memory.DefaultOrg, Scope: "user:alice", Content: "survives a restart", Kind: memory.KindFact, Status: memory.StatusUnverified, Source: memory.SourceDirect, ContentHash: "h", Version: 1}
	if err := s.Insert(ctx, m); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = sqlite.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	got, err := s.Get(ctx, memory.DefaultOrg, "mem_1")
	if err != nil || got.Content != "survives a restart" {
		t.Fatalf("after reopen: %v, %v", got, err)
	}
}

func TestRegistered(t *testing.T) {
	path := filepath.Join(t.TempDir(), "logbook.db")
	s, err := store.Open(context.Background(), "sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	if _, err := s.Get(context.Background(), memory.DefaultOrg, "mem_x"); err == nil {
		t.Fatal("empty store returned a memory")
	}
}

func TestOpenMemory(t *testing.T) {
	s, err := sqlite.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
}

func TestOpenBadPath(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := sqlite.Open(context.Background(), filepath.Join(blocker, "logbook.db")); err == nil {
		t.Fatal("opening under a regular file should fail")
	}
}
