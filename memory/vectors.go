package memory

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"sync/atomic"
	"time"
)

// vectorState is the per-process bookkeeping for the vector index.
type vectorState struct {
	// started is the wall-clock time the service was made. Vectors stored
	// before it reach the index through its first load.
	started   time.Time
	refreshMu sync.Mutex
	refreshed map[string]time.Time
	// backfillFrom is where the next backfill pass starts after one that
	// stopped early, so memories the model rejects do not block the rest.
	backfillMu   sync.Mutex
	backfillFrom map[string]string
}

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
	// neighbourCandidates is generous because the index also holds superseded
	// memories, which prepare skips.
	neighbourCandidates = 30
	backfillBudget      = 30 * time.Second
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
		return es.Embeddings(ctx, orgID, model, time.Time{}, fn)
	}
}

// refreshOverlap reaches back past the last refresh, so a vector stored while
// the previous refresh ran is not missed. Upserts are idempotent.
const refreshOverlap = 5 * time.Second

// RefreshVectors adds to the index the vectors other processes on the same
// database stored since the last refresh, and returns how many it read.
func (s *Service) RefreshVectors(ctx context.Context, orgID string) (int, error) {
	if !s.vectorsOn() {
		return 0, nil
	}
	s.refreshMu.Lock()
	defer s.refreshMu.Unlock()
	since, ok := s.refreshed[orgID]
	if !ok {
		since = s.started
	}
	start := time.Now()
	n := 0
	err := s.store.(EmbeddingStore).Embeddings(ctx, orgID, s.embedder.Model(), since.Add(-refreshOverlap), func(id, scope string, v []float32) error {
		n++
		return s.index.Upsert(ctx, orgID, scope, id, v)
	})
	if err != nil {
		return n, err
	}
	s.refreshed[orgID] = start
	return n, nil
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

// maxBackfillMisses ends a backfill pass when this many memories in a row
// fail, since the embedder is then most likely down.
const maxBackfillMisses = 3

// Backfill embeds up to batch memories at a time that have no vector for the
// current model, and returns how many it embedded. When a batch fails, its
// memories are tried one by one, so one memory the model always rejects does
// not hold up the ones after it. A pass that stops early is picked up after
// the point it stopped. The error reports the memories that failed.
func (s *Service) Backfill(ctx context.Context, orgID string, batch int) (int, error) {
	if !s.vectorsOn() || batch <= 0 {
		return 0, nil
	}
	s.backfillMu.Lock()
	defer s.backfillMu.Unlock()
	es := s.store.(EmbeddingStore)
	model := s.embedder.Model()
	total, failed, misses := 0, 0, 0
	var lastErr error
	after := s.backfillFrom[orgID]
	delete(s.backfillFrom, orgID)
	for {
		missing, err := es.MissingEmbeddings(ctx, orgID, model, after, batch)
		if err != nil {
			return total, err
		}
		if len(missing) == 0 {
			break
		}
		after = missing[len(missing)-1].ID
		texts := make([]string, len(missing))
		for i, m := range missing {
			texts[i] = m.Content
		}
		vs, err := s.embed(ctx, backfillBudget, texts)
		for i, m := range missing {
			var v []float32
			if err == nil {
				v = vs[i]
			} else {
				one, oneErr := s.embed(ctx, s.budgets.Embed, []string{m.Content})
				if oneErr != nil {
					failed, misses, lastErr = failed+1, misses+1, oneErr
					if misses >= maxBackfillMisses {
						s.backfillFrom[orgID] = m.ID
						return total, fmt.Errorf("embedding %d memories failed, stopping this pass: %w", failed, lastErr)
					}
					continue
				}
				v = one[0]
			}
			misses = 0
			stored, serr := s.storeBackfilled(ctx, m, model, v)
			if serr != nil {
				return total, serr
			}
			if stored {
				s.upsertVector(ctx, m, v)
				total++
			}
		}
		if len(missing) < batch {
			break
		}
	}
	if failed > 0 {
		return total, fmt.Errorf("embedding %d memories failed: %w", failed, lastErr)
	}
	return total, nil
}

// storeBackfilled writes a backfilled vector unless the memory was forgotten or
// changed while it was being embedded, so a purge is never undone.
func (s *Service) storeBackfilled(ctx context.Context, m *Memory, model string, v []float32) (bool, error) {
	stored := false
	err := s.store.Tx(ctx, func(st Store) error {
		cur, err := st.Get(ctx, m.OrgID, m.ID)
		if CodeOf(err) == CodeNotFound {
			return nil
		}
		if err != nil {
			return err
		}
		es, ok := st.(EmbeddingStore)
		if !ok || cur.Status == StatusForgotten || cur.ContentHash != m.ContentHash {
			return nil
		}
		stored = true
		return es.PutEmbedding(ctx, m.OrgID, m.ID, model, v)
	})
	return stored, err
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
	s.backfillMu.Lock()
	delete(s.backfillFrom, orgID)
	s.backfillMu.Unlock()
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
