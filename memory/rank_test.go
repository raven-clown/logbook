package memory_test

import (
	"math"
	"reflect"
	"testing"

	"github.com/raven-clown/logbook/memory"
)

func TestFuse(t *testing.T) {
	got := memory.Fuse(60, []memory.Hit{{ID: "a"}, {ID: "b"}, {ID: "c"}})
	want := []float64{1.0 / 61, 1.0 / 62, 1.0 / 63}
	for i, h := range got {
		if h.ID != []string{"a", "b", "c"}[i] || math.Abs(h.Score-want[i]) > 1e-12 {
			t.Fatalf("Fuse single list = %+v", got)
		}
	}

	got = memory.Fuse(60, []memory.Hit{{ID: "b"}, {ID: "a"}}, []memory.Hit{{ID: "a"}, {ID: "b"}})
	if len(got) != 2 || got[0].ID != "a" || got[1].ID != "b" || got[0].Score != got[1].Score {
		t.Fatalf("Fuse tie = %+v, want equal scores ordered by id", got)
	}

	if got := memory.Fuse(60); len(got) != 0 {
		t.Fatalf("Fuse() = %v", got)
	}
}

func TestReadableScopes(t *testing.T) {
	p := memory.Principal{Subject: "alice", Teams: []string{"platform", "sales"}, Project: "mothership"}
	want := []string{"user:alice", "project:mothership", "team:platform", "team:sales"}
	if got := p.ReadableScopes("", ""); !reflect.DeepEqual(got, want) {
		t.Fatalf("ReadableScopes = %v", got)
	}
	if got := p.ReadableScopes("rosetta", "sales"); !reflect.DeepEqual(got, []string{"user:alice", "project:rosetta", "team:sales"}) {
		t.Fatalf("ReadableScopes(rosetta, sales) = %v", got)
	}
	if got := (memory.Principal{Subject: "bob"}).ReadableScopes("", ""); !reflect.DeepEqual(got, []string{"user:bob"}) {
		t.Fatalf("no project, no teams = %v", got)
	}
}
