package storetest

import (
	"context"
	"testing"
	"time"

	"github.com/raven-clown/logbook/admin"
	"github.com/raven-clown/logbook/memory"
)

// RunAdmin runs the conformance checks for API keys and bindings.
func RunAdmin(t *testing.T, open func(t *testing.T) admin.Store) {
	checks := []struct {
		name string
		fn   func(t *testing.T, s admin.Store)
	}{
		{"KeyRoundTrip", keyRoundTrip},
		{"KeyNameTaken", keyNameTaken},
		{"KeyDelete", keyDelete},
		{"KeyTouch", keyTouch},
		{"BindingRoundTrip", bindingRoundTrip},
		{"BindingDuplicate", bindingDuplicate},
		{"BindingsByPrincipal", bindingsByPrincipal},
		{"BindingDelete", bindingDelete},
		{"AdminOrgSeparation", adminOrgSeparation},
	}
	for _, c := range checks {
		t.Run(c.name, func(t *testing.T) { c.fn(t, open(t)) })
	}
}

func newKey(org, id, name string) *admin.APIKey {
	return &admin.APIKey{OrgID: org, ID: id, Name: name, Hash: "hash-" + id, Last4: "abcd", CreatedAt: base}
}

func newBinding(id, principal string, role admin.Role, target string) *admin.Binding {
	return &admin.Binding{OrgID: memory.DefaultOrg, ID: id, Principal: principal, Role: role, Target: target, CreatedAt: base}
}

func keyRoundTrip(t *testing.T, s admin.Store) {
	ctx := context.Background()
	k := newKey(memory.DefaultOrg, "key_1", "ci")
	if err := s.CreateAPIKey(ctx, k); err != nil {
		t.Fatal(err)
	}
	if k.Version != 1 {
		t.Fatalf("version after create = %d", k.Version)
	}
	got, err := s.APIKeyByHash(ctx, memory.DefaultOrg, "hash-key_1")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "key_1" || got.Name != "ci" || got.Hash != "hash-key_1" || got.Last4 != "abcd" || !got.CreatedAt.Equal(base) || !got.LastUsedAt.IsZero() || got.Version != 1 {
		t.Fatalf("APIKeyByHash = %+v", got)
	}
	if _, err := s.APIKeyByHash(ctx, memory.DefaultOrg, "nope"); memory.CodeOf(err) != memory.CodeNotFound {
		t.Fatalf("missing key: %v", err)
	}
	if err := s.CreateAPIKey(ctx, newKey(memory.DefaultOrg, "key_2", "deploy")); err != nil {
		t.Fatal(err)
	}
	list, err := s.ListAPIKeys(ctx, memory.DefaultOrg)
	if err != nil || len(list) != 2 || list[0].ID != "key_1" || list[1].ID != "key_2" {
		t.Fatalf("ListAPIKeys = %v, %v", list, err)
	}
}

func keyNameTaken(t *testing.T, s admin.Store) {
	ctx := context.Background()
	if err := s.CreateAPIKey(ctx, newKey(memory.DefaultOrg, "key_1", "ci")); err != nil {
		t.Fatal(err)
	}
	err := s.CreateAPIKey(ctx, newKey(memory.DefaultOrg, "key_2", "ci"))
	if memory.CodeOf(err) != memory.CodeConflict {
		t.Fatalf("duplicate name: %v", err)
	}
}

func keyDelete(t *testing.T, s admin.Store) {
	ctx := context.Background()
	if err := s.CreateAPIKey(ctx, newKey(memory.DefaultOrg, "key_1", "ci")); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteAPIKey(ctx, memory.DefaultOrg, "key_1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.APIKeyByHash(ctx, memory.DefaultOrg, "hash-key_1"); memory.CodeOf(err) != memory.CodeNotFound {
		t.Fatalf("deleted key still found: %v", err)
	}
	if err := s.DeleteAPIKey(ctx, memory.DefaultOrg, "key_1"); memory.CodeOf(err) != memory.CodeNotFound {
		t.Fatalf("second delete: %v", err)
	}
	if err := s.CreateAPIKey(ctx, newKey(memory.DefaultOrg, "key_2", "ci")); err != nil {
		t.Fatalf("name not free after delete: %v", err)
	}
}

func keyTouch(t *testing.T, s admin.Store) {
	ctx := context.Background()
	if err := s.CreateAPIKey(ctx, newKey(memory.DefaultOrg, "key_1", "ci")); err != nil {
		t.Fatal(err)
	}
	at := base.Add(90 * time.Minute)
	if err := s.TouchAPIKey(ctx, memory.DefaultOrg, "key_1", at); err != nil {
		t.Fatal(err)
	}
	got, err := s.APIKeyByHash(ctx, memory.DefaultOrg, "hash-key_1")
	if err != nil || !got.LastUsedAt.Equal(at) {
		t.Fatalf("after touch = %+v, %v", got, err)
	}
}

