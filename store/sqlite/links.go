package sqlite

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/raven-clown/logbook/memory"
)

// AddEdge stores an edge. The same edge twice is stored once.
func (s *Store) AddEdge(ctx context.Context, e memory.Edge) error {
	_, err := s.q.ExecContext(ctx, `INSERT OR IGNORE INTO edges (org_id, from_id, to_id, type, created_by, created_at)
		VALUES (?, ?, ?, ?, ?, ?)`, e.OrgID, e.From, e.To, e.Type, e.CreatedBy, formatTime(e.CreatedAt))
	if err != nil {
		return fmt.Errorf("adding edge: %w", err)
	}
	return nil
}

// Edges returns every edge that starts or ends at one of ids.
func (s *Store) Edges(ctx context.Context, orgID string, ids []string) ([]memory.Edge, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	in := placeholders(len(ids))
	args := []any{orgID}
	for range 2 {
		for _, id := range ids {
			args = append(args, id)
		}
	}
	rows, err := s.q.QueryContext(ctx, `SELECT org_id, from_id, to_id, type, created_by, created_at FROM edges
		WHERE org_id = ? AND (from_id IN (`+in+`) OR to_id IN (`+in+`)) ORDER BY created_at, rowid`, args...)
	if err != nil {
		return nil, fmt.Errorf("reading edges: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []memory.Edge
	for rows.Next() {
		var e memory.Edge
		var at string
		if err := rows.Scan(&e.OrgID, &e.From, &e.To, &e.Type, &e.CreatedBy, &at); err != nil {
			return nil, err
		}
		if e.CreatedAt, err = parseTime(at); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// AddVerification stores a confirm or dispute.
func (s *Store) AddVerification(ctx context.Context, v memory.Verification) error {
	_, err := s.q.ExecContext(ctx, `INSERT INTO verifications (org_id, memory_id, verifier_subject, verifier_channel,
		verifier_client, verdict, note, counted, at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		v.OrgID, v.MemoryID, v.Verifier.Subject, v.Verifier.Channel, v.Verifier.Client, v.Verdict, v.Note, v.Counted,
		formatTime(v.At))
	if err != nil {
		return fmt.Errorf("adding verification: %w", err)
	}
	return nil
}

// Verifications returns a memory's verifications, oldest first.
func (s *Store) Verifications(ctx context.Context, orgID, memoryID string) ([]memory.Verification, error) {
	rows, err := s.q.QueryContext(ctx, `SELECT org_id, memory_id, verifier_subject, verifier_channel, verifier_client,
		verdict, note, counted, at FROM verifications WHERE org_id = ? AND memory_id = ? ORDER BY rowid`, orgID, memoryID)
	if err != nil {
		return nil, fmt.Errorf("reading verifications: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []memory.Verification
	for rows.Next() {
		var v memory.Verification
		var at string
		if err := rows.Scan(&v.OrgID, &v.MemoryID, &v.Verifier.Subject, &v.Verifier.Channel, &v.Verifier.Client,
			&v.Verdict, &v.Note, &v.Counted, &at); err != nil {
			return nil, err
		}
		if v.At, err = parseTime(at); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// AppendEvent adds an entry to a memory's history.
func (s *Store) AppendEvent(ctx context.Context, e memory.Event) error {
	var detail sql.NullString
	if len(e.Detail) > 0 {
		detail = sql.NullString{String: string(e.Detail), Valid: true}
	}
	_, err := s.q.ExecContext(ctx, `INSERT INTO events (org_id, memory_id, action, actor_subject, actor_channel,
		actor_client, at, detail) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		e.OrgID, e.MemoryID, e.Action, e.Actor.Subject, e.Actor.Channel, e.Actor.Client, formatTime(e.At), detail)
	if err != nil {
		return fmt.Errorf("adding event: %w", err)
	}
	return nil
}

// Events returns a memory's history, oldest first.
func (s *Store) Events(ctx context.Context, orgID, memoryID string) ([]memory.Event, error) {
	rows, err := s.q.QueryContext(ctx, `SELECT org_id, memory_id, action, actor_subject, actor_channel, actor_client,
		at, detail FROM events WHERE org_id = ? AND memory_id = ? ORDER BY rowid`, orgID, memoryID)
	if err != nil {
		return nil, fmt.Errorf("reading events: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []memory.Event
	for rows.Next() {
		var e memory.Event
		var at string
		var detail sql.NullString
		if err := rows.Scan(&e.OrgID, &e.MemoryID, &e.Action, &e.Actor.Subject, &e.Actor.Channel, &e.Actor.Client,
			&at, &detail); err != nil {
			return nil, err
		}
		if e.At, err = parseTime(at); err != nil {
			return nil, err
		}
		if detail.Valid {
			e.Detail = []byte(detail.String)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
