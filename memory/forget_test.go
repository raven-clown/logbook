package memory_test

import (
	"testing"

	"github.com/raven-clown/logbook/memory"
)

func TestForget(t *testing.T) {
	f := newFixture(t)
	m := f.remember(t, alice, memory.RememberRequest{Content: "the wifi password is on the fridge", Tags: []string{"office"}})
	got, err := f.svc.Forget(background, alice, memory.ForgetRequest{ID: m.ID, Reason: "moved office"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != memory.StatusForgotten || !got.ForgottenAt.Equal(f.now) || got.Content == "" {
		t.Fatalf("forgotten = %+v", got)
	}
	if res := f.recall(t, alice, memory.RecallRequest{Query: "wifi"}); len(res) != 0 {
		t.Fatalf("recall after forget = %v", resultIDs(res))
	}
	list, err := f.svc.List(background, alice, memory.ListRequest{})
	if err != nil || len(list.Items) != 0 {
		t.Fatalf("list after forget = %v, %v", list, err)
	}
	list, _ = f.svc.List(background, alice, memory.ListRequest{Statuses: []memory.Status{memory.StatusForgotten}})
	if len(list.Items) != 1 || list.Items[0].Content == "" {
		t.Fatalf("forgotten list = %+v", list.Items)
	}
	again, err := f.svc.Forget(background, alice, memory.ForgetRequest{ID: m.ID})
	if err != nil || again.Version != got.Version {
		t.Fatalf("forget twice = %+v, %v", again, err)
	}
	events, _ := f.store.Events(background, memory.DefaultOrg, m.ID)
	if events[len(events)-1].Action != "forgotten" || string(events[len(events)-1].Detail) != `{"reason":"moved office"}` {
		t.Fatalf("events = %+v", events)
	}
}

func TestForgetPurge(t *testing.T) {
	f := newFixture(t)
	m := f.remember(t, alice, memory.RememberRequest{Content: "the api key rotates on fridays", Evidence: "runbook", Tags: []string{"ops"}, Refs: []string{"runbook.md"}})
	got, err := f.svc.Forget(background, alice, memory.ForgetRequest{ID: m.ID, Purge: true})
	if err != nil {
		t.Fatal(err)
	}
	stored := f.get(t, m.ID)
	if !got.Purged || stored.Content != "" || stored.Evidence != "" || stored.Tags != nil || stored.Refs != nil || stored.Status != memory.StatusForgotten {
		t.Fatalf("purged = %+v", stored)
	}
	hits, _ := f.store.SearchText(background, "rotates", memory.Filter{OrgID: memory.DefaultOrg, Scopes: []string{"user:alice"}, Statuses: []memory.Status{memory.StatusForgotten}}, 10)
	if len(hits) != 0 {
		t.Fatalf("purged memory still searchable")
	}
	events, _ := f.store.Events(background, memory.DefaultOrg, m.ID)
	if events[len(events)-1].Action != "purged" {
		t.Fatalf("events = %+v", events)
	}

	n := f.remember(t, alice, memory.RememberRequest{Content: "forget first, purge later"})
	if _, err := f.svc.Forget(background, alice, memory.ForgetRequest{ID: n.ID}); err != nil {
		t.Fatal(err)
	}
	if got, err := f.svc.Forget(background, alice, memory.ForgetRequest{ID: n.ID, Purge: true}); err != nil || !got.Purged {
		t.Fatalf("purge after forget = %+v, %v", got, err)
	}
}

func TestForgetPermissions(t *testing.T) {
	f := newFixture(t)
	private := f.remember(t, alice, memory.RememberRequest{Content: "alice private"})
	_, err := f.svc.Forget(background, bob, memory.ForgetRequest{ID: private.ID})
	wantCode(t, err, memory.CodeNotFound, "id")

	shared := f.remember(t, inProject(alice, "p"), memory.RememberRequest{Content: "project note from alice"})
	if _, err := f.svc.Forget(background, inProject(bob, "p"), memory.ForgetRequest{ID: shared.ID}); err != nil {
		t.Fatalf("project member forgetting: %v", err)
	}

	team := f.remember(t, alice, memory.RememberRequest{Content: "platform note", Scope: "team:platform"})
	leaver := alice
	leaver.Teams = nil
	_, err = f.svc.Forget(background, leaver, memory.ForgetRequest{ID: team.ID})
	if err != nil {
		t.Fatalf("author who left the team forgetting their own memory: %v", err)
	}
}
