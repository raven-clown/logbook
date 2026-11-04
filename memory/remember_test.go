package memory_test

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/raven-clown/logbook/memory"
)

func TestRememberValidation(t *testing.T) {
	tests := []struct {
		name    string
		p       memory.Principal
		r       memory.RememberRequest
		code    memory.Code
		field   string
		success bool
	}{
		{name: "empty", p: alice, r: memory.RememberRequest{Content: ""}, code: memory.CodeInvalidArgument, field: "content"},
		{name: "whitespace", p: alice, r: memory.RememberRequest{Content: "   \n "}, code: memory.CodeInvalidArgument, field: "content"},
		{name: "over the limit", p: alice, r: memory.RememberRequest{Content: strings.Repeat("a", 8193)}, code: memory.CodeInvalidArgument, field: "content"},
		{name: "thai over the limit", p: alice, r: memory.RememberRequest{Content: strings.Repeat("ก", 2731)}, code: memory.CodeInvalidArgument, field: "content"},
		{name: "not utf-8", p: alice, r: memory.RememberRequest{Content: "\xff"}, code: memory.CodeInvalidArgument, field: "content"},
		{name: "exactly the limit", p: alice, r: memory.RememberRequest{Content: strings.Repeat("a", 8192)}, success: true},
		{name: "bad kind", p: alice, r: memory.RememberRequest{Content: "x", Kind: "note"}, code: memory.CodeInvalidArgument, field: "kind"},
		{name: "project without a name", p: alice, r: memory.RememberRequest{Content: "x", Scope: "project"}, code: memory.CodeInvalidArgument, field: "project"},
		{name: "team without a name", p: alice, r: memory.RememberRequest{Content: "x", Scope: "team"}, code: memory.CodeInvalidArgument, field: "team"},
		{name: "bad scope", p: alice, r: memory.RememberRequest{Content: "x", Scope: "galaxy"}, code: memory.CodeInvalidArgument, field: "scope"},
		{name: "extract", p: alice, r: memory.RememberRequest{Content: "x", Extract: true}, code: memory.CodeUnavailable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			_, err := f.svc.Remember(background, tt.p, tt.r)
			if tt.success {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			wantCode(t, err, tt.code, tt.field)
		})
	}
}

func TestRememberScopes(t *testing.T) {
	f := newFixture(t)
	if got := f.remember(t, alice, memory.RememberRequest{Content: "my own note"}).Scope; got != "user:alice" {
		t.Fatalf("default scope = %s", got)
	}
	if got := f.remember(t, inProject(alice, "mothership"), memory.RememberRequest{Content: "project note"}).Scope; got != "project:mothership" {
		t.Fatalf("project default = %s", got)
	}
	if got := f.remember(t, alice, memory.RememberRequest{Content: "named project", Scope: "project", Project: "rosetta"}).Scope; got != "project:rosetta" {
		t.Fatalf("named project = %s", got)
	}
	if got := f.remember(t, alice, memory.RememberRequest{Content: "team note", Scope: "team:platform"}).Scope; got != "team:platform" {
		t.Fatalf("full scope = %s", got)
	}
	_, err := f.svc.Remember(background, alice, memory.RememberRequest{Content: "x", Scope: "team", Team: "sales"})
	wantCode(t, err, memory.CodePermissionDenied, "")
	_, err = f.svc.Remember(background, alice, memory.RememberRequest{Content: "x", Scope: "user:bob"})
	wantCode(t, err, memory.CodePermissionDenied, "")
}

func TestRememberStoresWhatWasSent(t *testing.T) {
	f := newFixture(t)
	got := f.remember(t, alice, memory.RememberRequest{
		Content: "  line one\nline two  ", Kind: memory.KindProcedure, Subject: "release", Tags: []string{"ops"},
		Refs: []string{"Makefile"}, Evidence: "make release", ValidUntil: day0.Add(time.Hour),
	})
	m := f.get(t, got.ID)
	if m.Content != "line one\nline two" || m.Kind != memory.KindProcedure || m.Subject != "release" ||
		!slices.Equal(m.Tags, []string{"ops"}) || !slices.Equal(m.Refs, []string{"Makefile"}) || m.Evidence != "make release" ||
		!m.ValidUntil.Equal(day0.Add(time.Hour)) || m.Status != memory.StatusUnverified || m.Source != memory.SourceDirect ||
		m.Author != alice.Author() || m.Version != 1 || !m.CreatedAt.Equal(day0) || m.ContentHash == "" {
		t.Fatalf("stored %+v", m)
	}
	events, _ := f.store.Events(background, memory.DefaultOrg, got.ID)
	if len(events) != 1 || events[0].Action != "created" {
		t.Fatalf("events = %+v", events)
	}
}

