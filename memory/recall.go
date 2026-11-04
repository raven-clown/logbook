package memory

import (
	"cmp"
	"context"
	"slices"
	"strings"
	"time"
)

// Recall limits and ranking constants from the design.
const (
	DefaultRecallLimit = 8
	MaxRecallLimit     = 50
	rrfK               = 60
	searchCandidates   = 50
	maxCorrectionChain = 10
)

// RecallRequest is a search over the memories the caller can read.
type RecallRequest struct {
	Query string
	// Scopes are "user", "project", "team" or full scope strings. Empty means every readable scope.
	Scopes  []string
	Project string
	Team    string
	Kinds   []Kind
	Tags    []string
	Limit   int
	// MinStatus is "" or unverified for any active memory, or verified for verified ones only.
	MinStatus    Status
	ExcludeStale bool
	// Expand 1 attaches each result's edges.
	Expand int
	// AsOf returns what was current at that time instead of now.
	AsOf time.Time
}

// Correction says which older memory a result replaced.
type Correction struct {
	ID      string    `json:"id"`
	Content string    `json:"content"`
	At      time.Time `json:"at"`
}

// Recalled is one recall result.
type Recalled struct {
	Memory
	Stale         bool        `json:"stale"`
	StaleReason   string      `json:"stale_reason,omitempty"`
	Score         float64     `json:"score"`
	CorrectedFrom *Correction `json:"corrected_from,omitempty"`
	Links         []Edge      `json:"links,omitempty"`
}

// RecallResult is the result of Recall.
type RecallResult struct {
	Results  []Recalled `json:"results"`
	Degraded []string   `json:"degraded,omitempty"`
}

type candidate struct {
	m         *Memory
	score     float64
	direct    bool
	corrected *Correction
}

// Recall searches memories and ranks them by relevance and trust. A match on a
// superseded memory returns the memory that replaced it, with CorrectedFrom set.
func (s *Service) Recall(ctx context.Context, p Principal, r RecallRequest) (*RecallResult, error) {
	query := strings.TrimSpace(r.Query)
	if query == "" {
		return nil, Errorf(CodeInvalidArgument, "query", "query is empty")
	}
	limit, err := recallLimit(r.Limit)
	if err != nil {
		return nil, err
	}
	if r.Expand != 0 && r.Expand != 1 {
		return nil, Errorf(CodeInvalidArgument, "expand", "expand must be 0 or 1")
	}
	if r.MinStatus != "" && r.MinStatus != StatusUnverified && r.MinStatus != StatusVerified {
		return nil, Errorf(CodeInvalidArgument, "min_status", "min_status must be unverified or verified")
	}
	scopes, err := requestedScopes(p, r.Scopes, r.Project, r.Team)
	if err != nil {
		return nil, err
	}
	f := Filter{OrgID: p.OrgID, Scopes: scopes, Kinds: r.Kinds, Tags: r.Tags}
	if r.AsOf.IsZero() {
		f.Statuses = []Status{StatusUnverified, StatusVerified, StatusDisputed, StatusSuperseded}
	} else {
		f.AsOf = r.AsOf.UTC()
	}
	hits, err := s.store.SearchText(ctx, query, f, searchCandidates)
	if err != nil {
		return nil, err
	}
	cands, err := s.candidates(ctx, p, Fuse(rrfK, hits), r.AsOf.IsZero())
	if err != nil {
		return nil, err
	}
	results, err := s.rank(ctx, cands, r, limit)
	if err != nil {
		return nil, err
	}
	if r.Expand == 1 {
		if err := s.attachLinks(ctx, p, results); err != nil {
			return nil, err
		}
	}
	return &RecallResult{Results: results}, nil
}

func recallLimit(n int) (int, error) {
	switch {
	case n < 0:
		return 0, Errorf(CodeInvalidArgument, "limit", "limit must not be negative")
	case n == 0:
		return DefaultRecallLimit, nil
	case n > MaxRecallLimit:
		return MaxRecallLimit, nil
	}
	return n, nil
}

func requestedScopes(p Principal, requested []string, project, team string) ([]string, error) {
	readable := p.ReadableScopes(project, team)
	if len(requested) == 0 {
		return readable, nil
	}
	var out []string
	add := func(sc string) {
		if !slices.Contains(out, sc) {
			out = append(out, sc)
		}
	}
	for _, req := range requested {
		switch {
		case req == string(ScopeUser) || req == string(ScopeProject) || req == string(ScopeTeam):
			for _, sc := range readable {
				if strings.HasPrefix(sc, req+":") {
					add(sc)
				}
			}
		case strings.Contains(req, ":"):
			sc, err := ParseScope(req)
			if err != nil {
				return nil, Errorf(CodeInvalidArgument, "scopes", "%s", err.Error())
			}
			if !p.CanRead(sc) {
				return nil, Errorf(CodePermissionDenied, "scopes", "you cannot read %s", sc)
			}
			add(sc.String())
		default:
			return nil, Errorf(CodeInvalidArgument, "scopes", "scope must be user, project, team or a full scope, not %q", req)
		}
	}
	return out, nil
}

