package wire_test

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/raven-clown/logbook/internal/wire"
	"github.com/raven-clown/logbook/memory"
)

func TestToRequestTimes(t *testing.T) {
	_, err := wire.Remember{Content: "x", ValidUntil: "tomorrow"}.ToRequest()
	var e *memory.Error
	if !errors.As(err, &e) || e.Code != memory.CodeInvalidArgument || e.Field != "valid_until" {
		t.Fatalf("bad valid_until: %v", err)
	}
	_, err = wire.Recall{Query: "x", AsOf: "2026-13-01"}.ToRequest()
	if !errors.As(err, &e) || e.Field != "as_of" {
		t.Fatalf("bad as_of: %v", err)
	}
	r, err := wire.Remember{Content: "x", ValidUntil: "2026-12-01T00:00:00+07:00"}.ToRequest()
	if err != nil || !r.ValidUntil.Equal(time.Date(2026, 11, 30, 17, 0, 0, 0, time.UTC)) {
		t.Fatalf("valid_until = %v, %v", r.ValidUntil, err)
	}
}

func TestToRequest(t *testing.T) {
	no := false
	rc, err := wire.Recall{Query: "q", Kinds: []string{"fact"}, MinStatus: "verified", IncludeStale: &no, Expand: 1, Limit: 3}.ToRequest()
	if err != nil {
		t.Fatal(err)
	}
	want := memory.RecallRequest{Query: "q", Kinds: []memory.Kind{memory.KindFact}, MinStatus: memory.StatusVerified, ExcludeStale: true, Expand: 1, Limit: 3}
	if !reflect.DeepEqual(rc, want) {
		t.Fatalf("recall = %+v", rc)
	}
	if rc, _ := (wire.Recall{Query: "q"}).ToRequest(); rc.ExcludeStale {
		t.Fatal("include_stale defaults to true")
	}
	l := wire.List{Status: []string{"disputed"}, Kind: "decision", Order: "review", Stale: true, Cursor: "c"}.ToRequest()
	if l.Statuses[0] != memory.StatusDisputed || l.Kind != memory.KindDecision || l.Order != memory.OrderReview || !l.Stale || l.Cursor != "c" {
		t.Fatalf("list = %+v", l)
	}
	v := wire.Verify{ID: "mem_1", Verdict: "dispute", Note: "n"}.ToRequest()
	f := wire.Forget{ID: "mem_1", Reason: "r", Purge: true}.ToRequest()
	if v.Verdict != memory.VerdictDispute || v.ID != "mem_1" || !f.Purge || f.Reason != "r" {
		t.Fatalf("verify %+v forget %+v", v, f)
	}
}
