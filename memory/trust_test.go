package memory_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/raven-clown/logbook/memory"
)

var day0 = time.Date(2026, 11, 4, 0, 0, 0, 0, time.UTC)

const halfYear = 180 * 24 * time.Hour

func agentWrote(status memory.Status) *memory.Memory {
	return &memory.Memory{
		ID:     "mem_1",
		Status: status,
		Author: memory.Author{Subject: "alice", Channel: memory.ChannelMCP},
	}
}

func TestApplyVerdict(t *testing.T) {
	tests := []struct {
		name              string
		start             memory.Status
		verifier          memory.Author
		verdict           memory.Verdict
		period            time.Duration
		wantStatus        memory.Status
		wantCounted       bool
		wantConfirmations int
		wantDisputes      int
		wantReviewAfter   time.Time
	}{
		{"same subject other channel counts", memory.StatusUnverified, memory.Author{Subject: "alice", Channel: memory.ChannelCLI}, memory.VerdictConfirm, halfYear, memory.StatusVerified, true, 1, 0, time.Date(2027, 5, 3, 0, 0, 0, 0, time.UTC)},
		{"same subject same channel does not count", memory.StatusUnverified, memory.Author{Subject: "alice", Channel: memory.ChannelMCP}, memory.VerdictConfirm, halfYear, memory.StatusUnverified, false, 0, 0, time.Time{}},
		{"other subject same channel counts", memory.StatusUnverified, memory.Author{Subject: "bob", Channel: memory.ChannelMCP}, memory.VerdictConfirm, halfYear, memory.StatusVerified, true, 1, 0, day0.Add(halfYear)},
		{"author can dispute", memory.StatusUnverified, memory.Author{Subject: "alice", Channel: memory.ChannelMCP}, memory.VerdictDispute, halfYear, memory.StatusDisputed, true, 0, 1, time.Time{}},
		{"confirm after dispute", memory.StatusDisputed, memory.Author{Subject: "bob", Channel: memory.ChannelCLI}, memory.VerdictConfirm, halfYear, memory.StatusVerified, true, 1, 0, day0.Add(halfYear)},
		{"no period leaves review open", memory.StatusUnverified, memory.Author{Subject: "bob", Channel: memory.ChannelCLI}, memory.VerdictConfirm, 0, memory.StatusVerified, true, 1, 0, time.Time{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := agentWrote(tt.start)
			v := &memory.Verification{Verifier: tt.verifier, Verdict: tt.verdict}
			if err := memory.ApplyVerdict(m, v, day0, tt.period); err != nil {
				t.Fatal(err)
			}
			if m.Status != tt.wantStatus || v.Counted != tt.wantCounted || m.Confirmations != tt.wantConfirmations || m.Disputes != tt.wantDisputes {
				t.Fatalf("got status %s counted %v confirmations %d disputes %d", m.Status, v.Counted, m.Confirmations, m.Disputes)
			}
			if !m.ReviewAfter.Equal(tt.wantReviewAfter) {
				t.Fatalf("ReviewAfter = %v, want %v", m.ReviewAfter, tt.wantReviewAfter)
			}
			if tt.wantCounted && tt.verdict == memory.VerdictConfirm && !m.LastConfirmedAt.Equal(day0) {
				t.Fatalf("LastConfirmedAt = %v, want %v", m.LastConfirmedAt, day0)
			}
		})
	}
}

func TestApplyVerdictOnInactive(t *testing.T) {
	for _, st := range []memory.Status{memory.StatusSuperseded, memory.StatusForgotten} {
		for _, vd := range []memory.Verdict{memory.VerdictConfirm, memory.VerdictDispute} {
			m := agentWrote(st)
			before := *m
			err := memory.ApplyVerdict(m, &memory.Verification{Verifier: memory.Author{Subject: "bob"}, Verdict: vd}, day0, halfYear)
			if memory.CodeOf(err) != memory.CodeConflict {
				t.Fatalf("%s %s: code %q, want conflict", st, vd, memory.CodeOf(err))
			}
			if !reflect.DeepEqual(*m, before) {
				t.Fatalf("%s %s: memory changed", st, vd)
			}
		}
	}
}

func TestApplyVerdictUnknown(t *testing.T) {
	err := memory.ApplyVerdict(agentWrote(memory.StatusUnverified), &memory.Verification{Verdict: "maybe"}, day0, halfYear)
	var e *memory.Error
	if !errors.As(err, &e) || e.Field != "verdict" {
		t.Fatalf("error = %v, want invalid verdict field", err)
	}
}

func TestReviewAfter(t *testing.T) {
	want := map[memory.Kind]time.Duration{
		memory.KindObservation: 720 * time.Hour,
		memory.KindFact:        4320 * time.Hour,
		memory.KindProcedure:   4320 * time.Hour,
		memory.KindDecision:    8760 * time.Hour,
	}
	for k, d := range want {
		if got := memory.ReviewAfter(day0, k, memory.DefaultReviewPeriods); !got.Equal(day0.Add(d)) {
			t.Errorf("%s: %v, want %v", k, got, day0.Add(d))
		}
	}
	if got := memory.ReviewAfter(day0, memory.KindPreference, memory.DefaultReviewPeriods); !got.IsZero() {
		t.Errorf("preference: %v, want zero", got)
	}
}

type fixedRule struct {
	name  string
	stale bool
	err   error
}

func (r fixedRule) Name() string { return r.name }

func (r fixedRule) Check(context.Context, *memory.Memory, time.Time) (bool, string, error) {
	return r.stale, r.name, r.err
}

func TestCheckStale(t *testing.T) {
	ctx := context.Background()
	m := agentWrote(memory.StatusVerified)
	if stale, _, err := memory.CheckStale(ctx, nil, m, day0); stale || err != nil {
		t.Fatalf("no rules: %v %v", stale, err)
	}
	stale, reason, err := memory.CheckStale(ctx, []memory.Rule{fixedRule{name: "a"}, fixedRule{name: "b", stale: true}}, m, day0)
	if !stale || reason != "b" || err != nil {
		t.Fatalf("got %v %q %v, want stale by b", stale, reason, err)
	}
	boom := errors.New("boom")
	if _, _, err := memory.CheckStale(ctx, []memory.Rule{fixedRule{name: "a", err: boom}}, m, day0); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want boom", err)
	}
}
