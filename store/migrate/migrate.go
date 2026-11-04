// Package migrate runs numbered SQL files against a database, once each, in order.
package migrate

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var fileName = regexp.MustCompile(`^(\d{4})_[a-z0-9_]+\.sql$`)

type migration struct {
	version  int
	name     string
	sql      string
	checksum string
}

// Run applies the migrations in files that are not applied yet and returns their names.
// Files are named NNNN_name.sql; other files are ignored. Numbers must start at 0001
// with no gaps, and a file that was applied must not change afterwards.
func Run(ctx context.Context, db *sql.DB, files fs.FS) ([]string, error) {
	ms, err := load(files)
	if err != nil {
		return nil, err
	}
	if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER PRIMARY KEY,
		name TEXT NOT NULL,
		checksum TEXT NOT NULL,
		applied_at TEXT NOT NULL)`); err != nil {
		return nil, fmt.Errorf("creating schema_migrations: %w", err)
	}
	done, err := appliedChecksums(ctx, db)
	if err != nil {
		return nil, err
	}
	var applied []string
	for _, m := range ms {
		if sum, ok := done[m.version]; ok {
			if sum != m.checksum {
				return applied, fmt.Errorf("migration %s changed after it was applied", m.name)
			}
			continue
		}
		ran, err := apply(ctx, db, m)
		if err != nil {
			return applied, err
		}
		if ran {
			applied = append(applied, m.name)
		}
	}
	return applied, nil
}

func load(files fs.FS) ([]migration, error) {
	entries, err := fs.ReadDir(files, ".")
	if err != nil {
		return nil, fmt.Errorf("reading migrations: %w", err)
	}
	var ms []migration
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		match := fileName.FindStringSubmatch(e.Name())
		if match == nil {
			return nil, fmt.Errorf("migration %s must be named NNNN_name.sql", e.Name())
		}
		data, err := fs.ReadFile(files, e.Name())
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", e.Name(), err)
		}
		data = bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n"))
		sum := sha256.Sum256(data)
		v, _ := strconv.Atoi(match[1])
		ms = append(ms, migration{version: v, name: e.Name(), sql: string(data), checksum: hex.EncodeToString(sum[:])})
	}
	for i, m := range ms {
		if m.version != i+1 {
			return nil, fmt.Errorf("migration %04d is missing before %s", i+1, m.name)
		}
	}
	return ms, nil
}

func appliedChecksums(ctx context.Context, db *sql.DB) (map[int]string, error) {
	rows, err := db.QueryContext(ctx, "SELECT version, checksum FROM schema_migrations")
	if err != nil {
		return nil, fmt.Errorf("reading schema_migrations: %w", err)
	}
	defer func() { _ = rows.Close() }()
	done := map[int]string{}
	for rows.Next() {
		var v int
		var sum string
		if err := rows.Scan(&v, &sum); err != nil {
			return nil, fmt.Errorf("reading schema_migrations: %w", err)
		}
		done[v] = sum
	}
	return done, rows.Err()
}

// apply runs one migration in its own transaction. It checks again inside the
// transaction, so when two processes start on a new database only one applies
// it; the other reports false.
func apply(ctx context.Context, db *sql.DB, m migration) (bool, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("migration %s: %w", m.name, err)
	}
	defer func() { _ = tx.Rollback() }()
	var sum string
	switch err := tx.QueryRowContext(ctx, "SELECT checksum FROM schema_migrations WHERE version = ?", m.version).Scan(&sum); {
	case err == nil && sum == m.checksum:
		return false, nil
	case err == nil:
		return false, fmt.Errorf("migration %s changed after it was applied", m.name)
	case !errors.Is(err, sql.ErrNoRows):
		return false, fmt.Errorf("migration %s: %w", m.name, err)
	}
	if _, err := tx.ExecContext(ctx, m.sql); err != nil {
		return false, fmt.Errorf("migration %s: %w", m.name, err)
	}
	if _, err := tx.ExecContext(ctx,
		"INSERT INTO schema_migrations (version, name, checksum, applied_at) VALUES (?, ?, ?, ?)",
		m.version, m.name, m.checksum, time.Now().UTC().Format(time.RFC3339)); err != nil {
		return false, fmt.Errorf("migration %s: %w", m.name, err)
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("migration %s: %w", m.name, err)
	}
	return true, nil
}
