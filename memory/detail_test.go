package memory_test

import (
	"strings"
	"testing"

	"github.com/raven-clown/logbook/memory"
)

func TestDetail(t *testing.T) {
	f := newFixture(t)
	a := f.remember(t, alice, memory.RememberRequest{Content: "the build uses go 1.25", Scope: "team", Team: "platform"})
	b := f.remember(t, alice, memory.RememberRequest{Content: "the build uses go 1.26", Corrects: a.ID, Scope: "team", Team: "platform"})
	if _, err := f.svc.Verify(background, bob, memory.VerifyRequest{ID: b.ID, Verdict: memory.VerdictConfirm}); err != nil {
		t.Fatal(err)
	}
	d, err := f.svc.Detail(background, alice, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if d.Memory.ID != b.ID || len(d.Edges) != 1 || d.Edges[0].Type != memory.EdgeSupersedes || d.Edges[0].To != a.ID {
		t.Fatalf("detail = %+v", d)
	}
	if len(d.Verifications) != 1 || d.Verifications[0].Verdict != memory.VerdictConfirm {
		t.Fatalf("verifications = %+v", d.Verifications)
	}
	if len(d.History) == 0 || d.History[0].Action != "created" {
		t.Fatalf("history = %+v", d.History)
	}
	empty, err := f.svc.Detail(background, alice, f.remember(t, alice, memory.RememberRequest{Content: "lonely"}).ID)
	if err != nil || empty.Edges == nil || empty.Verifications == nil {
		t.Fatalf("empty lists must be [] in JSON: %+v, %v", empty, err)
	}
}

func TestDetailHidesEdges(t *testing.T) {
	f := newFixture(t)
	secret := f.remember(t, bob, memory.RememberRequest{Content: "bob only"})
	team := f.remember(t, bob, memory.RememberRequest{Content: "the team note", Scope: "team", Team: "platform", RelatedTo: []string{secret.ID}})
	d, err := f.svc.Detail(background, alice, team.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Edges) != 0 {
		t.Fatalf("edge to an unreadable memory leaked: %+v", d.Edges)
	}
}

func TestDetailNotFoundAcrossScopes(t *testing.T) {
	f := newFixture(t)
	m := f.remember(t, bob, memory.RememberRequest{Content: "bob only"})
	_, err := f.svc.Detail(background, alice, m.ID)
	wantCode(t, err, memory.CodeNotFound, "id")
}

func TestLink(t *testing.T) {
	f := newFixture(t)
	a := f.remember(t, alice, memory.RememberRequest{Content: "deploys run at noon"})
	b := f.remember(t, alice, memory.RememberRequest{Content: "the deploy window is one hour"})
	for range 2 {
		e, err := f.svc.Link(background, alice, a.ID, b.ID)
		if err != nil {
			t.Fatal(err)
		}
		if e.From != a.ID || e.To != b.ID || e.Type != memory.EdgeRelated || e.CreatedBy != "alice" {
			t.Fatalf("edge = %+v", e)
		}
	}
	d, _ := f.svc.Detail(background, alice, a.ID)
	if len(d.Edges) != 1 {
		t.Fatalf("edges after two links = %+v", d.Edges)
	}
	if last := d.History[len(d.History)-1]; last.Action != "linked" {
		t.Fatalf("last event = %+v", last)
	}
}

func TestLinkRules(t *testing.T) {
	f := newFixture(t)
	mine := f.remember(t, alice, memory.RememberRequest{Content: "alice note"})
	bobs := f.remember(t, bob, memory.RememberRequest{Content: "bob note"})
	gone := f.remember(t, alice, memory.RememberRequest{Content: "gone soon"})
	if _, err := f.svc.Forget(background, alice, memory.ForgetRequest{ID: gone.ID}); err != nil {
		t.Fatal(err)
	}
	open := f.remember(t, alice, memory.RememberRequest{Content: "project note", Scope: "project", Project: "api"})
	for _, tt := range []struct {
		name     string
		p        memory.Principal
		from, to string
		code     memory.Code
		field    string
	}{
		{"self", alice, mine.ID, mine.ID, memory.CodeInvalidArgument, "to"},
		{"unreadable target", alice, mine.ID, bobs.ID, memory.CodeNotFound, "to"},
		{"unreadable source", alice, bobs.ID, mine.ID, memory.CodeNotFound, "id"},
		{"forgotten target", alice, mine.ID, gone.ID, memory.CodeNotFound, "to"},
		{"missing target", alice, mine.ID, "mem_nope", memory.CodeNotFound, "to"},
		{"empty target", alice, mine.ID, "", memory.CodeInvalidArgument, "to"},
		{"no write on source", memory.Principal{OrgID: memory.DefaultOrg, Subject: "carol", RestrictProjects: true, Projects: []string{}}, open.ID, mine.ID, memory.CodeNotFound, "id"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := f.svc.Link(background, tt.p, tt.from, tt.to)
			wantCode(t, err, tt.code, tt.field)
		})
	}
}

func TestDetailHidesIDsInHistory(t *testing.T) {
	f := newFixture(t)
	private := f.remember(t, alice, memory.RememberRequest{Content: "alice private note"})
	team := f.remember(t, alice, memory.RememberRequest{Content: "team runbook", Scope: "team", Team: "platform"})
	if _, err := f.svc.Link(background, alice, team.ID, private.ID); err != nil {
		t.Fatal(err)
	}
	d, err := f.svc.Detail(background, bob, team.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range d.History {
		if strings.Contains(string(e.Detail), private.ID) {
			t.Fatalf("history shows an id bob can't read: %s %s", e.Action, e.Detail)
		}
	}
	d, _ = f.svc.Detail(background, alice, team.ID)
	if last := d.History[len(d.History)-1]; !strings.Contains(string(last.Detail), private.ID) {
		t.Fatalf("alice lost her own link target: %s", last.Detail)
	}
}
