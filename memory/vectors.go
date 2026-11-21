package memory

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"sync/atomic"
	"time"
)

// Embedder turns texts into vectors. embed.Embedder has the same methods.
type Embedder interface {
	Embed(ctx context.Context, texts []string) ([][]float32, error)
	Model() string
}

// Index finds the nearest vectors. vector.Index has the same methods.
type Index interface {
	Upsert(ctx context.Context, orgID, scope, id string, v []float32) error
	Delete(ctx context.Context, orgID, id string) error
	Search(ctx context.Context, orgID string, scopes []string, v []float32, k int) ([]Hit, error)
}

// Extractor pulls facts out of text and checks for contradictions.
// extract.Extractor has the same methods.
type Extractor interface {
	Extract(ctx context.Context, text string) ([]Candidate, error)
	Contradicts(ctx context.Context, a, b string) (bool, error)
}

// Budgets cap how long a call waits for the models. Zero values take the defaults.
type Budgets struct {
	// Embed is the time allowed to embed a new memory, 3 seconds by default.
	Embed time.Duration
	// Query is the time allowed to embed a recall query, 2 seconds by default.
	Query time.Duration
	// Contradiction is the time allowed for all contradiction checks of one write, 2 seconds by default.
	Contradiction time.Duration
	// Extract is the time allowed to extract facts, 30 seconds by default.
	Extract time.Duration
}

func (b Budgets) withDefaults() Budgets {
	if b.Embed <= 0 {
		b.Embed = 3 * time.Second
	}
	if b.Query <= 0 {
		b.Query = 2 * time.Second
	}
	if b.Contradiction <= 0 {
		b.Contradiction = 2 * time.Second
	}
	if b.Extract <= 0 {
		b.Extract = 30 * time.Second
	}
	return b
}

// Thresholds from the design.
const (
	relatedSimilarity    = 0.80
	maxRelated           = 3
	contradictSimilarity = 0.85
	maxContradictChecks  = 3
	neighbourCandidates  = 10
	backfillBudget       = 30 * time.Second
)

// Degraded names reported when a model call is skipped.
const (
	DegradedEmbeddings    = "embeddings"
	DegradedContradiction = "contradiction_check"
)

const (
	stateUnknown int32 = iota
	stateOK
	stateFailing
)

// VectorLoader reads the stored vectors of model for a vector index that keeps
// them in memory. It fails when st does not store embeddings.
func VectorLoader(st Store, model string) func(ctx context.Context, orgID string, fn func(id, scope string, v []float32) error) error {
	return func(ctx context.Context, orgID string, fn func(id, scope string, v []float32) error) error {
		es, ok := st.(EmbeddingStore)
		if !ok {
			return errors.New("the store does not keep embeddings")
		}
		return es.Embeddings(ctx, orgID, model, fn)
	}
}

func (s *Service) vectorsOn() bool {
	_, ok := s.store.(EmbeddingStore)
	return ok && s.embedder != nil && s.index != nil
}

// EmbedderState is "not configured", "unknown" before the first call, then
// "ok" or "failing" after the last one.
func (s *Service) EmbedderState() string {
	if s.embedder == nil {
		return "not configured"
	}
	switch atomic.LoadInt32(&s.embedState) {
	case stateOK:
		return "ok"
	case stateFailing:
		return "failing"
	}
	return "unknown"
}

// embed embeds texts within budget and records whether the embedder answered.
func (s *Service) embed(ctx context.Context, budget time.Duration, texts []string) ([][]float32, error) {
	ctx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	vs, err := s.embedder.Embed(ctx, texts)
	if err == nil && len(vs) != len(texts) {
		err = errors.New("the embedder returned the wrong number of vectors")
	}
	if err != nil {
		atomic.StoreInt32(&s.embedState, stateFailing)
		slog.WarnContext(ctx, "embedding failed", "model", s.embedder.Model(), "err", err)
		return nil, err
	}
	atomic.StoreInt32(&s.embedState, stateOK)
	return vs, nil
}

