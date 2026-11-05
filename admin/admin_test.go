package admin_test

import (
	"strings"
	"testing"

	"github.com/raven-clown/logbook/admin"
	"github.com/raven-clown/logbook/memory"
)

func TestBindingValidate(t *testing.T) {
	for _, tt := range []struct {
		principal, role, target, field string
	}{
		{"user:alice", "admin", "*", ""},
		{"group:ops", "viewer", "*", ""},
		{"key:ci", "member", "project:api", ""},
		{"user:auth0|7", "member", "team:*", ""},
		{"group:ops", "verifier", "team:platform", ""},
		{"user:alice", "member", "project:*", ""},
		{"alice", "member", "team:x", "principal"},
		{"robot:x", "member", "team:x", "principal"},
		{"key:Bad Name", "member", "team:x", "principal"},
		{"group:", "member", "team:x", "principal"},
		{"user:alice", "owner", "*", "role"},
		{"user:alice", "admin", "team:x", "target"},
		{"user:alice", "viewer", "project:*", "target"},
		{"user:alice", "member", "*", "target"},
		{"user:alice", "member", "user:bob", "target"},
		{"user:alice", "member", "team:bad id", "target"},
		{"user:alice", "verifier", "", "target"},
	} {
		t.Run(tt.principal+" "+tt.role+" "+tt.target, func(t *testing.T) {
			err := admin.Binding{Principal: tt.principal, Role: admin.Role(tt.role), Target: tt.target}.Validate()
			if tt.field == "" {
				if err != nil {
					t.Fatalf("Validate() = %v", err)
				}
				return
			}
			var e *memory.Error
			if !asError(err, &e) || e.Code != memory.CodeInvalidArgument || e.Field != tt.field {
				t.Fatalf("Validate() = %v, want invalid_argument on %s", err, tt.field)
			}
		})
	}
}

func TestValidKeyName(t *testing.T) {
	for _, ok := range []string{"ci", "deploy-bot", "a.b_c", "0"} {
		if err := admin.ValidKeyName(ok); err != nil {
			t.Fatalf("ValidKeyName(%q) = %v", ok, err)
		}
	}
	for _, bad := range []string{"", "CI", "-x", "a b", strings.Repeat("a", 65), "a:b"} {
		if memory.CodeOf(admin.ValidKeyName(bad)) != memory.CodeInvalidArgument {
			t.Fatalf("ValidKeyName(%q) accepted", bad)
		}
	}
}

func TestNewKey(t *testing.T) {
	k1, h1, last4, err := admin.NewKey()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(k1, "lbk_") || len(k1) != 47 || h1 != admin.HashKey(k1) || len(h1) != 64 || last4 != k1[len(k1)-4:] {
		t.Fatalf("NewKey() = %q %q %q", k1, h1, last4)
	}
	k2, h2, _, _ := admin.NewKey()
	if k1 == k2 || h1 == h2 {
		t.Fatal("two keys are equal")
	}
}
