package sqlite

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/raven-clown/logbook/memory"
)

const columns = `m.id, m.org_id, m.scope, m.content, m.kind, m.subject, m.tags, m.refs, m.status, m.source,
	m.evidence, m.author_subject, m.author_channel, m.author_client, m.created_at, m.updated_at,
	m.valid_until, m.review_after, m.last_confirmed_at, m.confirmations, m.disputes, m.superseded_by,
	m.superseded_at, m.forgotten_at, m.purged, m.content_hash, m.version`

const defaultLimit = 50

// Insert stores a new memory and its full-text row.
func (s *Store) Insert(ctx context.Context, m *memory.Memory) error {
	return s.Tx(ctx, func(tx memory.Store) error {
		t := tx.(*Store)
		tags, refs, err := encodeLists(m)
		if err != nil {
			return err
		}
		res, err := t.q.ExecContext(ctx, `INSERT INTO memories (org_id, id, scope, content, kind, subject, tags, refs,
			status, source, evidence, author_subject, author_channel, author_client, created_at, updated_at,
			valid_until, review_after, last_confirmed_at, confirmations, disputes, superseded_by, superseded_at,
			forgotten_at, purged, content_hash, version)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			m.OrgID, m.ID, m.Scope, m.Content, m.Kind, m.Subject, tags, refs, m.Status, m.Source, m.Evidence,
			m.Author.Subject, m.Author.Channel, m.Author.Client, formatTime(m.CreatedAt), formatTime(m.UpdatedAt),
			formatTime(m.ValidUntil), formatTime(m.ReviewAfter), formatTime(m.LastConfirmedAt), m.Confirmations,
			m.Disputes, m.SupersededBy, formatTime(m.SupersededAt), formatTime(m.ForgottenAt), m.Purged,
			m.ContentHash, m.Version)
		if err != nil {
			return fmt.Errorf("inserting memory %s: %w", m.ID, err)
		}
		rid, err := res.LastInsertId()
		if err != nil {
			return err
		}
		return t.indexText(ctx, rid, m)
	})
}

// Update writes m if its version still matches and keeps the full-text row in step.
func (s *Store) Update(ctx context.Context, m *memory.Memory) error {
	return s.Tx(ctx, func(tx memory.Store) error {
		t := tx.(*Store)
		tags, refs, err := encodeLists(m)
		if err != nil {
			return err
		}
		res, err := t.q.ExecContext(ctx, `UPDATE memories SET scope = ?, content = ?, kind = ?, subject = ?, tags = ?,
			refs = ?, status = ?, source = ?, evidence = ?, author_subject = ?, author_channel = ?, author_client = ?,
			created_at = ?, updated_at = ?, valid_until = ?, review_after = ?, last_confirmed_at = ?, confirmations = ?,
			disputes = ?, superseded_by = ?, superseded_at = ?, forgotten_at = ?, purged = ?, content_hash = ?,
			version = version + 1
			WHERE org_id = ? AND id = ? AND version = ?`,
			m.Scope, m.Content, m.Kind, m.Subject, tags, refs, m.Status, m.Source, m.Evidence, m.Author.Subject,
			m.Author.Channel, m.Author.Client, formatTime(m.CreatedAt), formatTime(m.UpdatedAt),
			formatTime(m.ValidUntil), formatTime(m.ReviewAfter), formatTime(m.LastConfirmedAt), m.Confirmations,
			m.Disputes, m.SupersededBy, formatTime(m.SupersededAt), formatTime(m.ForgottenAt), m.Purged,
			m.ContentHash, m.OrgID, m.ID, m.Version)
		if err != nil {
			return fmt.Errorf("updating memory %s: %w", m.ID, err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n == 0 {
			if _, err := t.Get(ctx, m.OrgID, m.ID); err != nil {
				return err
			}
			return memory.ErrVersionConflict
		}
		var rid int64
		if err := t.q.QueryRowContext(ctx, "SELECT rid FROM memories WHERE org_id = ? AND id = ?", m.OrgID, m.ID).Scan(&rid); err != nil {
			return err
		}
		if _, err := t.q.ExecContext(ctx, "DELETE FROM memories_fts WHERE rowid = ?", rid); err != nil {
			return fmt.Errorf("updating search index: %w", err)
		}
		if err := t.indexText(ctx, rid, m); err != nil {
			return err
		}
		m.Version++
		return nil
	})
}

func (s *Store) indexText(ctx context.Context, rid int64, m *memory.Memory) error {
	if m.Purged {
		return nil
	}
	_, err := s.q.ExecContext(ctx, "INSERT INTO memories_fts (rowid, content, subject, tags) VALUES (?, ?, ?, ?)",
		rid, m.Content, m.Subject, strings.Join(m.Tags, " "))
	if err != nil {
		return fmt.Errorf("indexing memory %s: %w", m.ID, err)
	}
	return nil
}

func encodeLists(m *memory.Memory) (string, string, error) {
	tags, err := json.Marshal(nonNil(m.Tags))
	if err != nil {
		return "", "", err
	}
	refs, err := json.Marshal(nonNil(m.Refs))
	if err != nil {
		return "", "", err
	}
	return string(tags), string(refs), nil
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// Get returns one memory.
func (s *Store) Get(ctx context.Context, orgID, id string) (*memory.Memory, error) {
	row := s.q.QueryRowContext(ctx, "SELECT "+columns+" FROM memories m WHERE m.org_id = ? AND m.id = ?", orgID, id)
	m, err := scanMemory(row)
	return m, notFound(err)
}

// FindByHash returns the active memory with this content hash in scope.
func (s *Store) FindByHash(ctx context.Context, orgID, scope, hash string) (*memory.Memory, error) {
	row := s.q.QueryRowContext(ctx, "SELECT "+columns+` FROM memories m
		WHERE m.org_id = ? AND m.scope = ? AND m.content_hash = ? AND m.status IN ('unverified', 'verified', 'disputed')
		ORDER BY m.created_at LIMIT 1`, orgID, scope, hash)
	m, err := scanMemory(row)
	return m, notFound(err)
}

// FindBySubject returns the active memories with this subject in scope.
func (s *Store) FindBySubject(ctx context.Context, orgID, scope, subject string) ([]*memory.Memory, error) {
	rows, err := s.q.QueryContext(ctx, "SELECT "+columns+` FROM memories m
		WHERE m.org_id = ? AND m.scope = ? AND m.subject = ? AND m.status IN ('unverified', 'verified', 'disputed')
		ORDER BY m.created_at`, orgID, scope, subject)
	if err != nil {
		return nil, err
	}
	return scanAll(rows)
}

// List returns one page of memories matching f.
func (s *Store) List(ctx context.Context, f memory.Filter) ([]*memory.Memory, string, error) {
	limit := f.Limit
	if limit <= 0 {
		limit = defaultLimit
	}
	offset, err := decodeCursor(f.Cursor)
	if err != nil {
		return nil, "", err
	}
	where, args := whereClause(f)
	order := "m.created_at DESC, m.id DESC"
	if f.Order == memory.OrderReview {
		order = `CASE WHEN m.status = 'disputed' THEN 0
			WHEN m.review_after != '' AND m.review_after <= ? THEN 1
			WHEN m.status = 'unverified' THEN 2
			ELSE 3 END, ` + order
		args = append(args, formatTime(f.Now))
	}
	args = append(args, limit+1, offset)
	rows, err := s.q.QueryContext(ctx, "SELECT "+columns+" FROM memories m WHERE "+where+" ORDER BY "+order+" LIMIT ? OFFSET ?", args...)
	if err != nil {
		return nil, "", fmt.Errorf("listing memories: %w", err)
	}
	ms, err := scanAll(rows)
	if err != nil {
		return nil, "", err
	}
	next := ""
	if len(ms) > limit {
		ms = ms[:limit]
		next = encodeCursor(offset + limit)
	}
	return ms, next, nil
}

func whereClause(f memory.Filter) (string, []any) {
	clauses := []string{"m.org_id = ?"}
	args := []any{f.OrgID}
	if len(f.Scopes) == 0 {
		clauses = append(clauses, "0")
	} else {
		clauses = append(clauses, "m.scope IN ("+placeholders(len(f.Scopes))+")")
		for _, sc := range f.Scopes {
			args = append(args, sc)
		}
	}
	switch {
	case len(f.Statuses) > 0:
		clauses = append(clauses, "m.status IN ("+placeholders(len(f.Statuses))+")")
		for _, st := range f.Statuses {
			args = append(args, st)
		}
	case f.AsOf.IsZero():
		clauses = append(clauses, "m.status != 'forgotten'")
	}
	if len(f.Kinds) > 0 {
		clauses = append(clauses, "m.kind IN ("+placeholders(len(f.Kinds))+")")
		for _, k := range f.Kinds {
			args = append(args, k)
		}
	}
	if len(f.Tags) > 0 {
		clauses = append(clauses, "EXISTS (SELECT 1 FROM json_each(m.tags) WHERE json_each.value IN ("+placeholders(len(f.Tags))+"))")
		for _, tg := range f.Tags {
			args = append(args, tg)
		}
	}
	if f.Subject != "" {
		clauses = append(clauses, "m.subject = ?")
		args = append(args, f.Subject)
	}
	if f.Author != "" {
		clauses = append(clauses, "m.author_subject = ?")
		args = append(args, f.Author)
	}
	if !f.StaleAt.IsZero() {
		clauses = append(clauses, "m.review_after != '' AND m.review_after <= ?")
		args = append(args, formatTime(f.StaleAt))
	}
	if !f.AsOf.IsZero() {
		at := formatTime(f.AsOf)
		clauses = append(clauses, "m.created_at <= ? AND (m.superseded_at = '' OR m.superseded_at > ?) AND (m.forgotten_at = '' OR m.forgotten_at > ?)")
		args = append(args, at, at, at)
	}
	return strings.Join(clauses, " AND "), args
}

func encodeCursor(offset int) string {
	return base64.RawURLEncoding.EncodeToString([]byte("o" + strconv.Itoa(offset)))
}

func decodeCursor(c string) (int, error) {
	if c == "" {
		return 0, nil
	}
	b, err := base64.RawURLEncoding.DecodeString(c)
	if err == nil && len(b) > 1 && b[0] == 'o' {
		if n, err := strconv.Atoi(string(b[1:])); err == nil && n >= 0 {
			return n, nil
		}
	}
	return 0, memory.Errorf(memory.CodeInvalidArgument, "cursor", "cursor is not valid")
}

type scanner interface {
	Scan(dest ...any) error
}

func scanMemory(r scanner) (*memory.Memory, error) {
	var m memory.Memory
	var tags, refs, created, updated, validUntil, reviewAfter, confirmed, supersededAt, forgottenAt string
	err := r.Scan(&m.ID, &m.OrgID, &m.Scope, &m.Content, &m.Kind, &m.Subject, &tags, &refs, &m.Status, &m.Source,
		&m.Evidence, &m.Author.Subject, &m.Author.Channel, &m.Author.Client, &created, &updated, &validUntil,
		&reviewAfter, &confirmed, &m.Confirmations, &m.Disputes, &m.SupersededBy, &supersededAt, &forgottenAt,
		&m.Purged, &m.ContentHash, &m.Version)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(tags), &m.Tags); err != nil {
		return nil, fmt.Errorf("memory %s tags: %w", m.ID, err)
	}
	if err := json.Unmarshal([]byte(refs), &m.Refs); err != nil {
		return nil, fmt.Errorf("memory %s refs: %w", m.ID, err)
	}
	if len(m.Tags) == 0 {
		m.Tags = nil
	}
	if len(m.Refs) == 0 {
		m.Refs = nil
	}
	for _, p := range []struct {
		dst *time.Time
		src string
	}{
		{&m.CreatedAt, created}, {&m.UpdatedAt, updated}, {&m.ValidUntil, validUntil}, {&m.ReviewAfter, reviewAfter},
		{&m.LastConfirmedAt, confirmed}, {&m.SupersededAt, supersededAt}, {&m.ForgottenAt, forgottenAt},
	} {
		t, err := parseTime(p.src)
		if err != nil {
			return nil, fmt.Errorf("memory %s: %w", m.ID, err)
		}
		*p.dst = t
	}
	return &m, nil
}

func scanAll(rows *sql.Rows) ([]*memory.Memory, error) {
	defer func() { _ = rows.Close() }()
	var out []*memory.Memory
	for rows.Next() {
		m, err := scanMemory(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
