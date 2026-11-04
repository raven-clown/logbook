package memory_test

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/raven-clown/logbook/memory"
	"github.com/raven-clown/logbook/staleness"
	"github.com/raven-clown/logbook/store/sqlite"
)

type fixture struct {
	svc   *memory.Service
	store memory.Store
	now   time.Time
}

func (f *fixture) advance(d time.Duration) { f.now = f.now.Add(d) }

func newFixture(t *testing.T) *fixture {
	t.Helper()
	st, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "logbook.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	f := &fixture{store: st, now: day0}
	n := 0
	f.svc = memory.NewService(st, memory.Options{
		Now:   func() time.Time { return f.now },
		NewID: func() string { n++; return fmt.Sprintf("mem_%d", n) },
		Rules: staleness.Default(),
	})
	return f
}

var (
	alice      = memory.Principal{OrgID: memory.DefaultOrg, Subject: "alice", Teams: []string{"platform"}, Channel: memory.ChannelMCP}
	bob        = memory.Principal{OrgID: memory.DefaultOrg, Subject: "bob", Teams: []string{"platform"}, Channel: memory.ChannelMCP}
	inProject  = func(p memory.Principal, project string) memory.Principal { p.Project = project; return p }
	background = context.Background()
)

func (f *fixture) remember(t *testing.T, p memory.Principal, r memory.RememberRequest) memory.Remembered {
	t.Helper()
	res, err := f.svc.Remember(background, p, r)
	if err != nil {
		t.Fatalf("Remember(%q): %v", r.Content, err)
	}
	if len(res.Memories) != 1 {
		t.Fatalf("Remember(%q) returned %d memories", r.Content, len(res.Memories))
	}
	return res.Memories[0]
}

func (f *fixture) get(t *testing.T, id string) *memory.Memory {
	t.Helper()
	m, err := f.store.Get(background, memory.DefaultOrg, id)
	if err != nil {
		t.Fatalf("Get(%s): %v", id, err)
	}
	return m
}

func wantCode(t *testing.T, err error, code memory.Code, field string) {
	t.Helper()
	var e *memory.Error
	if !asError(err, &e) {
		t.Fatalf("err = %v, want %s", err, code)
	}
	if e.Code != code || e.Field != field {
		t.Fatalf("err = %s on %q (%s), want %s on %q", e.Code, e.Field, e.Message, code, field)
	}
}
