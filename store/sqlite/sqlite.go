// Package sqlite is logbook's default storage backend: one SQLite file with
// FTS5 full-text search, on the pure-Go modernc.org/sqlite driver.
package sqlite

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"time"

	"github.com/raven-clown/logbook/memory"
	"github.com/raven-clown/logbook/store"
	"github.com/raven-clown/logbook/store/migrate"
	_ "modernc.org/sqlite" // database/sql driver
)

//go:embed migrations/*.sql
var migrations embed.FS

func init() {
	store.Register("sqlite", func(ctx context.Context, dsn string) (memory.Store, error) {
		return Open(ctx, dsn)
	})
}

type querier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// Store is a memory.Store on one SQLite database.
type Store struct {
	db   *sql.DB
	q    querier
	inTx bool
}

// Open opens or creates the database at path, which is a file path or ":memory:",
// and brings its schema up to date.
func Open(ctx context.Context, path string) (*Store, error) {
	dsn := path + "?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_txlock=immediate"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", path, err)
	}
	if path == ":memory:" {
		db.SetMaxOpenConns(1)
	}
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("opening %s: %w", path, err)
	}
	if path != ":memory:" {
		if err := useWAL(ctx, db); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("opening %s: %w", path, err)
		}
	}
	files, err := fs.Sub(migrations, "migrations")
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	if _, err := migrate.Run(ctx, db, files); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrating %s: %w", path, err)
	}
	return &Store{db: db, q: db}, nil
}

// Close closes the database.
func (s *Store) Close() error {
	return s.db.Close()
}

// Tx runs fn in a transaction. Calls inside fn on the Store it receives join that transaction.
func (s *Store) Tx(ctx context.Context, fn func(memory.Store) error) error {
	if s.inTx {
		return fn(s)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := fn(&Store{db: s.db, q: tx, inTx: true}); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing transaction: %w", err)
	}
	return nil
}

// useWAL switches the file to write-ahead logging. The mode is stored in the
// file, and switching a new file returns SQLITE_BUSY without waiting on the busy
// timeout when another process is opening it too, so it is retried for a while.
func useWAL(ctx context.Context, db *sql.DB) error {
	deadline := time.Now().Add(5 * time.Second)
	for {
		var mode string
		err := db.QueryRowContext(ctx, "PRAGMA journal_mode=WAL").Scan(&mode)
		if err == nil || !strings.Contains(err.Error(), "SQLITE_BUSY") || time.Now().After(deadline) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(20 * time.Millisecond):
		}
	}
}

const timeLayout = "2006-01-02T15:04:05.000000000Z"

func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(timeLayout)
}

func parseTime(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	return time.Parse(timeLayout, s)
}

func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

func notFound(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return memory.ErrNotFound
	}
	return err
}
