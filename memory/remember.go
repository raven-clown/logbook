package memory

import (
	"context"
	"log/slog"
	"slices"
	"time"
)

// RememberRequest is one memory to store. Scope is "", "user", "project", "team"
// or a full scope string; empty means the current project if there is one, else the user.
type RememberRequest struct {
	Content    string
	Kind       Kind
	Subject    string
	Scope      string
	Project    string
	Team       string
	Tags       []string
	Refs       []string
	Corrects   string
	RelatedTo  []string
	ValidUntil time.Time
	Evidence   string
	Extract    bool
	Source     Source
}

// Remembered describes one memory a remember call stored or found.
type Remembered struct {
	ID         string   `json:"id"`
	Scope      string   `json:"scope"`
	Status     Status   `json:"status"`
	Duplicate  bool     `json:"duplicate,omitempty"`
	Conflicts  []string `json:"conflicts,omitempty"`
	Superseded []string `json:"superseded,omitempty"`
}

// RememberResult is the result of Remember.
type RememberResult struct {
	Memories []Remembered `json:"memories"`
	Degraded []string     `json:"degraded,omitempty"`
}

// Remember stores a memory, or returns the existing one if the same content is
// already in the scope. Corrects supersedes an older memory; a shared Subject
// with different content marks the older memory disputed.
func (s *Service) Remember(ctx context.Context, p Principal, r RememberRequest) (*RememberResult, error) {
	if r.Extract {
		return s.rememberExtracted(ctx, p, r)
	}
	content, err := validContent(r.Content)
	if err != nil {
		return nil, err
	}
	kind, err := ParseKind(string(r.Kind))
	if err != nil {
		return nil, err
	}
	if err := validMeta(r); err != nil {
		return nil, err
	}
	sc, err := s.targetScope(ctx, p, r)
	if err != nil {
		return nil, err
	}
	out, degraded, err := s.rememberOne(ctx, p, r, sc, content, kind)
	if err != nil {
		return nil, err
	}
	return &RememberResult{Memories: []Remembered{out}, Degraded: degraded}, nil
}

// maxExtracted is the most facts one extract call stores.
const maxExtracted = 10

// rememberExtracted asks the extractor for the facts in r.Content and stores
// each one like a plain remember, with source extracted and its sentence as evidence.
func (s *Service) rememberExtracted(ctx context.Context, p Principal, r RememberRequest) (*RememberResult, error) {
	if s.extractor == nil {
		return nil, Errorf(CodeUnavailable, "", "no extractor is configured, so extract is not available")
	}
	if r.Corrects != "" {
		return nil, Errorf(CodeInvalidArgument, "corrects", "corrects cannot be used with extract")
	}
	text, err := validContent(r.Content)
	if err != nil {
		return nil, err
	}
	if err := validMeta(r); err != nil {
		return nil, err
	}
	sc, err := s.targetScope(ctx, p, r)
	if err != nil {
		return nil, err
	}
	ectx, cancel := context.WithTimeout(ctx, s.budgets.Extract)
	facts, err := s.extractor.Extract(ectx, text)
	cancel()
	if err != nil {
		slog.WarnContext(ctx, "extraction failed", "err", err)
		return nil, Errorf(CodeUnavailable, "", "the extractor did not answer, try again later")
	}
	res := &RememberResult{Memories: []Remembered{}}
	for _, c := range facts {
		if len(res.Memories) == maxExtracted {
			break
		}
		content, err := validContent(c.Content)
		if err != nil {
			continue
		}
		kind, err := ParseKind(string(c.Kind))
		if err != nil {
			kind = KindFact
		}
		fr := r
		fr.Extract, fr.Source, fr.Kind, fr.Subject, fr.Evidence = false, SourceExtracted, kind, c.Subject, c.Evidence
		if validMeta(fr) != nil {
			fr.Subject, fr.Evidence = "", ""
		}
		out, degraded, err := s.rememberOne(ctx, p, fr, sc, content, kind)
		if err != nil {
			return nil, err
		}
		res.Memories = append(res.Memories, out)
		for _, d := range degraded {
			if !slices.Contains(res.Degraded, d) {
				res.Degraded = append(res.Degraded, d)
			}
		}
	}
	return res, nil
}

