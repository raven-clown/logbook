// Package storetest is the conformance suite every storage backend runs.
// A backend's test calls Run with a function that opens a fresh, empty store.
package storetest

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/raven-clown/logbook/memory"
)

var base = time.Date(2026, 11, 4, 9, 30, 0, 123456789, time.UTC)

// Run runs every conformance check against stores made by open.
func Run(t *testing.T, open func(t *testing.T) memory.Store) {
	checks := []struct {
		name string
		fn   func(t *testing.T, s memory.Store)
	}{
		{"InsertGet", insertGet},
		{"GetMissing", getMissing},
		{"UpdateVersion", updateVersion},
		{"OrgIsolation", orgIsolation},
		{"FindByHash", findByHash},
		{"FindBySubject", findBySubject},
		{"ListFilters", listFilters},
		{"ListPaging", listPaging},
		{"ListReviewOrder", listReviewOrder},
		{"SearchText", searchText},
		{"SearchTextSyntax", searchTextSyntax},
		{"Edges", edges},
		{"VerificationsEvents", verificationsEvents},
		{"TxRollback", txRollback},
		{"ConcurrentInserts", concurrentInserts},
	}
	for _, c := range checks {
		t.Run(c.name, func(t *testing.T) {
			c.fn(t, open(t))
		})
	}
}

func newMemory(id, scope, content string) *memory.Memory {
	return &memory.Memory{
		ID:          id,
		OrgID:       memory.DefaultOrg,
		Scope:       scope,
		Content:     content,
		Kind:        memory.KindFact,
		Status:      memory.StatusUnverified,
		Source:      memory.SourceDirect,
		Author:      memory.Author{Subject: "alice", Channel: memory.ChannelMCP},
		CreatedAt:   base,
		UpdatedAt:   base,
		ContentHash: "hash-" + id,
		Version:     1,
	}
}

func insert(t *testing.T, s memory.Store, ms ...*memory.Memory) {
	t.Helper()
	for _, m := range ms {
		if err := s.Insert(context.Background(), m); err != nil {
			t.Fatalf("Insert(%s): %v", m.ID, err)
		}
	}
}

func filter(scopes ...string) memory.Filter {
	return memory.Filter{OrgID: memory.DefaultOrg, Scopes: scopes}
}

func ids(ms []*memory.Memory) []string {
	out := make([]string, len(ms))
	for i, m := range ms {
		out[i] = m.ID
	}
	return out
}

func hitIDs(hs []memory.Hit) []string {
	out := make([]string, len(hs))
	for i, h := range hs {
		out[i] = h.ID
	}
	return out
}

func list(t *testing.T, s memory.Store, f memory.Filter) []string {
	t.Helper()
	got, _, err := s.List(context.Background(), f)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	return ids(got)
}

func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	seen := map[string]int{}
	for _, x := range a {
		seen[x]++
	}
	for _, x := range b {
		seen[x]--
		if seen[x] < 0 {
			return false
		}
	}
	return true
}

