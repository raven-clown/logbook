// Package staleness holds the built-in rules that mark a memory stale.
package staleness

import (
	"context"
	"time"

	"github.com/raven-clown/logbook/memory"
)

// Default returns the rules logbook runs when none are configured.
func Default() []memory.Rule {
	return []memory.Rule{ReviewDue()}
}

// ReviewDue marks a memory stale once its review date has come.
func ReviewDue() memory.Rule {
	return reviewDue{}
}

type reviewDue struct{}

func (reviewDue) Name() string { return "review_due" }

func (reviewDue) Check(_ context.Context, m *memory.Memory, now time.Time) (bool, string, error) {
	if m.ReviewAfter.IsZero() || now.Before(m.ReviewAfter) {
		return false, "", nil
	}
	return true, "review_due", nil
}
