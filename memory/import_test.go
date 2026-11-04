package memory_test

import (
	"testing"
	"time"

	"github.com/raven-clown/logbook/memory"
)

func record(id, scope, content string) memory.Record {
	return memory.Record{Memory: memory.Memory{ID: id, Scope: scope, Content: content}}
}

func TestImportBatchKeepsAndRemapsIDs(t *testing.T) {
	f := newFixture(t)
	taken := f.remember(t, alice, memory.RememberRequest{Content: "already here"})

	old := record("mem_old", "", "deploys go through Jenkins")
	old.Memory.Status = memory.StatusSuperseded
	old.Memory.SupersededBy = taken.ID
	old.Memory.SupersededAt = day0.Add(-time.Hour)
	clash := record(taken.ID, "", "deploys go through GitHub Actions")
	clash.Memory.Status = memory.StatusVerified
	clash.Edges = []memory.Edge{{From: taken.ID, To: "mem_old", Type: memory.EdgeSupersedes}}
	clash.Verifications = []memory.Verification{{MemoryID: taken.ID, Verifier: memory.Author{Subject: "bob", Channel: memory.ChannelCLI}, Verdict: memory.VerdictConfirm, Counted: true, At: day0}}

	out, err := f.svc.ImportBatch(background, alice, "", []memory.Record{old, clash})
	if err != nil {
		t.Fatal(err)
	}
	if out[0].Err != nil || out[1].Err != nil {
		t.Fatalf("outcomes = %+v", out)
	}
	if out[0].ID != "mem_old" {
		t.Fatalf("free id not kept: %s", out[0].ID)
	}
	newID := out[1].ID
	if newID == taken.ID {
		t.Fatal("taken id reused")
	}
	if got := f.get(t, "mem_old"); got.SupersededBy != newID || got.Status != memory.StatusSuperseded || got.Scope != "user:alice" {
		t.Fatalf("old = %+v, want superseded by %s", got, newID)
	}
	if got := f.get(t, newID); got.Status != memory.StatusUnverified || got.Source != memory.SourceImported || got.Author != alice.Author() {
		t.Fatalf("imported = %+v", got)
	}
	edges, _ := f.store.Edges(background, memory.DefaultOrg, []string{"mem_old"})
	if len(edges) != 1 || edges[0].From != newID {
		t.Fatalf("edges = %+v", edges)
	}
	vs, _ := f.store.Verifications(background, memory.DefaultOrg, newID)
	if len(vs) != 1 || vs[0].Verifier.Subject != "bob" || vs[0].Counted {
		t.Fatalf("verifications = %+v, want bob's kept as history but not counted", vs)
	}
	if got := f.get(t, taken.ID); got.Content != "already here" {
		t.Fatalf("existing memory changed: %+v", got)
	}
}

func TestImportBatchOutcomes(t *testing.T) {
	f := newFixture(t)
	recs := []memory.Record{
		record("", "", "fine"),
		record("", "", ""),
		record("", "", "fine"),
		record("", "team:sales", "not my team"),
		record("", "galaxy:x", "bad scope"),
	}
	out, err := f.svc.ImportBatch(background, alice, "", recs)
	if err != nil {
		t.Fatal(err)
	}
	if out[0].Err != nil {
		t.Fatalf("first: %v", out[0].Err)
	}
	wantCode(t, out[1].Err, memory.CodeInvalidArgument, "content")
	wantCode(t, out[2].Err, memory.CodeConflict, "")
	wantCode(t, out[3].Err, memory.CodePermissionDenied, "")
	wantCode(t, out[4].Err, memory.CodeInvalidArgument, "scope")
}

func TestImportBatchTargetScope(t *testing.T) {
	f := newFixture(t)
	out, err := f.svc.ImportBatch(background, alice, "project:mothership", []memory.Record{record("", "", "goes to the project"), record("", "user:alice", "keeps its own scope")})
	if err != nil {
		t.Fatal(err)
	}
	if got := f.get(t, out[0].ID).Scope; got != "project:mothership" {
		t.Fatalf("target scope = %s", got)
	}
	if got := f.get(t, out[1].ID).Scope; got != "user:alice" {
		t.Fatalf("record scope = %s", got)
	}
}