func (s *Service) upsertVector(ctx context.Context, m *Memory, v []float32) {
	if err := s.index.Upsert(ctx, m.OrgID, m.Scope, m.ID, v); err != nil {
		slog.WarnContext(ctx, "updating the vector index failed", "id", m.ID, "err", err)
	}
}

// dropVectors removes a memory's vectors inside a transaction. The index entry
// goes after the commit, in forgetVector.
func (s *Service) dropVectors(ctx context.Context, st Store, m *Memory) error {
	if es, ok := st.(EmbeddingStore); ok {
		return es.DeleteEmbeddings(ctx, m.OrgID, m.ID)
	}
	return nil
}

func (s *Service) forgetVector(ctx context.Context, m *Memory) {
	if s.index == nil {
		return
	}
	if err := s.index.Delete(ctx, m.OrgID, m.ID); err != nil {
		slog.WarnContext(ctx, "removing from the vector index failed", "id", m.ID, "err", err)
	}
}

// Backfill embeds up to batch memories at a time that have no vector for the
// current model, until none are left, and returns how many it embedded.
func (s *Service) Backfill(ctx context.Context, orgID string, batch int) (int, error) {
	if !s.vectorsOn() || batch <= 0 {
		return 0, nil
	}
	es := s.store.(EmbeddingStore)
	model := s.embedder.Model()
	total := 0
	for {
		missing, err := es.MissingEmbeddings(ctx, orgID, model, batch)
		if err != nil || len(missing) == 0 {
			return total, err
		}
		texts := make([]string, len(missing))
		for i, m := range missing {
			texts[i] = m.Content
		}
		vs, err := s.embed(ctx, backfillBudget, texts)
		if err != nil {
			return total, err
		}
		for i, m := range missing {
			if err := es.PutEmbedding(ctx, orgID, m.ID, model, vs[i]); err != nil {
				return total, err
			}
			s.upsertVector(ctx, m, vs[i])
			total++
		}
		if len(missing) < batch {
			return total, nil
		}
	}
}

// ExtractorState is "configured" or "not configured".
func (s *Service) ExtractorState() string {
	if s.extractor == nil {
		return "not configured"
	}
	return "configured"
}

// ErrNoEmbedder is returned by Reindex when no embedder is configured.
var ErrNoEmbedder = errors.New("no embedder is configured: set embedder.type, embedder.base_url and embedder.model")

// Reindex drops the vectors of other models, or every vector when all is set,
// and embeds what is missing for the current model. A process with an
// in-memory index must restart afterwards to forget the dropped vectors.
func (s *Service) Reindex(ctx context.Context, orgID string, all bool, batch int) (dropped, embedded int, err error) {
	if !s.vectorsOn() {
		return 0, 0, ErrNoEmbedder
	}
	keep := s.embedder.Model()
	if all {
		keep = ""
	}
	if dropped, err = s.store.(EmbeddingStore).DropEmbeddings(ctx, orgID, keep); err != nil {
		return 0, 0, err
	}
	embedded, err = s.Backfill(ctx, orgID, batch)
	return dropped, embedded, err
}

// matches applies the parts of f that the text search applies in SQL, for hits
// that come from the vector index.
func matches(m *Memory, f Filter) bool {
	if m.OrgID != f.OrgID || (!f.AllScopes && !slices.Contains(f.Scopes, m.Scope)) || m.Purged {
		return false
	}
	switch {
	case len(f.Statuses) > 0:
		if !slices.Contains(f.Statuses, m.Status) {
			return false
		}
	case f.AsOf.IsZero():
		if m.Status == StatusForgotten {
			return false
		}
	}
	if len(f.Kinds) > 0 && !slices.Contains(f.Kinds, m.Kind) {
		return false
	}
	if len(f.Tags) > 0 && !slices.ContainsFunc(f.Tags, func(t string) bool { return slices.Contains(m.Tags, t) }) {
		return false
	}
	if !f.AsOf.IsZero() {
		if m.CreatedAt.After(f.AsOf) {
			return false
		}
		if !m.SupersededAt.IsZero() && !m.SupersededAt.After(f.AsOf) {
			return false
		}
		if !m.ForgottenAt.IsZero() && !m.ForgottenAt.After(f.AsOf) {
			return false
		}
	}
	return true
}