// rememberOne stores one validated memory in sc.
func (s *Service) rememberOne(ctx context.Context, p Principal, r RememberRequest, sc Scope, content string, kind Kind) (Remembered, []string, error) {
	source := r.Source
	if source == "" {
		source = SourceDirect
	}
	hash := ContentHash(content)
	now := s.clock()
	m := &Memory{
		OrgID: p.OrgID, Scope: sc.String(), Content: content, Kind: kind, Subject: r.Subject,
		Tags: r.Tags, Refs: r.Refs, Status: StatusUnverified, Source: source, Evidence: r.Evidence,
		Author: p.Author(), CreatedAt: now, UpdatedAt: now, ValidUntil: r.ValidUntil.UTC(),
		ReviewAfter: ReviewAfter(now, kind, s.periods), ContentHash: hash, Version: 1,
	}
	prep := s.prepare(ctx, m, r.Corrects)
	var out Remembered
	err := s.store.Tx(ctx, func(st Store) error {
		if dup, err := st.FindByHash(ctx, p.OrgID, m.Scope, hash); err == nil {
			if dup.ID == r.Corrects {
				return Errorf(CodeInvalidArgument, "corrects", "the correction has the same content as memory %s", dup.ID)
			}
			out = Remembered{ID: dup.ID, Scope: dup.Scope, Status: dup.Status, Duplicate: true}
			return nil
		} else if CodeOf(err) != CodeNotFound {
			return err
		}
		var old *Memory
		var err error
		if r.Corrects != "" {
			if old, err = s.correctable(ctx, st, p, r.Corrects); err != nil {
				return err
			}
		}
		for _, id := range r.RelatedTo {
			if _, err := s.readable(ctx, st, p, id, "related_to"); err != nil {
				return err
			}
		}
		m.ID = s.newID()
		if err := st.Insert(ctx, m); err != nil {
			return err
		}
		if err := s.event(ctx, st, p, m.ID, "created", nil); err != nil {
			return err
		}
		if es, ok := st.(EmbeddingStore); ok && prep.vector != nil {
			if err := es.PutEmbedding(ctx, m.OrgID, m.ID, s.embedder.Model(), prep.vector); err != nil {
				return err
			}
		}
		out = Remembered{ID: m.ID, Scope: m.Scope, Status: m.Status}
		if old != nil {
			if err := s.supersede(ctx, st, p, old, m); err != nil {
				return err
			}
			out.Superseded = []string{old.ID}
		} else if m.Subject != "" {
			if out.Conflicts, err = s.dispute(ctx, st, p, m); err != nil {
				return err
			}
		}
		for _, id := range prep.contradicts {
			o, err := st.Get(ctx, p.OrgID, id)
			if err != nil || !o.Status.Active() || slices.Contains(out.Conflicts, id) {
				continue
			}
			if err := s.markDisputed(ctx, st, p, m, o); err != nil {
				return err
			}
			out.Conflicts = append(out.Conflicts, id)
		}
		for _, id := range r.RelatedTo {
			if err := st.AddEdge(ctx, Edge{OrgID: p.OrgID, From: m.ID, To: id, Type: EdgeRelated, CreatedBy: p.Subject, CreatedAt: now}); err != nil {
				return err
			}
		}
		for _, id := range prep.related {
			if slices.Contains(out.Conflicts, id) || slices.Contains(r.RelatedTo, id) {
				continue
			}
			if err := st.AddEdge(ctx, Edge{OrgID: p.OrgID, From: m.ID, To: id, Type: EdgeRelated, CreatedBy: jobAuthor.Subject, CreatedAt: now}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return Remembered{}, nil, err
	}
	if prep.vector != nil && !out.Duplicate {
		s.upsertVector(ctx, m, prep.vector)
	}
	return out, prep.degraded, nil
}

// prepared is the model work for one write, done before its transaction so a
// slow endpoint never holds the database.
type prepared struct {
	vector      []float32
	related     []string
	contradicts []string
	degraded    []string
}

// prepare embeds m and looks for near neighbours in its scope: the closest
// become related, and for a memory without a subject the extractor checks the
// closest for a contradiction.
func (s *Service) prepare(ctx context.Context, m *Memory, corrects string) prepared {
	var out prepared
	if !s.vectorsOn() {
		return out
	}
	if _, err := s.store.FindByHash(ctx, m.OrgID, m.Scope, m.ContentHash); err == nil {
		return out
	}
	vs, err := s.embed(ctx, s.budgets.Embed, []string{m.Content})
	if err != nil {
		out.degraded = append(out.degraded, DegradedEmbeddings)
		return out
	}
	out.vector = vs[0]
	hits, err := s.index.Search(ctx, m.OrgID, []string{m.Scope}, out.vector, neighbourCandidates)
	if err != nil {
		slog.WarnContext(ctx, "searching for neighbours failed", "err", err)
		return out
	}
	var near []*Memory
	var scores []float64
	for _, h := range hits {
		if h.Score < relatedSimilarity || h.ID == corrects {
			continue
		}
		n, err := s.store.Get(ctx, m.OrgID, h.ID)
		if err != nil || !n.Status.Active() {
			continue
		}
		near, scores = append(near, n), append(scores, h.Score)
	}
	if m.Subject == "" && s.extractor != nil {
		out.contradicts, out.degraded = s.contradictions(ctx, m, near, scores, out.degraded)
	}
	for _, n := range near {
		if len(out.related) == maxRelated {
			break
		}
		if !slices.Contains(out.contradicts, n.ID) {
			out.related = append(out.related, n.ID)
		}
	}
	return out
}

func (s *Service) contradictions(ctx context.Context, m *Memory, near []*Memory, scores []float64, degraded []string) ([]string, []string) {
	ctx, cancel := context.WithTimeout(ctx, s.budgets.Contradiction)
	defer cancel()
	var out []string
	for i, n := range near {
		if i == maxContradictChecks || scores[i] <= contradictSimilarity {
			break
		}
		yes, err := s.extractor.Contradicts(ctx, n.Content, m.Content)
		if err != nil {
			slog.WarnContext(ctx, "the contradiction check was skipped", "err", err)
			return out, append(degraded, DegradedContradiction)
		}
		if yes {
			out = append(out, n.ID)
		}
	}
	return out, degraded
}

// targetScope picks the scope a new memory goes to. A correction stays in the
// scope of the memory it corrects, so it cannot hide a shared memory from
// people who cannot read the new one.
func (s *Service) targetScope(ctx context.Context, p Principal, r RememberRequest) (Scope, error) {
	if r.Corrects == "" {
		return writableScope(p, r.Scope, r.Project, r.Team)
	}
	old, err := s.readable(ctx, s.store, p, r.Corrects, "corrects")
	if err != nil {
		return Scope{}, err
	}
	if r.Scope == "" && r.Project == "" && r.Team == "" {
		return writableScope(p, old.Scope, "", "")
	}
	sc, err := writableScope(p, r.Scope, r.Project, r.Team)
	if err != nil {
		return Scope{}, err
	}
	if sc.String() != old.Scope {
		return Scope{}, Errorf(CodeInvalidArgument, "corrects", "a correction must be in the same scope as the memory it corrects, %s", old.Scope)
	}
	return sc, nil
}

func (s *Service) correctable(ctx context.Context, st Store, p Principal, id string) (*Memory, error) {
	old, err := s.readable(ctx, st, p, id, "corrects")
	if err != nil {
		return nil, err
	}
	sc, err := ParseScope(old.Scope)
	if err != nil {
		return nil, err
	}
	if !p.CanWrite(sc) {
		return nil, Errorf(CodePermissionDenied, "corrects", "you cannot change memories in %s", sc)
	}
	if !old.Status.Active() {
		return nil, Errorf(CodeConflict, "corrects", "memory %s is already %s", old.ID, old.Status)
	}
	return old, nil
}

func (s *Service) supersede(ctx context.Context, st Store, p Principal, old, by *Memory) error {
	now := s.clock()
	old.Status = StatusSuperseded
	old.SupersededBy = by.ID
	old.SupersededAt = now
	old.UpdatedAt = now
	if err := st.Update(ctx, old); err != nil {
		return err
	}
	if err := st.AddEdge(ctx, Edge{OrgID: p.OrgID, From: by.ID, To: old.ID, Type: EdgeSupersedes, CreatedBy: p.Subject, CreatedAt: now}); err != nil {
		return err
	}
	return s.event(ctx, st, p, old.ID, "superseded", map[string]any{"by": by.ID})
}

func (s *Service) dispute(ctx context.Context, st Store, p Principal, m *Memory) ([]string, error) {
	others, err := st.FindBySubject(ctx, p.OrgID, m.Scope, m.Subject)
	if err != nil {
		return nil, err
	}
	var conflicts []string
	for _, o := range others {
		if o.ID == m.ID || o.ContentHash == m.ContentHash {
			continue
		}
		if err := s.markDisputed(ctx, st, p, m, o); err != nil {
			return nil, err
		}
		conflicts = append(conflicts, o.ID)
	}
	return conflicts, nil
}

// markDisputed records that m contradicts o: o becomes disputed and gets a
// contradicts edge from m.
func (s *Service) markDisputed(ctx context.Context, st Store, p Principal, m, o *Memory) error {
	now := s.clock()
	if o.Status != StatusDisputed {
		o.Status = StatusDisputed
		o.UpdatedAt = now
		if err := st.Update(ctx, o); err != nil {
			return err
		}
	}
	if err := st.AddEdge(ctx, Edge{OrgID: p.OrgID, From: m.ID, To: o.ID, Type: EdgeContradicts, CreatedBy: p.Subject, CreatedAt: now}); err != nil {
		return err
	}
	return s.event(ctx, st, p, o.ID, "disputed", map[string]any{"by": m.ID})
}
