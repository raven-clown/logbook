package sqlite

import (
	"context"
	"fmt"
	"strings"
	"unicode"

	"github.com/raven-clown/logbook/memory"
)

// SearchText runs a full-text search. The query is plain text: every word is
// matched literally, so FTS5 operators and punctuation in it are never an error.
func (s *Store) SearchText(ctx context.Context, query string, f memory.Filter, limit int) ([]memory.Hit, error) {
	match := ftsQuery(query)
	if match == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = defaultLimit
	}
	where, args := whereClause(f)
	args = append([]any{match}, args...)
	args = append(args, limit)
	rows, err := s.q.QueryContext(ctx, `SELECT m.id, -bm25(memories_fts) FROM memories_fts
		JOIN memories m ON m.rid = memories_fts.rowid
		WHERE memories_fts MATCH ? AND m.purged = 0 AND `+where+`
		ORDER BY bm25(memories_fts), m.id LIMIT ?`, args...)
	if err != nil {
		return nil, fmt.Errorf("searching memories: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var hits []memory.Hit
	for rows.Next() {
		var h memory.Hit
		if err := rows.Scan(&h.ID, &h.Score); err != nil {
			return nil, err
		}
		hits = append(hits, h)
	}
	return hits, rows.Err()
}

func ftsQuery(q string) string {
	words := strings.FieldsFunc(q, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && !unicode.IsMark(r) && r != '.' && r != '_' && r != '-'
	})
	var terms []string
	for _, w := range words {
		w = strings.Trim(w, "._-")
		if w != "" {
			terms = append(terms, `"`+w+`"`)
		}
	}
	return strings.Join(terms, " OR ")
}
