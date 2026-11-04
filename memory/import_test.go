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
	if got := f.get(t, newID); got.Status != memory.StatusVerified || got.Source != memory.SourceImported || got.Author != alice.Author() {
		t.Fatalf("imported = %+v", got)
	}
	edges, _ := f.store.Edges(background, memory.DefaultOrg, []string{"mem_old"})
	if len(edges) != 1 || edges[0].From != newID {
		t.Fatalf("edges = %+v", edges)
	}
	vs, _ := f.store.Verifications(background, memory.DefaultOrg, newID)
	if len(vs) != 1 || vs[0].Verifier.Subject != "bob" {
		t.Fatalf("verifications = %+v", vs)
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