func TestRememberDuplicate(t *testing.T) {
	f := newFixture(t)
	first := f.remember(t, alice, memory.RememberRequest{Content: "Use pnpm, not npm"})
	again := f.remember(t, alice, memory.RememberRequest{Content: "  use   PNPM, not npm "})
	if again.ID != first.ID || !again.Duplicate {
		t.Fatalf("second remember = %+v, want duplicate of %s", again, first.ID)
	}
	other := f.remember(t, inProject(alice, "p"), memory.RememberRequest{Content: "use pnpm, not npm"})
	if other.Duplicate || other.ID == first.ID {
		t.Fatalf("same content in another scope = %+v", other)
	}
}

func TestRememberCorrects(t *testing.T) {
	f := newFixture(t)
	p := inProject(alice, "p")
	a := f.remember(t, p, memory.RememberRequest{Content: "deploys go through Jenkins"})
	f.advance(time.Hour)
	b := f.remember(t, p, memory.RememberRequest{Content: "deploys go through GitHub Actions", Corrects: a.ID})
	if !slices.Equal(b.Superseded, []string{a.ID}) {
		t.Fatalf("Superseded = %v", b.Superseded)
	}
	old := f.get(t, a.ID)
	if old.Status != memory.StatusSuperseded || old.SupersededBy != b.ID || !old.SupersededAt.Equal(f.now) {
		t.Fatalf("old = %+v", old)
	}
	edges, _ := f.store.Edges(background, memory.DefaultOrg, []string{a.ID})
	if len(edges) != 1 || edges[0].From != b.ID || edges[0].To != a.ID || edges[0].Type != memory.EdgeSupersedes {
		t.Fatalf("edges = %+v", edges)
	}

	_, err := f.svc.Remember(background, p, memory.RememberRequest{Content: "deploys go through Argo", Corrects: a.ID})
	wantCode(t, err, memory.CodeConflict, "corrects")
	_, err = f.svc.Remember(background, p, memory.RememberRequest{Content: "x", Corrects: "mem_404"})
	wantCode(t, err, memory.CodeNotFound, "corrects")

	mine := f.remember(t, alice, memory.RememberRequest{Content: "private"})
	_, err = f.svc.Remember(background, bob, memory.RememberRequest{Content: "fixed", Corrects: mine.ID})
	wantCode(t, err, memory.CodeNotFound, "corrects")
}

func TestRememberSubjectConflict(t *testing.T) {
	f := newFixture(t)
	p := inProject(alice, "p")
	a := f.remember(t, p, memory.RememberRequest{Content: "CI runs on Go 1.25", Subject: "ci.go-version"})
	b := f.remember(t, p, memory.RememberRequest{Content: "CI runs on Go 1.26", Subject: "ci.go-version"})
	if !slices.Equal(b.Conflicts, []string{a.ID}) {
		t.Fatalf("Conflicts = %v", b.Conflicts)
	}
	if got := f.get(t, a.ID).Status; got != memory.StatusDisputed {
		t.Fatalf("older status = %s", got)
	}
	if got := f.get(t, b.ID).Status; got != memory.StatusUnverified {
		t.Fatalf("newer status = %s", got)
	}
	edges, _ := f.store.Edges(background, memory.DefaultOrg, []string{b.ID})
	if len(edges) != 1 || edges[0].Type != memory.EdgeContradicts || edges[0].From != b.ID || edges[0].To != a.ID {
		t.Fatalf("edges = %+v", edges)
	}
	c := f.remember(t, inProject(alice, "q"), memory.RememberRequest{Content: "CI runs on Go 1.26", Subject: "ci.go-version"})
	if c.Duplicate || len(c.Conflicts) != 0 {
		t.Fatalf("other scope = %+v", c)
	}
}

func TestRememberRelated(t *testing.T) {
	f := newFixture(t)
	a := f.remember(t, alice, memory.RememberRequest{Content: "staging uses postgres 17"})
	b := f.remember(t, alice, memory.RememberRequest{Content: "staging is in eu-west-1", RelatedTo: []string{a.ID}})
	edges, _ := f.store.Edges(background, memory.DefaultOrg, []string{a.ID})
	if len(edges) != 1 || edges[0].From != b.ID || edges[0].Type != memory.EdgeRelated {
		t.Fatalf("edges = %+v", edges)
	}
	_, err := f.svc.Remember(background, alice, memory.RememberRequest{Content: "y", RelatedTo: []string{"mem_404"}})
	wantCode(t, err, memory.CodeNotFound, "related_to")
}

func TestRememberReviewAfter(t *testing.T) {
	f := newFixture(t)
	obs := f.remember(t, alice, memory.RememberRequest{Content: "the build took 4 minutes today", Kind: memory.KindObservation})
	if got := f.get(t, obs.ID).ReviewAfter; !got.Equal(day0.Add(720 * time.Hour)) {
		t.Fatalf("observation review_after = %v", got)
	}
	pref := f.remember(t, alice, memory.RememberRequest{Content: "I like short answers", Kind: memory.KindPreference})
	if got := f.get(t, pref.ID).ReviewAfter; !got.IsZero() {
		t.Fatalf("preference review_after = %v", got)
	}
}
