package sqlite

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/raven-clown/logbook/memory"
)

func TestBusyIsUnavailable(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "logbook.db")
	s, err := open(ctx, path, 50)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	other, err := sql.Open("sqlite", path+"?_txlock=immediate")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = other.Close() }()
	tx, err := other.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	m := &memory.Memory{ID: "mem_1", OrgID: memory.DefaultOrg, Scope: "user:alice", Content: "x", Kind: memory.KindFact, Status: memory.StatusUnverified, Source: memory.SourceDirect, ContentHash: "h", Version: 1}
	if err := s.Insert(ctx, m); memory.CodeOf(err) != memory.CodeUnavailable {
		t.Fatalf("insert while another process writes: %v", err)
	}
	err = s.Tx(ctx, func(memory.Store) error { return nil })
	if memory.CodeOf(err) != memory.CodeUnavailable {
		t.Fatalf("transaction while another process writes: %v", err)
	}
}