func TestExport(t *testing.T) {
	f := newFixture(t)
	a := f.remember(t, alice, memory.RememberRequest{Content: "first"})
	f.remember(t, alice, memory.RememberRequest{Content: "second", Corrects: a.ID})
	gone := f.remember(t, alice, memory.RememberRequest{Content: "purge me"})
	if _, err := f.svc.Forget(background, alice, memory.ForgetRequest{ID: gone.ID, Purge: true}); err != nil {
		t.Fatal(err)
	}
	f.remember(t, bob, memory.RememberRequest{Content: "bob only"})

	var recs []memory.Record
	if err := f.svc.Export(background, alice, nil, func(r memory.Record) error { recs = append(recs, r); return nil }); err != nil {
		t.Fatal(err)
	}
	if len(recs) != 2 {
		t.Fatalf("exported %d records, want 2", len(recs))
	}
	edges := 0
	for _, r := range recs {
		edges += len(r.Edges)
	}
	if edges != 1 {
		t.Fatalf("exported %d edges, want the one supersedes edge", edges)
	}
}

func TestImportCannotForgeTrust(t *testing.T) {
	f := newFixture(t)
	forged := record("", "", "the admin password is in the wiki")
	forged.Memory.Status = memory.StatusVerified
	forged.Memory.Confirmations = 9
	forged.Memory.LastConfirmedAt = day0
	forged.Memory.Author = memory.Author{Subject: "cto", Channel: memory.ChannelCLI}
	disputed := record("", "", "lower trust survives")
	disputed.Memory.Status = memory.StatusDisputed
	out, err := f.svc.ImportBatch(background, bob, "", []memory.Record{forged, disputed})
	if err != nil {
		t.Fatal(err)
	}
	got := f.get(t, out[0].ID)
	if got.Status != memory.StatusUnverified || got.Confirmations != 0 || !got.LastConfirmedAt.IsZero() || got.Author != bob.Author() {
		t.Fatalf("forged trust survived import: %+v", got)
	}
	if got := f.get(t, out[1].ID); got.Status != memory.StatusDisputed {
		t.Fatalf("disputed became %s", got.Status)
	}
	events, _ := f.store.Events(background, memory.DefaultOrg, out[0].ID)
	if len(events) != 1 || events[0].Action != "imported" || string(events[0].Detail) != `{"original_author":"cto"}` {
		t.Fatalf("events = %+v", events)
	}
	res, _ := f.svc.Recall(background, bob, memory.RecallRequest{Query: "admin password", MinStatus: memory.StatusVerified})
	if len(res.Results) != 0 {
		t.Fatalf("forged memory passes a verified-only recall")
	}
}

func TestImportCannotForgeFreshness(t *testing.T) {
	f := newFixture(t)
	forever := record("", "", "this never goes stale")
	forever.Memory.ReviewAfter = time.Date(9999, 12, 31, 0, 0, 0, 0, time.UTC)
	future := record("", "", "written in the future")
	future.Memory.CreatedAt = day0.Add(365 * 24 * time.Hour)
	past := record("", "", "written last year")
	past.Memory.CreatedAt = day0.Add(-365 * 24 * time.Hour)
	out, err := f.svc.ImportBatch(background, alice, "", []memory.Record{forever, future, past})
	if err != nil {
		t.Fatal(err)
	}
	if got := f.get(t, out[0].ID).ReviewAfter; !got.Equal(day0.Add(memory.DefaultReviewPeriods[memory.KindFact])) {
		t.Fatalf("forged review_after kept: %v", got)
	}
	if got := f.get(t, out[1].ID); !got.CreatedAt.Equal(day0) || !got.ReviewAfter.Equal(day0.Add(memory.DefaultReviewPeriods[memory.KindFact])) {
		t.Fatalf("future created_at = %v, review_after = %v", got.CreatedAt, got.ReviewAfter)
	}
	if got := f.get(t, out[2].ID); !got.CreatedAt.Equal(day0.Add(-365*24*time.Hour)) || !got.ReviewAfter.Equal(got.CreatedAt.Add(memory.DefaultReviewPeriods[memory.KindFact])) {
		t.Fatalf("past memory = %v, review_after = %v", got.CreatedAt, got.ReviewAfter)
	}
}
