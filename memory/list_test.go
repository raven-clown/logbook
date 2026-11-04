package memory_test

import (
	"testing"
	"time"

	"github.com/raven-clown/logbook/memory"
)

func TestListReviewQueue(t *testing.T) {
	f := newFixture(t)
	p := inProject(alice, "p")
	stale := f.remember(t, p, memory.RememberRequest{Content: "the queue is slow", Kind: memory.KindObservation})
	f.advance(31 * 24 * time.Hour)
	first := f.remember(t, p, memory.RememberRequest{Content: "lint runs in ci", Subject: "lint"})
	f.remember(t, p, memory.RememberRequest{Content: "lint runs locally", Subject: "lint"})
	unverified := f.remember(t, p, memory.RememberRequest{Content: "tests run on push"})
	verified := f.remember(t, p, memory.RememberRequest{Content: "the docs live in the wiki"})
	f.verdict(t, verified.ID, bob.Author(), memory.VerdictConfirm)

	res, err := f.svc.List(background, p, memory.ListRequest{Scopes: []string{"project"}, Order: memory.OrderReview})
	if err != nil {
		t.Fatal(err)
	}
	got := idsOf(res.Items)
	if len(got) != 5 || got[0] != first.ID || got[1] != stale.ID || got[4] != verified.ID {
		t.Fatalf("review order = %v", got)
	}
	if got[2] != unverified.ID && got[3] != unverified.ID {
		t.Fatalf("unverified %s not in the unverified group: %v", unverified.ID, got)
	}

	res, _ = f.svc.List(background, p, memory.ListRequest{Stale: true})
	if ids := idsOf(res.Items); len(ids) != 1 || ids[0] != stale.ID {
		t.Fatalf("stale only = %v", ids)
	}

	var walked []string
	req := memory.ListRequest{Order: memory.OrderReview, Limit: 2, Scopes: []string{"project"}}
	for range 5 {
		page, err := f.svc.List(background, p, req)
		if err != nil {
			t.Fatal(err)
		}
		walked = append(walked, idsOf(page.Items)...)
		if page.NextCursor == "" {
			break
		}
		req.Cursor = page.NextCursor
	}
	if len(walked) != 5 {
		t.Fatalf("paging walked %v", walked)
	}
}

func TestListValidation(t *testing.T) {
	f := newFixture(t)
	_, err := f.svc.List(background, alice, memory.ListRequest{Order: "random"})
	wantCode(t, err, memory.CodeInvalidArgument, "order")
	_, err = f.svc.List(background, alice, memory.ListRequest{Limit: -1})
	wantCode(t, err, memory.CodeInvalidArgument, "limit")
	_, err = f.svc.List(background, alice, memory.ListRequest{Cursor: "garbage"})
	wantCode(t, err, memory.CodeInvalidArgument, "cursor")
	_, err = f.svc.List(background, alice, memory.ListRequest{Scopes: []string{"team:sales"}})
	wantCode(t, err, memory.CodePermissionDenied, "scopes")
}

func TestListFilters(t *testing.T) {
	f := newFixture(t)
	p := inProject(alice, "p")
	a := f.remember(t, p, memory.RememberRequest{Content: "use make release", Kind: memory.KindProcedure, Tags: []string{"ops"}, Subject: "release"})
	f.remember(t, inProject(bob, "p"), memory.RememberRequest{Content: "bob wrote this"})
	for _, tt := range []struct {
		name string
		r    memory.ListRequest
		want int
	}{
		{"kind", memory.ListRequest{Kind: memory.KindProcedure}, 1},
		{"tag", memory.ListRequest{Tag: "ops"}, 1},
		{"subject", memory.ListRequest{Subject: "release"}, 1},
		{"author", memory.ListRequest{Author: "bob"}, 1},
		{"all", memory.ListRequest{}, 2},
	} {
		res, err := f.svc.List(background, p, tt.r)
		if err != nil || len(res.Items) != tt.want {
			t.Fatalf("%s: %v, %v", tt.name, res, err)
		}
		if tt.want == 1 && tt.name != "author" && res.Items[0].ID != a.ID {
			t.Fatalf("%s returned %s", tt.name, res.Items[0].ID)
		}
	}
}

func TestListLimitCap(t *testing.T) {
	f := newFixture(t)
	for i := range 3 {
		f.remember(t, alice, memory.RememberRequest{Content: "note " + string(rune('a'+i))})
	}
	res, err := f.svc.List(background, alice, memory.ListRequest{Limit: 10000})
	if err != nil || len(res.Items) != 3 {
		t.Fatalf("huge limit = %v, %v", res, err)
	}
}

func TestGetHidesOtherScopes(t *testing.T) {
	f := newFixture(t)
	m := f.remember(t, alice, memory.RememberRequest{Content: "alice private"})
	if got, err := f.svc.Get(background, alice, m.ID); err != nil || got.ID != m.ID {
		t.Fatalf("owner get = %v, %v", got, err)
	}
	_, err := f.svc.Get(background, bob, m.ID)
	wantCode(t, err, memory.CodeNotFound, "id")
}

func idsOf(ms []*memory.Memory) []string {
	out := make([]string, len(ms))
	for i, m := range ms {
		out[i] = m.ID
	}
	return out
}

func TestReviewQueueSkipsSuperseded(t *testing.T) {
	f := newFixture(t)
	a := f.remember(t, alice, memory.RememberRequest{Content: "the vault is in us-east-1"})
	b := f.remember(t, alice, memory.RememberRequest{Content: "the vault is in eu-west-1", Corrects: a.ID})
	f.advance(200 * 24 * time.Hour)
	for _, r := range []memory.ListRequest{{Order: memory.OrderReview}, {Stale: true}} {
		res, err := f.svc.List(background, alice, r)
		if err != nil {
			t.Fatal(err)
		}
		if ids := idsOf(res.Items); len(ids) != 1 || ids[0] != b.ID {
			t.Fatalf("%+v listed %v, want only the current memory", r, ids)
		}
	}
	res, _ := f.svc.List(background, alice, memory.ListRequest{Order: memory.OrderReview, Statuses: []memory.Status{memory.StatusSuperseded}})
	if ids := idsOf(res.Items); len(ids) != 1 || ids[0] != a.ID {
		t.Fatalf("explicit superseded filter = %v", ids)
	}
}
