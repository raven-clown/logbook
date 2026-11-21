package memory_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/raven-clown/logbook/memory"
)

func TestPurgeForgotten(t *testing.T) {
	day := 24 * time.Hour
	f := newFixture(t)
	old := f.remember(t, alice, memory.RememberRequest{Content: "the vpn password is in the vault", Evidence: "runbook", Tags: []string{"ops"}})
	team := f.remember(t, bob, memory.RememberRequest{Content: "the staging db is called pg-17", Scope: "team", Team: "platform"})
	done := f.remember(t, alice, memory.RememberRequest{Content: "already purged"})
	recent := f.remember(t, alice, memory.RememberRequest{Content: "forgotten yesterday"})
	kept := f.remember(t, alice, memory.RememberRequest{Content: "still here"})
	for _, id := range []string{old.ID, team.ID} {
		if _, err := f.svc.Forget(background, alice, memory.ForgetRequest{ID: id}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.svc.Forget(background, alice, memory.ForgetRequest{ID: done.ID, Purge: true}); err != nil {
		t.Fatal(err)
	}
	f.advance(30 * day)
	if _, err := f.svc.Forget(background, alice, memory.ForgetRequest{ID: recent.ID}); err != nil {
		t.Fatal(err)
	}
	f.advance(day)

	if n, err := f.svc.PurgeForgotten(background, memory.DefaultOrg, 0); err != nil || n != 0 {
		t.Fatalf("zero retention purged %d, %v", n, err)
	}
	n, err := f.svc.PurgeForgotten(background, memory.DefaultOrg, 30*day)
	if err != nil || n != 2 {
		t.Fatalf("PurgeForgotten = %d, %v, want 2", n, err)
	}
	for _, id := range []string{old.ID, team.ID} {
		m := f.get(t, id)
		if !m.Purged || m.Content != "" || m.Evidence != "" || m.Tags != nil {
			t.Fatalf("%s not purged: %+v", id, m)
		}
		events, _ := f.store.Events(background, memory.DefaultOrg, id)
		last := events[len(events)-1]
		if last.Action != "purged" || last.Actor != (memory.Author{Subject: "logbook", Channel: memory.ChannelJob}) {
			t.Fatalf("last event = %+v", last)
		}
	}
	if m := f.get(t, recent.ID); m.Purged || m.Content == "" {
		t.Fatalf("recent memory purged early: %+v", m)
	}
	if m := f.get(t, kept.ID); m.Purged || m.Status != memory.StatusUnverified {
		t.Fatalf("active memory touched: %+v", m)
	}
	if n, err := f.svc.PurgeForgotten(background, memory.DefaultOrg, 30*day); err != nil || n != 0 {
		t.Fatalf("second run = %d, %v", n, err)
	}
}

func TestPurgeForgottenPages(t *testing.T) {
	f := newFixture(t)
	for i := range 105 {
		m := f.remember(t, alice, memory.RememberRequest{Content: fmt.Sprintf("note %d", i)})
		f.advance(time.Second)
		if _, err := f.svc.Forget(background, alice, memory.ForgetRequest{ID: m.ID}); err != nil {
			t.Fatal(err)
		}
	}
	f.advance(48 * time.Hour)
	if n, err := f.svc.PurgeForgotten(background, memory.DefaultOrg, time.Hour); err != nil || n != 105 {
		t.Fatalf("PurgeForgotten = %d, %v, want 105", n, err)
	}
}
