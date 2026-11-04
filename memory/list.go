package memory

import "context"

// List limits from the design.
const (
	DefaultListLimit = 50
	MaxListLimit     = 500
)

// ListRequest browses memories. Scopes work as in RecallRequest.
type ListRequest struct {
	Scopes   []string
	Project  string
	Team     string
	Statuses []Status
	Kind     Kind
	Subject  string
	Tag      string
	Author   string
	Stale    bool
	Order    Order
	Limit    int
	Cursor   string
}

// ListResult is one page of memories.
type ListResult struct {
	Items      []*Memory `json:"items"`
	NextCursor string    `json:"next_cursor,omitempty"`
}

// List returns one page of memories. Order review puts disputed first, then
// stale, then unverified: that is the review queue. The queue and the stale
// filter show only active memories unless statuses are given.
func (s *Service) List(ctx context.Context, p Principal, r ListRequest) (*ListResult, error) {
	limit := r.Limit
	switch {
	case limit < 0:
		return nil, Errorf(CodeInvalidArgument, "limit", "limit must not be negative")
	case limit == 0:
		limit = DefaultListLimit
	case limit > MaxListLimit:
		limit = MaxListLimit
	}
	order := r.Order
	switch order {
	case "":
		order = OrderNewest
	case OrderNewest, OrderReview:
	default:
		return nil, Errorf(CodeInvalidArgument, "order", "order must be newest or review")
	}
	scopes, err := requestedScopes(p, r.Scopes, r.Project, r.Team)
	if err != nil {
		return nil, err
	}
	now := s.clock()
	f := Filter{
		OrgID: p.OrgID, Scopes: scopes, Statuses: r.Statuses, Subject: r.Subject, Author: r.Author,
		Order: order, Now: now, Limit: limit, Cursor: r.Cursor,
	}
	if r.Kind != "" {
		f.Kinds = []Kind{r.Kind}
	}
	if r.Tag != "" {
		f.Tags = []string{r.Tag}
	}
	if r.Stale {
		f.StaleAt = now
	}
	if len(f.Statuses) == 0 && (order == OrderReview || r.Stale) {
		f.Statuses = []Status{StatusUnverified, StatusVerified, StatusDisputed}
	}
	items, next, err := s.store.List(ctx, f)
	if err != nil {
		return nil, err
	}
	if items == nil {
		items = []*Memory{}
	}
	return &ListResult{Items: items, NextCursor: next}, nil
}

// Get returns one memory. A memory the caller cannot read is reported as not found.
func (s *Service) Get(ctx context.Context, p Principal, id string) (*Memory, error) {
	return s.readable(ctx, s.store, p, id, "id")
}
