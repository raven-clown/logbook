package sqlite_test

import (
	"context"
	"database/sql"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/raven-clown/logbook/admin"
	"github.com/raven-clown/logbook/memory"
	"github.com/raven-clown/logbook/store"
	"github.com/raven-clown/logbook/store/migrate"
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

func TestEmbeddingConformance(t *testing.T) {
	storetest.RunEmbeddings(t, openTemp)
}

func TestUpgradeFromV011(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "logbook.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	old := os.DirFS("migrations")
	v011 := fstest.MapFS{}
	for _, name := range []string{"0001_init.sql", "0002_access.sql"} {
		b, err := fs.ReadFile(old, name)
		if err != nil {
			t.Fatal(err)
		}
		v011[name] = &fstest.MapFile{Data: b}
	}
	if _, err := migrate.Run(ctx, db, v011); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO memories (org_id, id, scope, content, kind, status, source, created_at, updated_at, content_hash, version)
		VALUES ('default', 'mem_1', 'user:alice', 'from v0.1.1', 'fact', 'unverified', 'direct', '2026-11-05T00:00:00.000000000Z', '2026-11-05T00:00:00.000000000Z', 'h', 1)`); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	s, err := sqlite.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	missing, err := s.MissingEmbeddings(ctx, memory.DefaultOrg, "m1", 10)
	if err != nil || len(missing) != 1 || missing[0].Content != "from v0.1.1" {
		t.Fatalf("after upgrade: %v, %v", missing, err)
	}
}

func TestAdminConformance(t *testing.T) {
	storetest.RunAdmin(t, func(t *testing.T) admin.Store {
		s, ok := openTemp(t).(admin.Store)
		if !ok {
			t.Fatal("the sqlite store is not an admin.Store")
		}
		return s
	})
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

func TestConcurrentFirstOpen(t *testing.T) {
	for round := range 10 {
		path := filepath.Join(t.TempDir(), "logbook.db")
		errs := make(chan error, 4)
		var wg sync.WaitGroup
		for range 4 {
			wg.Go(func() {
				s, err := sqlite.Open(context.Background(), path)
				if err == nil {
					err = s.Close()
				}
				errs <- err
			})
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			if err != nil {
				t.Fatalf("round %d: %v", round, err)
			}
		}
	}
}

func TestTxRollsBackOnPanic(t *testing.T) {
	s := openTemp(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	func() {
		defer func() { _ = recover() }()
		_ = s.Tx(ctx, func(tx memory.Store) error {
			if err := tx.Insert(ctx, &memory.Memory{ID: "mem_p", OrgID: memory.DefaultOrg, Scope: "user:alice", Content: "inside", Kind: memory.KindFact, Status: memory.StatusUnverified, Source: memory.SourceDirect, ContentHash: "hp", Version: 1}); err != nil {
				t.Fatal(err)
			}
			panic("handler bug")
		})
	}()
	m := &memory.Memory{ID: "mem_after", OrgID: memory.DefaultOrg, Scope: "user:alice", Content: "after", Kind: memory.KindFact, Status: memory.StatusUnverified, Source: memory.SourceDirect, ContentHash: "ha", Version: 1}
	if err := s.Insert(ctx, m); err != nil {
		t.Fatalf("write after a panicking transaction: %v", err)
	}
	if _, err := s.Get(ctx, memory.DefaultOrg, "mem_p"); err == nil {
		t.Fatal("the panicking transaction was committed")
	}
}