func insertGet(t *testing.T, s memory.Store) {
	m := newMemory("mem_full", "project:mothership", "CI runs on Go 1.26\nwith two lines")
	m.Subject = "ci.go-version"
	m.Tags = []string{"ci", "go"}
	m.Refs = []string{".github/workflows/ci.yml", "https://go.dev/doc/devel/release"}
	m.Status = memory.StatusVerified
	m.Source = memory.SourceImported
	m.Evidence = "go version go1.26.0"
	m.Author = memory.Author{Subject: "alice", Channel: memory.ChannelCLI, Client: "logbook"}
	m.ValidUntil = base.Add(48 * time.Hour)
	m.ReviewAfter = base.Add(4320 * time.Hour)
	m.LastConfirmedAt = base.Add(time.Hour)
	m.Confirmations = 2
	m.Disputes = 1
	m.SupersededBy = "mem_next"
	m.SupersededAt = base.Add(2 * time.Hour)
	m.ForgottenAt = base.Add(3 * time.Hour)
	m.Purged = false
	insert(t, s, m)

	got, err := s.Get(context.Background(), memory.DefaultOrg, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	want := *m
	normalizeTimes(&want)
	normalizeTimes(got)
	if !reflect.DeepEqual(*got, want) {
		t.Fatalf("round trip mismatch\n got %+v\nwant %+v", *got, want)
	}
}

func normalizeTimes(m *memory.Memory) {
	for _, p := range []*time.Time{&m.CreatedAt, &m.UpdatedAt, &m.ValidUntil, &m.ReviewAfter, &m.LastConfirmedAt, &m.SupersededAt, &m.ForgottenAt} {
		if !p.IsZero() {
			*p = p.UTC().Truncate(time.Millisecond)
		}
	}
}

func getMissing(t *testing.T, s memory.Store) {
	_, err := s.Get(context.Background(), memory.DefaultOrg, "mem_missing")
	if !errors.Is(err, memory.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func updateVersion(t *testing.T, s memory.Store) {
	ctx := context.Background()
	m := newMemory("mem_v", "user:alice", "first")
	insert(t, s, m)
	stale := *m

	m.Content = "second"
	m.ContentHash = "hash-second"
	if err := s.Update(ctx, m); err != nil {
		t.Fatal(err)
	}
	if m.Version != 2 {
		t.Fatalf("Version = %d, want 2", m.Version)
	}
	got, _ := s.Get(ctx, memory.DefaultOrg, m.ID)
	if got.Content != "second" || got.Version != 2 {
		t.Fatalf("stored %q v%d", got.Content, got.Version)
	}
	hits, err := s.SearchText(ctx, "second", filter("user:alice"), 10)
	if err != nil || len(hits) != 1 {
		t.Fatalf("search after update = %v, %v", hits, err)
	}

	stale.Content = "third"
	if err := s.Update(ctx, &stale); !errors.Is(err, memory.ErrVersionConflict) {
		t.Fatalf("stale update err = %v, want ErrVersionConflict", err)
	}
}

func orgIsolation(t *testing.T, s memory.Store) {
	ctx := context.Background()
	m := newMemory("mem_org", "project:p", "only in org a")
	m.OrgID = "a"
	insert(t, s, m)

	if _, err := s.Get(ctx, "b", m.ID); !errors.Is(err, memory.ErrNotFound) {
		t.Fatalf("Get from org b: %v", err)
	}
	if _, err := s.FindByHash(ctx, "b", "project:p", m.ContentHash); !errors.Is(err, memory.ErrNotFound) {
		t.Fatalf("FindByHash from org b: %v", err)
	}
	f := memory.Filter{OrgID: "b", Scopes: []string{"project:p"}}
	if got := list(t, s, f); len(got) != 0 {
		t.Fatalf("List from org b = %v", got)
	}
	hits, err := s.SearchText(ctx, "org", f, 10)
	if err != nil || len(hits) != 0 {
		t.Fatalf("SearchText from org b = %v, %v", hits, err)
	}
	f.OrgID = "a"
	if got := list(t, s, f); len(got) != 1 {
		t.Fatalf("List from org a = %v", got)
	}
}

func findByHash(t *testing.T, s memory.Store) {
	ctx := context.Background()
	live := newMemory("mem_live", "user:alice", "live")
	live.ContentHash = "h1"
	gone := newMemory("mem_gone", "user:alice", "gone")
	gone.ContentHash = "h2"
	gone.Status = memory.StatusForgotten
	insert(t, s, live, gone)

	got, err := s.FindByHash(ctx, memory.DefaultOrg, "user:alice", "h1")
	if err != nil || got.ID != live.ID {
		t.Fatalf("FindByHash(h1) = %v, %v", got, err)
	}
	if _, err := s.FindByHash(ctx, memory.DefaultOrg, "user:alice", "h2"); !errors.Is(err, memory.ErrNotFound) {
		t.Fatalf("FindByHash(forgotten) err = %v", err)
	}
	if _, err := s.FindByHash(ctx, memory.DefaultOrg, "user:bob", "h1"); !errors.Is(err, memory.ErrNotFound) {
		t.Fatalf("FindByHash(other scope) err = %v", err)
	}
}

func findBySubject(t *testing.T, s memory.Store) {
	a := newMemory("mem_a", "project:p", "go 1.25")
	a.Subject = "ci.go-version"
	b := newMemory("mem_b", "project:p", "go 1.26")
	b.Subject = "ci.go-version"
	b.Status = memory.StatusDisputed
	c := newMemory("mem_c", "project:p", "go 1.24")
	c.Subject = "ci.go-version"
	c.Status = memory.StatusSuperseded
	d := newMemory("mem_d", "project:q", "go 1.23")
	d.Subject = "ci.go-version"
	insert(t, s, a, b, c, d)

	got, err := s.FindBySubject(context.Background(), memory.DefaultOrg, "project:p", "ci.go-version")
	if err != nil {
		t.Fatal(err)
	}
	if !sameSet(ids(got), []string{"mem_a", "mem_b"}) {
		t.Fatalf("FindBySubject = %v", ids(got))
	}
}

func listFilters(t *testing.T, s memory.Store) {
	day := 24 * time.Hour
	a := newMemory("mem_a", "project:p", "a")
	a.Kind = memory.KindDecision
	a.Tags = []string{"ci"}
	a.Subject = "deploy"
	b := newMemory("mem_b", "project:p", "b")
	b.Tags = []string{"go", "lint"}
	b.Author.Subject = "bob"
	b.ReviewAfter = base.Add(10 * day)
	c := newMemory("mem_c", "user:alice", "c")
	c.Status = memory.StatusForgotten
	c.ForgottenAt = base.Add(5 * day)
	d := newMemory("mem_d", "project:p", "d")
	d.Status = memory.StatusSuperseded
	d.SupersededAt = base.Add(2 * day)
	e := newMemory("mem_e", "project:p", "e")
	e.CreatedAt = base.Add(3 * day)
	e.Status = memory.StatusVerified
	insert(t, s, a, b, c, d, e)

	all := []string{"project:p", "user:alice"}
	tests := []struct {
		name string
		f    memory.Filter
		want []string
	}{
		{"no scopes matches nothing", memory.Filter{OrgID: memory.DefaultOrg}, nil},
		{"default hides forgotten", filter(all...), []string{"mem_a", "mem_b", "mem_d", "mem_e"}},
		{"scope", filter("user:alice"), nil},
		{"status forgotten", withStatuses(filter(all...), memory.StatusForgotten), []string{"mem_c"}},
		{"statuses", withStatuses(filter(all...), memory.StatusVerified, memory.StatusSuperseded), []string{"mem_d", "mem_e"}},
		{"kind", func() memory.Filter { f := filter(all...); f.Kinds = []memory.Kind{memory.KindDecision}; return f }(), []string{"mem_a"}},
		{"tags any of", func() memory.Filter { f := filter(all...); f.Tags = []string{"lint", "ci"}; return f }(), []string{"mem_a", "mem_b"}},
		{"subject", func() memory.Filter { f := filter(all...); f.Subject = "deploy"; return f }(), []string{"mem_a"}},
		{"author", func() memory.Filter { f := filter(all...); f.Author = "bob"; return f }(), []string{"mem_b"}},
		{"stale at before", func() memory.Filter { f := filter(all...); f.StaleAt = base.Add(9 * day); return f }(), nil},
		{"stale at after", func() memory.Filter { f := filter(all...); f.StaleAt = base.Add(10 * day); return f }(), []string{"mem_b"}},
		{"as of day 1", func() memory.Filter { f := filter(all...); f.AsOf = base.Add(day); return f }(), []string{"mem_a", "mem_b", "mem_c", "mem_d"}},
		{"as of day 4", func() memory.Filter { f := filter(all...); f.AsOf = base.Add(4 * day); return f }(), []string{"mem_a", "mem_b", "mem_c", "mem_e"}},
		{"as of day 6", func() memory.Filter { f := filter(all...); f.AsOf = base.Add(6 * day); return f }(), []string{"mem_a", "mem_b", "mem_e"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := list(t, s, tt.f); !sameSet(got, tt.want) {
				t.Fatalf("List = %v, want %v", got, tt.want)
			}
		})
	}
}

func withStatuses(f memory.Filter, st ...memory.Status) memory.Filter {
	f.Statuses = st
	return f
}

func listPaging(t *testing.T, s memory.Store) {
	for i := range 7 {
		m := newMemory(fmt.Sprintf("mem_%d", i), "user:alice", fmt.Sprintf("memory %d", i))
		m.CreatedAt = base.Add(time.Duration(i) * time.Minute)
		insert(t, s, m)
	}
	f := filter("user:alice")
	f.Limit = 3
	var pages [][]string
	for {
		got, next, err := s.List(context.Background(), f)
		if err != nil {
			t.Fatal(err)
		}
		pages = append(pages, ids(got))
		if next == "" {
			break
		}
		if len(pages) > 5 {
			t.Fatal("paging did not end")
		}
		f.Cursor = next
	}
	want := [][]string{{"mem_6", "mem_5", "mem_4"}, {"mem_3", "mem_2", "mem_1"}, {"mem_0"}}
	if !reflect.DeepEqual(pages, want) {
		t.Fatalf("pages = %v, want %v", pages, want)
	}
}

func listReviewOrder(t *testing.T, s memory.Store) {
	verified := newMemory("mem_verified", "user:alice", "verified")
	verified.Status = memory.StatusVerified
	verified.CreatedAt = base.Add(4 * time.Minute)
	unverified := newMemory("mem_unverified", "user:alice", "unverified")
	unverified.CreatedAt = base.Add(3 * time.Minute)
	stale := newMemory("mem_stale", "user:alice", "stale")
	stale.Status = memory.StatusVerified
	stale.ReviewAfter = base.Add(time.Hour)
	stale.CreatedAt = base.Add(2 * time.Minute)
	disputed := newMemory("mem_disputed", "user:alice", "disputed")
	disputed.Status = memory.StatusDisputed
	disputed.CreatedAt = base.Add(time.Minute)
	insert(t, s, verified, unverified, stale, disputed)

	f := filter("user:alice")
	f.Order = memory.OrderReview
	f.Now = base.Add(2 * time.Hour)
	want := []string{"mem_disputed", "mem_stale", "mem_unverified", "mem_verified"}
	if got := list(t, s, f); !reflect.DeepEqual(got, want) {
		t.Fatalf("review order = %v, want %v", got, want)
	}
}

func searchText(t *testing.T, s memory.Store) {
	ctx := context.Background()
	a := newMemory("mem_a", "project:p", "the deploy uses blue green")
	b := newMemory("mem_b", "project:p", "tests run on every push")
	c := newMemory("mem_c", "user:alice", "deploy checklist lives in the wiki")
	d := newMemory("mem_d", "project:p", "")
	d.Status = memory.StatusForgotten
	d.Purged = true
	insert(t, s, a, b, c, d)

	hits, err := s.SearchText(ctx, "deploy", filter("project:p"), 10)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(hitIDs(hits), []string{"mem_a"}) {
		t.Fatalf("hits = %v", hitIDs(hits))
	}
	hits, _ = s.SearchText(ctx, "deploy", filter("project:p", "user:alice"), 10)
	if !sameSet(hitIDs(hits), []string{"mem_a", "mem_c"}) {
		t.Fatalf("two scopes hits = %v", hitIDs(hits))
	}
	for i := 1; i < len(hits); i++ {
		if hits[i].Score > hits[i-1].Score {
			t.Fatalf("hits not best first: %v", hits)
		}
	}

	purged := newMemory("mem_p", "project:p", "secret token rotation")
	insert(t, s, purged)
	purged.Content = ""
	purged.Status = memory.StatusForgotten
	purged.Purged = true
	if err := s.Update(ctx, purged); err != nil {
		t.Fatal(err)
	}
	f := withStatuses(filter("project:p"), memory.StatusForgotten)
	if hits, _ := s.SearchText(ctx, "rotation", f, 10); len(hits) != 0 {
		t.Fatalf("purged memory still searchable: %v", hits)
	}
}

func searchTextSyntax(t *testing.T, s memory.Store) {
	insert(t, s, newMemory("mem_a", "project:p", "use c++ and ci.go-version, AND more"))
	for _, q := range []string{"c++", `"quoted`, "ci.go-version", "AND", "-x", "*", "(", `""`, "NEAR(", "ไทย"} {
		t.Run(q, func(t *testing.T) {
			if _, err := s.SearchText(context.Background(), q, filter("project:p"), 10); err != nil {
				t.Fatalf("SearchText(%q): %v", q, err)
			}
		})
	}
	hits, err := s.SearchText(context.Background(), "ci.go-version", filter("project:p"), 10)
	if err != nil || len(hits) != 1 {
		t.Fatalf("dotted term: %v, %v", hits, err)
	}
}

func edges(t *testing.T, s memory.Store) {
	ctx := context.Background()
	insert(t, s, newMemory("mem_a", "user:alice", "a"), newMemory("mem_b", "user:alice", "b"), newMemory("mem_c", "user:alice", "c"))
	e1 := memory.Edge{OrgID: memory.DefaultOrg, From: "mem_b", To: "mem_a", Type: memory.EdgeSupersedes, CreatedBy: "alice", CreatedAt: base}
	e2 := memory.Edge{OrgID: memory.DefaultOrg, From: "mem_c", To: "mem_b", Type: memory.EdgeRelated, CreatedAt: base}
	for _, e := range []memory.Edge{e1, e1, e2} {
		if err := s.AddEdge(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.Edges(ctx, memory.DefaultOrg, []string{"mem_a"})
	if err != nil || len(got) != 1 || got[0].From != "mem_b" || got[0].Type != memory.EdgeSupersedes {
		t.Fatalf("Edges(a) = %v, %v", got, err)
	}
	got, _ = s.Edges(ctx, memory.DefaultOrg, []string{"mem_b"})
	if len(got) != 2 {
		t.Fatalf("Edges(b) = %v", got)
	}
	got, _ = s.Edges(ctx, memory.DefaultOrg, nil)
	if len(got) != 0 {
		t.Fatalf("Edges(nil) = %v", got)
	}
}

func verificationsEvents(t *testing.T, s memory.Store) {
	ctx := context.Background()
	insert(t, s, newMemory("mem_a", "user:alice", "a"))
	v1 := memory.Verification{OrgID: memory.DefaultOrg, MemoryID: "mem_a", Verifier: memory.Author{Subject: "bob", Channel: memory.ChannelCLI}, Verdict: memory.VerdictConfirm, Note: "checked", Counted: true, At: base}
	v2 := memory.Verification{OrgID: memory.DefaultOrg, MemoryID: "mem_a", Verifier: memory.Author{Subject: "carol", Channel: memory.ChannelMCP}, Verdict: memory.VerdictDispute, Counted: true, At: base.Add(time.Minute)}
	for _, v := range []memory.Verification{v1, v2} {
		if err := s.AddVerification(ctx, v); err != nil {
			t.Fatal(err)
		}
	}
	vs, err := s.Verifications(ctx, memory.DefaultOrg, "mem_a")
	if err != nil || len(vs) != 2 || vs[0].Verifier.Subject != "bob" || vs[0].Note != "checked" || !vs[1].Counted || vs[1].Verdict != memory.VerdictDispute {
		t.Fatalf("Verifications = %+v, %v", vs, err)
	}

	e1 := memory.Event{OrgID: memory.DefaultOrg, MemoryID: "mem_a", Action: "created", Actor: memory.Author{Subject: "alice", Channel: memory.ChannelMCP}, At: base}
	e2 := memory.Event{OrgID: memory.DefaultOrg, MemoryID: "mem_a", Action: "confirmed", Actor: memory.Author{Subject: "bob"}, At: base.Add(time.Minute), Detail: []byte(`{"note":"checked"}`)}
	for _, e := range []memory.Event{e1, e2} {
		if err := s.AppendEvent(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	es, err := s.Events(ctx, memory.DefaultOrg, "mem_a")
	if err != nil || len(es) != 2 || es[0].Action != "created" || string(es[1].Detail) != `{"note":"checked"}` {
		t.Fatalf("Events = %+v, %v", es, err)
	}
}

func txRollback(t *testing.T, s memory.Store) {
	ctx := context.Background()
	boom := errors.New("boom")
	err := s.Tx(ctx, func(tx memory.Store) error {
		if err := tx.Insert(ctx, newMemory("mem_tx", "user:alice", "inside")); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("Tx err = %v, want boom", err)
	}
	if _, err := s.Get(ctx, memory.DefaultOrg, "mem_tx"); !errors.Is(err, memory.ErrNotFound) {
		t.Fatalf("rolled back insert is visible: %v", err)
	}
	err = s.Tx(ctx, func(tx memory.Store) error {
		return tx.Insert(ctx, newMemory("mem_ok", "user:alice", "kept"))
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(ctx, memory.DefaultOrg, "mem_ok"); err != nil {
		t.Fatalf("committed insert missing: %v", err)
	}
}

func concurrentInserts(t *testing.T, s memory.Store) {
	var wg sync.WaitGroup
	errs := make(chan error, 20)
	for i := range 20 {
		wg.Go(func() {
			m := newMemory(fmt.Sprintf("mem_%02d", i), "user:alice", fmt.Sprintf("concurrent %d", i))
			errs <- s.Tx(context.Background(), func(tx memory.Store) error {
				return tx.Insert(context.Background(), m)
			})
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent insert: %v", err)
		}
	}
	f := filter("user:alice")
	f.Limit = 50
	if got := list(t, s, f); len(got) != 20 {
		t.Fatalf("List = %d memories, want 20", len(got))
	}
}