func bindingRoundTrip(t *testing.T, s admin.Store) {
	ctx := context.Background()
	b := newBinding("bnd_1", "user:alice", admin.RoleMember, "team:platform")
	if err := s.CreateBinding(ctx, b); err != nil {
		t.Fatal(err)
	}
	if b.Version != 1 {
		t.Fatalf("version after create = %d", b.Version)
	}
	all, err := s.Bindings(ctx, memory.DefaultOrg, nil)
	if err != nil || len(all) != 1 {
		t.Fatalf("Bindings = %v, %v", all, err)
	}
	got := all[0]
	if got.ID != "bnd_1" || got.Principal != "user:alice" || got.Role != admin.RoleMember || got.Target != "team:platform" || !got.CreatedAt.Equal(base) || got.Version != 1 {
		t.Fatalf("binding = %+v", got)
	}
}

func bindingDuplicate(t *testing.T, s admin.Store) {
	ctx := context.Background()
	if err := s.CreateBinding(ctx, newBinding("bnd_1", "user:alice", admin.RoleMember, "team:platform")); err != nil {
		t.Fatal(err)
	}
	err := s.CreateBinding(ctx, newBinding("bnd_2", "user:alice", admin.RoleMember, "team:platform"))
	if memory.CodeOf(err) != memory.CodeConflict {
		t.Fatalf("duplicate binding: %v", err)
	}
	if err := s.CreateBinding(ctx, newBinding("bnd_3", "user:alice", admin.RoleVerifier, "team:platform")); err != nil {
		t.Fatalf("another role on the same target: %v", err)
	}
}

func bindingsByPrincipal(t *testing.T, s admin.Store) {
	ctx := context.Background()
	for _, b := range []*admin.Binding{
		newBinding("bnd_1", "user:alice", admin.RoleMember, "team:platform"),
		newBinding("bnd_2", "group:ops", admin.RoleAdmin, "*"),
		newBinding("bnd_3", "user:bob", admin.RoleMember, "team:sales"),
	} {
		if err := s.CreateBinding(ctx, b); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.Bindings(ctx, memory.DefaultOrg, []string{"user:alice", "group:ops"})
	if err != nil || len(got) != 2 || got[0].ID != "bnd_1" || got[1].ID != "bnd_2" {
		t.Fatalf("Bindings(alice, ops) = %v, %v", got, err)
	}
	got, err = s.Bindings(ctx, memory.DefaultOrg, []string{})
	if err != nil || len(got) != 0 {
		t.Fatalf("Bindings(empty) = %v, %v", got, err)
	}
}

func bindingDelete(t *testing.T, s admin.Store) {
	ctx := context.Background()
	if err := s.CreateBinding(ctx, newBinding("bnd_1", "user:alice", admin.RoleMember, "team:platform")); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteBinding(ctx, memory.DefaultOrg, "bnd_1"); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteBinding(ctx, memory.DefaultOrg, "bnd_1"); memory.CodeOf(err) != memory.CodeNotFound {
		t.Fatalf("second delete: %v", err)
	}
	if all, _ := s.Bindings(ctx, memory.DefaultOrg, nil); len(all) != 0 {
		t.Fatalf("after delete: %v", all)
	}
}

func adminOrgSeparation(t *testing.T, s admin.Store) {
	ctx := context.Background()
	if err := s.CreateAPIKey(ctx, newKey("other", "key_1", "ci")); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateAPIKey(ctx, newKey(memory.DefaultOrg, "key_2", "ci")); err != nil {
		t.Fatalf("same name in another org: %v", err)
	}
	if _, err := s.APIKeyByHash(ctx, memory.DefaultOrg, "hash-key_1"); memory.CodeOf(err) != memory.CodeNotFound {
		t.Fatalf("key crossed orgs: %v", err)
	}
	if err := s.DeleteAPIKey(ctx, memory.DefaultOrg, "key_1"); memory.CodeOf(err) != memory.CodeNotFound {
		t.Fatalf("key delete crossed orgs: %v", err)
	}
	b := newBinding("bnd_1", "user:alice", admin.RoleAdmin, "*")
	b.OrgID = "other"
	if err := s.CreateBinding(ctx, b); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Bindings(ctx, memory.DefaultOrg, nil); len(got) != 0 {
		t.Fatalf("binding crossed orgs: %v", got)
	}
	if err := s.DeleteBinding(ctx, memory.DefaultOrg, "bnd_1"); memory.CodeOf(err) != memory.CodeNotFound {
		t.Fatalf("binding delete crossed orgs: %v", err)
	}
}
