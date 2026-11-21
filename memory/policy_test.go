package memory_test

import (
	"slices"
	"testing"

	"github.com/raven-clown/logbook/memory"
)

func verifiersOnTeams(o *memory.Options) {
	o.VerifyPolicy = map[memory.ScopeType]memory.Policy{memory.ScopeTeam: memory.PolicyVerifiers}
}

func TestVerifyPolicy(t *testing.T) {
	carol := memory.Principal{OrgID: memory.DefaultOrg, Subject: "carol", Teams: []string{"platform"}, Channel: memory.ChannelCLI}
	withVerifies := func(p memory.Principal, scopes ...string) memory.Principal { p.Verifies = scopes; return p }
	aliceCLI := alice
	aliceCLI.Channel = memory.ChannelCLI
	tests := []struct {
		name        string
		opts        []func(*memory.Options)
		by          memory.Principal
		verdict     memory.Verdict
		wantCounted bool
		wantStatus  memory.Status
	}{
		{"writers: a member confirm counts", nil, bob, memory.VerdictConfirm, true, memory.StatusVerified},
		{"verifiers: a member confirm is not counted", []func(*memory.Options){verifiersOnTeams}, bob, memory.VerdictConfirm, false, memory.StatusUnverified},
		{"verifiers: a verifier of the team counts", []func(*memory.Options){verifiersOnTeams}, withVerifies(carol, "team:platform"), memory.VerdictConfirm, true, memory.StatusVerified},
		{"verifiers: a verifier of every team counts", []func(*memory.Options){verifiersOnTeams}, withVerifies(carol, "team:*"), memory.VerdictConfirm, true, memory.StatusVerified},
		{"verifiers: a verifier of another team is not counted", []func(*memory.Options){verifiersOnTeams}, withVerifies(carol, "team:sales", "project:*"), memory.VerdictConfirm, false, memory.StatusUnverified},
		{"verifiers: the author on its own channel is not counted", []func(*memory.Options){verifiersOnTeams}, withVerifies(alice, "team:platform"), memory.VerdictConfirm, false, memory.StatusUnverified},
		{"verifiers: the author over another channel counts", []func(*memory.Options){verifiersOnTeams}, withVerifies(aliceCLI, "team:platform"), memory.VerdictConfirm, true, memory.StatusVerified},
		{"verifiers: a member dispute counts", []func(*memory.Options){verifiersOnTeams}, bob, memory.VerdictDispute, true, memory.StatusDisputed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t, tt.opts...)
			m := f.remember(t, alice, memory.RememberRequest{Content: "deploys freeze on fridays", Scope: "team", Team: "platform"})
			res, err := f.svc.Verify(background, tt.by, memory.VerifyRequest{ID: m.ID, Verdict: tt.verdict})
			if err != nil {
				t.Fatal(err)
			}
			if res.Counted != tt.wantCounted || res.Status != tt.wantStatus {
				t.Fatalf("result = %+v", res)
			}
			vs, _ := f.store.Verifications(background, memory.DefaultOrg, m.ID)
			if len(vs) != 1 || vs[0].Counted != tt.wantCounted {
				t.Fatalf("verifications = %+v", vs)
			}
		})
	}
}

func TestVerifyPolicyLeavesProjectsAlone(t *testing.T) {
	f := newFixture(t, verifiersOnTeams)
	m := f.remember(t, inProject(alice, "web"), memory.RememberRequest{Content: "web uses vite"})
	res, err := f.svc.Verify(background, inProject(bob, "web"), memory.VerifyRequest{ID: m.ID, Verdict: memory.VerdictConfirm})
	if err != nil || !res.Counted {
		t.Fatalf("result = %+v, %v", res, err)
	}
}

func TestCanCount(t *testing.T) {
	p := memory.Principal{Subject: "carol", Verifies: []string{"team:platform", "project:*"}}
	for _, tt := range []struct {
		scope memory.Scope
		want  bool
	}{
		{memory.Scope{Type: memory.ScopeTeam, ID: "platform"}, true},
		{memory.Scope{Type: memory.ScopeTeam, ID: "sales"}, false},
		{memory.Scope{Type: memory.ScopeProject, ID: "web"}, true},
		{memory.Scope{Type: memory.ScopeUser, ID: "carol"}, false},
	} {
		if got := p.CanCount(tt.scope); got != tt.want {
			t.Errorf("CanCount(%s) = %v, want %v", tt.scope, got, tt.want)
		}
	}
}

func TestRecallScopeMinStatus(t *testing.T) {
	f := newFixture(t, func(o *memory.Options) {
		o.MinStatus = map[memory.ScopeType]memory.Status{memory.ScopeTeam: memory.StatusVerified}
	})
	p := inProject(alice, "web")
	team := f.remember(t, p, memory.RememberRequest{Content: "the release train leaves on tuesday", Scope: "team", Team: "platform"})
	proj := f.remember(t, p, memory.RememberRequest{Content: "the web release train leaves on monday", Scope: "project"})
	got := resultIDs(f.recall(t, p, memory.RecallRequest{Query: "release train"}))
	if !slices.Equal(got, []string{proj.ID}) {
		t.Fatalf("default recall = %v, want only the project memory", got)
	}
	got = resultIDs(f.recall(t, p, memory.RecallRequest{Query: "release train", MinStatus: memory.StatusUnverified}))
	if len(got) != 2 {
		t.Fatalf("min_status unverified = %v, want both", got)
	}
	if _, err := f.svc.Verify(background, inProject(bob, "web"), memory.VerifyRequest{ID: team.ID, Verdict: memory.VerdictConfirm}); err != nil {
		t.Fatal(err)
	}
	got = resultIDs(f.recall(t, p, memory.RecallRequest{Query: "release train"}))
	if len(got) != 2 {
		t.Fatalf("after confirm = %v, want both", got)
	}
}
