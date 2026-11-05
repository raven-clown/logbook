package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/raven-clown/logbook/admin"
	"github.com/raven-clown/logbook/memory"
)

var _ admin.Store = (*Store)(nil)

func unique(err error, what string) error {
	if err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed") {
		return memory.Errorf(memory.CodeConflict, "", "%s already exists", what)
	}
	return err
}

func deleted(res sql.Result, err error, what, id string) error {
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return memory.Errorf(memory.CodeNotFound, "id", "%s %s not found", what, id)
	}
	return nil
}

// CreateAPIKey implements admin.Store.
func (s *Store) CreateAPIKey(ctx context.Context, k *admin.APIKey) error {
	_, err := s.q.ExecContext(ctx,
		"INSERT INTO api_keys (org_id, id, name, hash, last4, created_at, last_used_at, version) VALUES (?, ?, ?, ?, ?, ?, ?, 1)",
		k.OrgID, k.ID, k.Name, k.Hash, k.Last4, formatTime(k.CreatedAt), formatTime(k.LastUsedAt))
	if err != nil {
		return unique(err, fmt.Sprintf("a key named %q", k.Name))
	}
	k.Version = 1
	return nil
}

const keyColumns = "org_id, id, name, hash, last4, created_at, last_used_at, version"

func scanKey(row interface{ Scan(...any) error }) (*admin.APIKey, error) {
	var k admin.APIKey
	var created, used string
	if err := row.Scan(&k.OrgID, &k.ID, &k.Name, &k.Hash, &k.Last4, &created, &used, &k.Version); err != nil {
		return nil, err
	}
	var err error
	if k.CreatedAt, err = parseTime(created); err != nil {
		return nil, err
	}
	if k.LastUsedAt, err = parseTime(used); err != nil {
		return nil, err
	}
	return &k, nil
}

// APIKeyByHash implements admin.Store.
func (s *Store) APIKeyByHash(ctx context.Context, orgID, hash string) (*admin.APIKey, error) {
	k, err := scanKey(s.q.QueryRowContext(ctx, "SELECT "+keyColumns+" FROM api_keys WHERE org_id = ? AND hash = ?", orgID, hash))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, memory.Errorf(memory.CodeNotFound, "", "no such key")
	}
	return k, err
}

// ListAPIKeys implements admin.Store.
func (s *Store) ListAPIKeys(ctx context.Context, orgID string) ([]*admin.APIKey, error) {
	rows, err := s.q.QueryContext(ctx, "SELECT "+keyColumns+" FROM api_keys WHERE org_id = ? ORDER BY created_at, id", orgID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []*admin.APIKey
	for rows.Next() {
		k, err := scanKey(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

// DeleteAPIKey implements admin.Store.
func (s *Store) DeleteAPIKey(ctx context.Context, orgID, id string) error {
	res, err := s.q.ExecContext(ctx, "DELETE FROM api_keys WHERE org_id = ? AND id = ?", orgID, id)
	return deleted(res, err, "key", id)
}

// TouchAPIKey implements admin.Store.
func (s *Store) TouchAPIKey(ctx context.Context, orgID, id string, at time.Time) error {
	_, err := s.q.ExecContext(ctx, "UPDATE api_keys SET last_used_at = ? WHERE org_id = ? AND id = ?", formatTime(at), orgID, id)
	return err
}

// CreateBinding implements admin.Store.
func (s *Store) CreateBinding(ctx context.Context, b *admin.Binding) error {
	_, err := s.q.ExecContext(ctx,
		"INSERT INTO bindings (org_id, id, principal, role, target, created_at, version) VALUES (?, ?, ?, ?, ?, ?, 1)",
		b.OrgID, b.ID, b.Principal, string(b.Role), b.Target, formatTime(b.CreatedAt))
	if err != nil {
		return unique(err, fmt.Sprintf("a %s binding for %s on %s", b.Role, b.Principal, b.Target))
	}
	b.Version = 1
	return nil
}

// Bindings implements admin.Store.
func (s *Store) Bindings(ctx context.Context, orgID string, principals []string) ([]*admin.Binding, error) {
	if principals != nil && len(principals) == 0 {
		return nil, nil
	}
	q := "SELECT org_id, id, principal, role, target, created_at, version FROM bindings WHERE org_id = ?"
	args := []any{orgID}
	if principals != nil {
		q += " AND principal IN (" + placeholders(len(principals)) + ")"
		for _, p := range principals {
			args = append(args, p)
		}
	}
	rows, err := s.q.QueryContext(ctx, q+" ORDER BY created_at, id", args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []*admin.Binding
	for rows.Next() {
		var b admin.Binding
		var role, created string
		if err := rows.Scan(&b.OrgID, &b.ID, &b.Principal, &role, &b.Target, &created, &b.Version); err != nil {
			return nil, err
		}
		b.Role = admin.Role(role)
		if b.CreatedAt, err = parseTime(created); err != nil {
			return nil, err
		}
		out = append(out, &b)
	}
	return out, rows.Err()
}

// DeleteBinding implements admin.Store.
func (s *Store) DeleteBinding(ctx context.Context, orgID, id string) error {
	res, err := s.q.ExecContext(ctx, "DELETE FROM bindings WHERE org_id = ? AND id = ?", orgID, id)
	return deleted(res, err, "binding", id)
}
