package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/raven-clown/logbook/admin"
	"github.com/raven-clown/logbook/memory"
	"github.com/raven-clown/logbook/store/sqlite"
)

func TestKeyCommands(t *testing.T) {
	c := newCLI(t)
	out := c.ok("key", "create", "ci")
	if !strings.Contains(out, "lbk_") || !strings.Contains(out, "not shown again") {
		t.Fatalf("key create printed %q", out)
	}
	var created struct{ ID, Name, Key string }
	if err := json.Unmarshal([]byte(c.ok("key", "create", "deploy", "--json")), &created); err != nil {
		t.Fatal(err)
	}
	if created.Name != "deploy" || !strings.HasPrefix(created.ID, "key_") || !strings.HasPrefix(created.Key, "lbk_") {
		t.Fatalf("key create --json = %+v", created)
	}
	st, err := sqlite.Open(context.Background(), c.dsn())
	if err != nil {
		t.Fatal(err)
	}
	k, err := st.APIKeyByHash(context.Background(), memory.DefaultOrg, admin.HashKey(created.Key))
	_ = st.Close()
	if err != nil || k.Name != "deploy" {
		t.Fatalf("stored key = %+v, %v", k, err)
	}

	list := c.ok("key", "list")
	if !strings.Contains(list, "ci") || !strings.Contains(list, created.ID) || strings.Contains(list, created.Key) || !strings.Contains(list, created.Key[len(created.Key)-4:]) {
		t.Fatalf("key list = %q", list)
	}
	if code, _, errOut := c.run("key", "create", "ci"); code != 1 || !strings.Contains(errOut, "already exists") {
		t.Fatalf("duplicate name: %d %s", code, errOut)
	}
	if code, _, errOut := c.run("key", "create", "Bad Name"); code != 1 || !strings.Contains(errOut, "key name") {
		t.Fatalf("bad name: %d %s", code, errOut)
	}
	c.ok("key", "delete", created.ID)
	if list := c.ok("key", "list"); strings.Contains(list, created.ID) {
		t.Fatalf("deleted key still listed: %q", list)
	}
	if code, _, errOut := c.run("key", "delete", created.ID); code != 1 || !strings.Contains(errOut, "not found") {
		t.Fatalf("second delete: %d %s", code, errOut)
	}
	if code, _, _ := c.run("key"); code != 2 {
		t.Fatalf("key with no action exited %d", code)
	}
	if code, _, _ := c.run("key", "rotate"); code != 2 {
		t.Fatalf("unknown action exited %d", code)
	}
}

func TestBindingCommands(t *testing.T) {
	c := newCLI(t)
	out := c.ok("binding", "add", "user:alice", "member", "team:platform")
	if !strings.Contains(out, "bnd_") {
		t.Fatalf("binding add printed %q", out)
	}
	var b admin.Binding
	if err := json.Unmarshal([]byte(c.ok("binding", "add", "group:ops", "admin", "*", "--json")), &b); err != nil {
		t.Fatal(err)
	}
	list := c.ok("binding", "list")
	if !strings.Contains(list, "user:alice") || !strings.Contains(list, "team:platform") || !strings.Contains(list, "group:ops") {
		t.Fatalf("binding list = %q", list)
	}
	c.ok("binding", "delete", b.ID)
	if list := c.ok("binding", "list"); strings.Contains(list, "group:ops") {
		t.Fatalf("deleted binding still listed: %q", list)
	}
	if code, _, errOut := c.run("binding", "add", "user:alice", "member", "team:platform"); code != 1 || !strings.Contains(errOut, "already exists") {
		t.Fatalf("duplicate binding: %d %s", code, errOut)
	}
}

func TestBindingRejected(t *testing.T) {
	c := newCLI(t)
	for _, args := range [][]string{
		{"binding", "add", "alice", "member", "team:x"},
		{"binding", "add", "user:alice", "owner", "*"},
		{"binding", "add", "user:alice", "admin", "team:x"},
	} {
		if code, _, errOut := c.run(args...); code != 1 || errOut == "" {
			t.Fatalf("%v: %d %s", args, code, errOut)
		}
	}
	if code, _, _ := c.run("binding", "add", "user:alice"); code != 2 {
		t.Fatalf("missing arguments exited %d", code)
	}
}
