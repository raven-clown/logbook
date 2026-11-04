package staleness_test

import (
	"context"
	"testing"
	"time"

	"github.com/raven-clown/logbook/memory"
	"github.com/raven-clown/logbook/staleness"
)

func TestReviewDue(t *testing.T) {
	due := time.Date(2027, 5, 3, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		name        string
		reviewAfter time.Time
		now         time.Time
		want        bool
	}{
		{"never", time.Time{}, due, false},
		{"before", due, due.Add(-time.Second), false},
		{"exactly at", due, due, true},
		{"after", due, due.Add(time.Hour), true},
	}
	rule := staleness.ReviewDue()
	if rule.Name() != "review_due" {
		t.Fatalf("Name() = %q", rule.Name())
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stale, reason, err := rule.Check(context.Background(), &memory.Memory{ReviewAfter: tt.reviewAfter}, tt.now)
			if err != nil {
				t.Fatal(err)
			}
			if stale != tt.want {
				t.Fatalf("stale = %v, want %v", stale, tt.want)
			}
			if stale && reason != "review_due" {
				t.Fatalf("reason = %q", reason)
			}
		})
	}
}

func TestDefault(t *testing.T) {
	rules := staleness.Default()
	if len(rules) != 1 || rules[0].Name() != "review_due" {
		t.Fatalf("Default() = %v", rules)
	}
}
