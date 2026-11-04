package memory_test

import (
	"testing"
	"time"

	"github.com/raven-clown/logbook/memory"
)

func TestVerify(t *testing.T) {
	aliceCLI := alice
	aliceCLI.Channel = memory.ChannelCLI
	tests := []struct {
		name        string
		by          memory.Principal
		verdict     memory.Verdict
		wantStatus  memory.Status
		wantCounted bool
		wantConfirm int
		wantDispute int
	}{
		{"same subject and channel", inProject(alice, "p"), memory.VerdictConfirm, memory.StatusUnverified, false, 0, 0},
		{"same subject other channel", inProject(aliceCLI, "p"), memory.VerdictConfirm, memory.StatusVerified, true, 1, 0},
		{"another agent disputes", inProject(bob, "p"), memory.VerdictDispute, memory.StatusDisputed, true, 0, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			m := f.remember(t, inProject(alice, "p"), memory.RememberRequest{Content: "staging runs postgres 17"})
			res, err := f.svc.Verify(background, tt.by, memory.VerifyRequest{ID: m.ID, Verdict: tt.verdict, Note: "checked"})
			if err != nil {
				t.Fatal(err)
			}
			if res.ID != m.ID || res.Status != tt.wantStatus || res.Counted != tt.wantCounted || res.Confirmations != tt.wantConfirm || res.Disputes != tt.wantDispute {
				t.Fatalf("result = %+v", res)
			}
			stored := f.get(t, m.ID)
			if stored.Status != tt.wantStatus || stored.Version != 2 {
				t.Fatalf("stored = %s v%d", stored.Status, stored.Version)
			}
			vs, _ := f.store.Verifications(background, memory.DefaultOrg, m.ID)
			if len(vs) != 1 || vs[0].Note != "checked" || vs[0].Counted != tt.wantCounted || vs[0].Verifier != tt.by.Author() {
				t.Fatalf("verifications = %+v", vs)
			}
			events, _ := f.store.Events(background, memory.DefaultOrg, m.ID)
			wantAction := map[memory.Verdict]string{memory.VerdictConfirm: "confirmed", memory.VerdictDispute: "disputed"}[tt.verdict]
			if len(events) != 2 || events[1].Action != wantAction {
				t.Fatalf("events = %+v", events)
			}
		})
	}
}

func TestVerifyErrors(t *testing.T) {
	f := newFixture(t)
	p := inProject(alice, "p")
	a := f.remember(t, p, memory.RememberRequest{Content: "old fact"})
	f.remember(t, p, memory.RememberRequest{Content: "new fact", Corrects: a.ID})
	_, err := f.svc.Verify(background, inProject(bob, "p"), memory.VerifyRequest{ID: a.ID, Verdict: memory.VerdictConfirm})
	wantCode(t, err, memory.CodeConflict, "")

	b := f.remember(t, p, memory.RememberRequest{Content: "another fact"})
	_, err = f.svc.Verify(background, inProject(bob, "p"), memory.VerifyRequest{ID: b.ID, Verdict: "maybe"})
	wantCode(t, err, memory.CodeInvalidArgument, "verdict")

	_, err = f.svc.Verify(background, bob, memory.VerifyRequest{ID: "mem_404", Verdict: memory.VerdictConfirm})
	wantCode(t, err, memory.CodeNotFound, "id")

	private := f.remember(t, alice, memory.RememberRequest{Content: "alice only"})
	_, err = f.svc.Verify(background, bob, memory.VerifyRequest{ID: private.ID, Verdict: memory.VerdictConfirm})
	wantCode(t, err, memory.CodeNotFound, "id")
}

func TestVerifyResetsReview(t *testing.T) {
	f := newFixture(t)
	m := f.remember(t, alice, memory.RememberRequest{Content: "the on-call rota is in pagerduty", Kind: memory.KindFact})
	f.advance(100 * 24 * time.Hour)
	cli := alice
	cli.Channel = memory.ChannelCLI
	if _, err := f.svc.Verify(background, cli, memory.VerifyRequest{ID: m.ID, Verdict: memory.VerdictConfirm}); err != nil {
		t.Fatal(err)
	}
	got := f.get(t, m.ID)
	if !got.ReviewAfter.Equal(f.now.Add(memory.DefaultReviewPeriods[memory.KindFact])) || !got.LastConfirmedAt.Equal(f.now) {
		t.Fatalf("review_after %v last_confirmed %v", got.ReviewAfter, got.LastConfirmedAt)
	}
}
