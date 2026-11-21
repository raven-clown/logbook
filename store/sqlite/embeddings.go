package sqlite

import (
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"time"

	"github.com/raven-clown/logbook/memory"
)

// PutEmbedding stores v for the memory and model, replacing an earlier one.
func (s *Store) PutEmbedding(ctx context.Context, orgID, id, model string, v []float32) error {
	_, err := s.q.ExecContext(ctx, `INSERT INTO embeddings (org_id, memory_id, model, dims, vector, created_at) VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT (org_id, memory_id, model) DO UPDATE SET dims = excluded.dims, vector = excluded.vector, created_at = excluded.created_at`,
		orgID, id, model, len(v), encodeVector(v), formatTime(time.Now()))
	if err != nil {
		return fmt.Errorf("storing embedding: %w", err)
	}
	return nil
}

// DeleteEmbeddings removes every vector of the memory.
func (s *Store) DeleteEmbeddings(ctx context.Context, orgID, id string) error {
	if _, err := s.q.ExecContext(ctx, "DELETE FROM embeddings WHERE org_id = ? AND memory_id = ?", orgID, id); err != nil {
		return fmt.Errorf("deleting embeddings: %w", err)
	}
	return nil
}

// Embeddings calls fn for every vector of model whose memory is not forgotten.
func (s *Store) Embeddings(ctx context.Context, orgID, model string, fn func(id, scope string, v []float32) error) error {
	rows, err := s.q.QueryContext(ctx, `SELECT e.memory_id, m.scope, e.vector FROM embeddings e
		JOIN memories m ON m.org_id = e.org_id AND m.id = e.memory_id
		WHERE e.org_id = ? AND e.model = ? AND m.status != 'forgotten'`, orgID, model)
	if err != nil {
		return fmt.Errorf("reading embeddings: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id, scope string
		var blob []byte
		if err := rows.Scan(&id, &scope, &blob); err != nil {
			return fmt.Errorf("reading embeddings: %w", err)
		}
		if err := fn(id, scope, decodeVector(blob)); err != nil {
			return err
		}
	}
	return rows.Err()
}

// MissingEmbeddings returns up to limit memories that are not forgotten and
// have no vector for model, oldest first.
func (s *Store) MissingEmbeddings(ctx context.Context, orgID, model string, limit int) ([]*memory.Memory, error) {
	rows, err := s.q.QueryContext(ctx, "SELECT "+columns+` FROM memories m
		WHERE m.org_id = ? AND m.status != 'forgotten' AND NOT EXISTS (
			SELECT 1 FROM embeddings e WHERE e.org_id = m.org_id AND e.memory_id = m.id AND e.model = ?)
		ORDER BY m.created_at, m.id LIMIT ?`, orgID, model, limit)
	if err != nil {
		return nil, fmt.Errorf("finding memories without embeddings: %w", err)
	}
	return scanAll(rows)
}

// DropEmbeddings deletes every vector whose model is not keep, or every vector
// when keep is empty.
func (s *Store) DropEmbeddings(ctx context.Context, orgID, keep string) (int, error) {
	res, err := s.q.ExecContext(ctx, "DELETE FROM embeddings WHERE org_id = ? AND (? = '' OR model != ?)", orgID, keep, keep)
	if err != nil {
		return 0, fmt.Errorf("dropping embeddings: %w", err)
	}
	n, err := res.RowsAffected()
	return int(n), err
}

func encodeVector(v []float32) []byte {
	b := make([]byte, 4*len(v))
	for i, f := range v {
		binary.LittleEndian.PutUint32(b[4*i:], math.Float32bits(f))
	}
	return b
}

func decodeVector(b []byte) []float32 {
	v := make([]float32, len(b)/4)
	for i := range v {
		v[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[4*i:]))
	}
	return v
}
