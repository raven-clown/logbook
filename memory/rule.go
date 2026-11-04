package memory

import (
	"context"
	"time"
)

// Rule decides whether a memory is stale. Rules are plugins: the staleness
// package has the built-in ones.
type Rule interface {
	Name() string
	Check(ctx context.Context, m *Memory, now time.Time) (stale bool, reason string, err error)
}

// CheckStale runs rules in order and returns the first one that finds m stale.
func CheckStale(ctx context.Context, rules []Rule, m *Memory, now time.Time) (bool, string, error) {
	for _, r := range rules {
		stale, reason, err := r.Check(ctx, m, now)
		if err != nil {
			return false, "", err
		}
		if stale {
			return true, reason, nil
		}
	}
	return false, "", nil
}