func (s *Service) candidates(ctx context.Context, p Principal, hits []Hit, swap bool) ([]*candidate, error) {
	byID := map[string]*candidate{}
	var order []string
	for _, h := range hits {
		m, err := s.store.Get(ctx, p.OrgID, h.ID)
		if err != nil {
			return nil, err
		}
		c := &candidate{m: m, score: h.Score, direct: true}
		if swap && m.Status == StatusSuperseded {
			cur, err := s.current(ctx, p, m)
			if err != nil {
				return nil, err
			}
			if cur == nil {
				continue
			}
			c = &candidate{m: cur, score: h.Score, corrected: &Correction{ID: m.ID, Content: m.Content, At: m.SupersededAt}}
		}
		prev, ok := byID[c.m.ID]
		if !ok {
			byID[c.m.ID] = c
			order = append(order, c.m.ID)
			continue
		}
		switch {
		case c.direct && !prev.direct:
			prev.direct, prev.corrected = true, nil
		case !c.direct && !prev.direct && c.score > prev.score:
			prev.corrected = c.corrected
		}
		prev.score = max(prev.score, c.score)
	}
	out := make([]*candidate, 0, len(order))
	for _, id := range order {
		out = append(out, byID[id])
	}
	return out, nil
}

// current follows a supersede chain to the memory in force now, or nil if
// the chain ends somewhere the principal cannot read.
func (s *Service) current(ctx context.Context, p Principal, m *Memory) (*Memory, error) {
	for range maxCorrectionChain {
		if m.SupersededBy == "" {
			return nil, nil
		}
		next, err := s.store.Get(ctx, p.OrgID, m.SupersededBy)
		if CodeOf(err) == CodeNotFound {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		sc, err := ParseScope(next.Scope)
		if err != nil {
			return nil, err
		}
		if !p.CanRead(sc) {
			return nil, nil
		}
		if next.Status.Active() {
			return next, nil
		}
		m = next
	}
	return nil, nil
}

func (s *Service) rank(ctx context.Context, cands []*candidate, r RecallRequest, limit int) ([]Recalled, error) {
	now := s.clock()
	if !r.AsOf.IsZero() {
		now = r.AsOf.UTC()
	}
	var out []Recalled
	for _, c := range cands {
		stale, reason, err := CheckStale(ctx, s.rules, c.m, now)
		if err != nil {
			return nil, err
		}
		if stale && r.ExcludeStale {
			continue
		}
		if r.MinStatus == StatusVerified && c.m.Status != StatusVerified {
			continue
		}
		score := c.score * s.weight(c.m.Status)
		if stale {
			score *= s.weights.Stale
		}
		out = append(out, Recalled{Memory: *c.m, Stale: stale, StaleReason: reason, Score: score, CorrectedFrom: c.corrected})
	}
	slices.SortStableFunc(out, func(a, b Recalled) int {
		if c := cmp.Compare(b.Score, a.Score); c != 0 {
			return c
		}
		return b.CreatedAt.Compare(a.CreatedAt)
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (s *Service) attachLinks(ctx context.Context, p Principal, results []Recalled) error {
	ids := make([]string, len(results))
	for i, r := range results {
		ids[i] = r.ID
	}
	edges, err := s.store.Edges(ctx, p.OrgID, ids)
	if err != nil {
		return err
	}
	visible := map[string]bool{}
	for _, r := range results {
		visible[r.ID] = true
	}
	for i := range results {
		for _, e := range edges {
			if e.From != results[i].ID && e.To != results[i].ID {
				continue
			}
			other := e.To
			if other == results[i].ID {
				other = e.From
			}
			ok, err := s.canSee(ctx, p, other, visible)
			if err != nil {
				return err
			}
			if ok {
				results[i].Links = append(results[i].Links, e)
			}
		}
	}
	return nil
}

// canSee reports whether the principal may read memory id, caching answers in seen.
func (s *Service) canSee(ctx context.Context, p Principal, id string, seen map[string]bool) (bool, error) {
	if ok, done := seen[id]; done {
		return ok, nil
	}
	_, err := s.readable(ctx, s.store, p, id, "")
	switch CodeOf(err) {
	case "":
		seen[id] = true
	case CodeNotFound:
		seen[id] = false
	default:
		return false, err
	}
	return seen[id], nil
}
