package memory_test

import (
	"testing"
	"time"

	"github.com/raven-clown/logbook/memory"
)

func (f *fixture) recall(t *testing.T, p memory.Principal, r memory.RecallRequest) []memory.Recalled {
	t.Helper()
	res, err := f.svc.Recall(background, p, r)
	if err != nil {
		t.Fatalf("Recall(%q): %v", r.Query, err)
	}
	return res.Results
}

func (f *fixture) verdict(t *testing.T, id string, by memory.Author, v memory.Verdict) {
	t.Helper()
	m := f.get(t, id)
	if err := memory.ApplyVerdict(m, &memory.Verification{Verifier: by, Verdict: v}, f.now, 0); err != nil {
		t.Fatal(err)
	}
	if err := f.store.Update(background, m); err != nil {
		t.Fatal(err)
	}
}

func resultIDs(rs []memory.Recalled) []string {
	out := make([]string, len(rs))
	for i, r := range rs {
		out[i] = r.ID
	}
	return out
}

func TestRecallValidation(t *testing.T) {
	f := newFixture(t)
	tests := []struct {
		name  string
		r     memory.RecallRequest
		field string
	}{
		{"empty query", memory.RecallRequest{Query: "  "}, "query"},
		{"negative limit", memory.RecallRequest{Query: "x", Limit: -1}, "limit"},
		{"expand two", memory.RecallRequest{Query: "x", Expand: 2}, "expand"},
		{"bad min status", memory.RecallRequest{Query: "x", MinStatus: memory.StatusSuperseded}, "min_status"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := f.svc.Recall(background, alice, tt.r)
			wantCode(t, err, memory.CodeInvalidArgument, tt.field)
		})
	}
}

func TestRecallLimitCap(t *testing.T) {
	f := newFixture(t)
	for i := range 60 {
		f.remember(t, alice, memory.RememberRequest{Content: "deploy note number " + string(rune('A'+i%26)) + string(rune('a'+i/26))})
	}
	if got := f.recall(t, alice, memory.RecallRequest{Query: "deploy", Limit: 100}); len(got) != 50 {
		t.Fatalf("limit 100 returned %d, want 50", len(got))
	}
	if got := f.recall(t, alice, memory.RecallRequest{Query: "deploy"}); len(got) != 8 {
		t.Fatalf("default limit returned %d, want 8", len(got))
	}
}

func TestRecallCorrectionChain(t *testing.T) {
	f := newFixture(t)
	p := inProject(alice, "p")
	a := f.remember(t, p, memory.RememberRequest{Content: "deploys go through Jenkins"})
	f.advance(time.Hour)
	b := f.remember(t, p, memory.RememberRequest{Content: "deploys go through GitHub Actions", Corrects: a.ID})
	f.advance(time.Hour)
	c := f.remember(t, p, memory.RememberRequest{Content: "deploys go through Argo CD", Corrects: b.ID})

	got := f.recall(t, p, memory.RecallRequest{Query: "Jenkins"})
	if len(got) != 1 || got[0].ID != c.ID || got[0].CorrectedFrom == nil || got[0].CorrectedFrom.ID != a.ID ||
		got[0].CorrectedFrom.Content != "deploys go through Jenkins" || !got[0].CorrectedFrom.At.Equal(day0.Add(time.Hour)) {
		t.Fatalf("recall Jenkins = %+v", got)
	}
	got = f.recall(t, p, memory.RecallRequest{Query: "Argo"})
	if len(got) != 1 || got[0].ID != c.ID || got[0].CorrectedFrom != nil {
		t.Fatalf("recall Argo = %+v", got)
	}
	got = f.recall(t, p, memory.RecallRequest{Query: "deploys"})
	if len(got) != 1 || got[0].ID != c.ID || got[0].CorrectedFrom != nil {
		t.Fatalf("recall deploys = %+v, want only the current memory", got)
	}
}

func TestRecallTrustOrder(t *testing.T) {
	f := newFixture(t)
	p := inProject(alice, "p")
	plain := f.remember(t, p, memory.RememberRequest{Content: "the cache lives in redis one"})
	confirmed := f.remember(t, p, memory.RememberRequest{Content: "the cache lives in redis two"})
	disputed := f.remember(t, p, memory.RememberRequest{Content: "the cache lives in redis three"})
	f.verdict(t, confirmed.ID, bob.Author(), memory.VerdictConfirm)
	f.verdict(t, disputed.ID, bob.Author(), memory.VerdictDispute)
	got := resultIDs(f.recall(t, p, memory.RecallRequest{Query: "cache redis"}))
	want := []string{confirmed.ID, plain.ID, disputed.ID}
	if len(got) != 3 || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Fatalf("order = %v, want %v", got, want)
	}
	got = resultIDs(f.recall(t, p, memory.RecallRequest{Query: "cache redis", MinStatus: memory.StatusVerified}))
	if len(got) != 1 || got[0] != confirmed.ID {
		t.Fatalf("verified only = %v", got)
	}
}

