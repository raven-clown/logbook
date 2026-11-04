package memory

import (
	"context"
	"time"
)

// Order is the sort order of a list.
type Order string

// The list orders.
const (
	OrderNewest Order = "newest"
	OrderReview Order = "review"
)

// Filter selects memories. Every field narrows the result; zero values do not filter.
type Filter struct {
	OrgID string
	// Scopes are full scope strings. An empty list matches nothing.
	Scopes []string
	// Statuses empty means every status except forgotten.
	Statuses []Status
	Kinds    []Kind
	// Tags match when a memory has any of them.
	Tags    []string
	Subject string
	// Author is the author's subject.
	Author string
	// StaleAt keeps memories whose review date is set and not after it.
	StaleAt time.Time
	// AsOf keeps memories that existed and were neither superseded nor forgotten at that time.
	AsOf  time.Time
	Order Order
	// Now is the time OrderReview uses to decide what is stale.
	Now    time.Time
	Limit  int
	Cursor string
}

// Store is the storage a Service runs on. Backends implement it and prove it
// with the storetest suite.
type Store interface {
	Insert(ctx context.Context, m *Memory) error
	// Update writes m if its Version still matches the stored one, then increments
	// m.Version. A mismatch returns ErrVersionConflict.
	Update(ctx context.Context, m *Memory) error
	Get(ctx context.Context, orgID, id string) (*Memory, error)
	// FindByHash returns the non-forgotten memory with this content hash in scope.
	FindByHash(ctx context.Context, orgID, scope, hash string) (*Memory, error)
	// FindBySubject returns the active memories with this subject in scope.
	FindBySubject(ctx context.Context, orgID, scope, subject string) ([]*Memory, error)
	// List returns one page and the cursor of the next, empty on the last page.
	List(ctx context.Context, f Filter) ([]*Memory, string, error)
	// SearchText returns full-text matches, best first. Query syntax is never an error.
	SearchText(ctx context.Context, query string, f Filter, limit int) ([]Hit, error)
	// AddEdge stores e. Adding the same from, to and type again does nothing.
	AddEdge(ctx context.Context, e Edge) error
	// Edges returns every edge that starts or ends at one of ids.
	Edges(ctx context.Context, orgID string, ids []string) ([]Edge, error)
	AddVerification(ctx context.Context, v Verification) error
	Verifications(ctx context.Context, orgID, memoryID string) ([]Verification, error)
	AppendEvent(ctx context.Context, e Event) error
	// Events returns a memory's history, oldest first.
	Events(ctx context.Context, orgID, memoryID string) ([]Event, error)
	// Tx runs fn in a transaction and rolls back if fn returns an error.
	Tx(ctx context.Context, fn func(Store) error) error
	Close() error
}