func TestRecallStale(t *testing.T) {
	f := newFixture(t)
	old := f.remember(t, alice, memory.RememberRequest{Content: "the build queue is slow", Kind: memory.KindObservation})
	f.advance(31 * 24 * time.Hour)
	fresh := f.remember(t, alice, memory.RememberRequest{Content: "the build queue is slow again", Kind: memory.KindObservation})
	got := f.recall(t, alice, memory.RecallRequest{Query: "build queue slow"})
	if len(got) != 2 || got[0].ID != fresh.ID || got[1].ID != old.ID || !got[1].Stale || got[1].StaleReason != "review_due" || got[0].Stale {
		t.Fatalf("recall = %+v", got)
	}
	got = f.recall(t, alice, memory.RecallRequest{Query: "build queue slow", ExcludeStale: true})
	if len(got) != 1 || got[0].ID != fresh.ID {
		t.Fatalf("exclude stale = %v", resultIDs(got))
	}
}

func TestRecallScopes(t *testing.T) {
	f := newFixture(t)
	sales := memory.Principal{OrgID: memory.DefaultOrg, Subject: "sam", Teams: []string{"sales"}, Channel: memory.ChannelMCP}
	f.remember(t, sales, memory.RememberRequest{Content: "the quota is ten deals", Scope: "team", Team: "sales"})
	mine := f.remember(t, alice, memory.RememberRequest{Content: "my quota note"})
	f.remember(t, inProject(alice, "p"), memory.RememberRequest{Content: "project quota note"})

	if got := resultIDs(f.recall(t, inProject(alice, "p"), memory.RecallRequest{Query: "quota"})); len(got) != 2 {
		t.Fatalf("alice sees %v, want her two", got)
	}
	_, err := f.svc.Recall(background, alice, memory.RecallRequest{Query: "quota", Scopes: []string{"team:sales"}})
	wantCode(t, err, memory.CodePermissionDenied, "scopes")
	got := resultIDs(f.recall(t, inProject(alice, "p"), memory.RecallRequest{Query: "quota", Scopes: []string{"user"}}))
	if len(got) != 1 || got[0] != mine.ID {
		t.Fatalf("user scope only = %v", got)
	}
	_, err = f.svc.Recall(background, alice, memory.RecallRequest{Query: "quota", Scopes: []string{"galaxy"}})
	wantCode(t, err, memory.CodeInvalidArgument, "scopes")
}

func TestRecallAsOf(t *testing.T) {
	f := newFixture(t)
	a := f.remember(t, alice, memory.RememberRequest{Content: "the release train leaves on monday"})
	f.advance(4 * 24 * time.Hour)
	b := f.remember(t, alice, memory.RememberRequest{Content: "the release train leaves on tuesday", Corrects: a.ID})
	day3 := day0.Add(3 * 24 * time.Hour)
	if got := resultIDs(f.recall(t, alice, memory.RecallRequest{Query: "release train", AsOf: day3})); len(got) != 1 || got[0] != a.ID {
		t.Fatalf("as of day 3 = %v", got)
	}
	day6 := day0.Add(6 * 24 * time.Hour)
	if got := resultIDs(f.recall(t, alice, memory.RecallRequest{Query: "release train", AsOf: day6})); len(got) != 1 || got[0] != b.ID {
		t.Fatalf("as of day 6 = %v", got)
	}
}

func TestRecallExpand(t *testing.T) {
	f := newFixture(t)
	p := inProject(alice, "p")
	a := f.remember(t, p, memory.RememberRequest{Content: "CI runs on Go 1.25", Subject: "ci.go-version"})
	b := f.remember(t, p, memory.RememberRequest{Content: "CI runs on Go 1.26", Subject: "ci.go-version"})
	got := f.recall(t, p, memory.RecallRequest{Query: "1.26", Expand: 1})
	if len(got) != 1 || got[0].ID != b.ID || len(got[0].Links) != 1 || got[0].Links[0].To != a.ID || got[0].Links[0].Type != memory.EdgeContradicts {
		t.Fatalf("expand = %+v", got)
	}
	if got := f.recall(t, p, memory.RecallRequest{Query: "1.26"}); len(got[0].Links) != 0 {
		t.Fatalf("links without expand = %+v", got[0].Links)
	}
}

func TestRecallExpandHidesUnreadableEnds(t *testing.T) {
	f := newFixture(t)
	shared := f.remember(t, inProject(alice, "p"), memory.RememberRequest{Content: "the deploy window is friday"})
	f.remember(t, alice, memory.RememberRequest{Content: "my private note about the window", RelatedTo: []string{shared.ID}})
	got := f.recall(t, inProject(bob, "p"), memory.RecallRequest{Query: "deploy window", Expand: 1})
	if len(got) != 1 || got[0].ID != shared.ID {
		t.Fatalf("bob recall = %v", resultIDs(got))
	}
	if len(got[0].Links) != 0 {
		t.Fatalf("bob sees links into alice's private scope: %+v", got[0].Links)
	}
	mine := f.recall(t, inProject(alice, "p"), memory.RecallRequest{Query: "deploy window", Expand: 1, Scopes: []string{"project"}})
	if len(mine) != 1 || len(mine[0].Links) != 1 {
		t.Fatalf("alice should still see her own link: %+v", mine)
	}
}
