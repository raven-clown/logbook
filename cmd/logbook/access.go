package main

import (
	"fmt"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/raven-clown/logbook/admin"
	"github.com/raven-clown/logbook/memory"
)

func (a *app) accessStore(c *common) (*session, admin.Store, error) {
	s, err := a.open(c)
	if err != nil {
		return nil, nil, err
	}
	st, ok := s.store.(admin.Store)
	if !ok {
		_ = s.store.Close()
		return nil, nil, fmt.Errorf("the %s storage driver does not store API keys and bindings", s.cfg.Database.Driver)
	}
	return s, st, nil
}

func when(t time.Time) string {
	if t.IsZero() {
		return "never"
	}
	return t.UTC().Format(time.RFC3339)
}

const keyUsage = "key create <name> | key list | key delete <id>"

func (a *app) key(args []string) error {
	if len(args) == 0 {
		return usageError{msg: "usage: logbook " + keyUsage}
	}
	fs, c := a.flags("key " + args[0])
	want := map[string]int{"create": 1, "list": 0, "delete": 1}
	n, ok := want[args[0]]
	if !ok {
		return usageError{msg: "usage: logbook " + keyUsage}
	}
	pos, err := parse(fs, args[1:], n, keyUsage)
	if err != nil {
		return err
	}
	s, st, err := a.accessStore(c)
	if err != nil {
		return err
	}
	defer func() { _ = s.store.Close() }()
	switch args[0] {
	case "create":
		if err := admin.ValidKeyName(pos[0]); err != nil {
			return err
		}
		plain, hash, last4, err := admin.NewKey()
		if err != nil {
			return err
		}
		k := &admin.APIKey{OrgID: memory.DefaultOrg, ID: "key_" + ulid.Make().String(), Name: pos[0], Hash: hash, Last4: last4, CreatedAt: s.now().UTC()}
		if err := st.CreateAPIKey(a.ctx, k); err != nil {
			return err
		}
		if c.json {
			return a.printJSON(map[string]string{"id": k.ID, "name": k.Name, "key": plain})
		}
		a.println(fmt.Sprintf("created key %s (%s)", k.Name, k.ID))
		a.println(plain)
		a.println("store it now, it is not shown again")
	case "list":
		keys, err := st.ListAPIKeys(a.ctx, memory.DefaultOrg)
		if err != nil {
			return err
		}
		if c.json {
			if keys == nil {
				keys = []*admin.APIKey{}
			}
			return a.printJSON(map[string]any{"items": keys})
		}
		for _, k := range keys {
			a.println(fmt.Sprintf("%s  %s  ...%s  created %s  last used %s", k.ID, k.Name, k.Last4, when(k.CreatedAt), when(k.LastUsedAt)))
		}
	case "delete":
		if err := st.DeleteAPIKey(a.ctx, memory.DefaultOrg, pos[0]); err != nil {
			return err
		}
		a.println("deleted " + pos[0])
	}
	return nil
}

const bindingUsage = "binding add <principal> <role> <target> | binding list | binding delete <id>"

func (a *app) binding(args []string) error {
	if len(args) == 0 {
		return usageError{msg: "usage: logbook " + bindingUsage}
	}
	fs, c := a.flags("binding " + args[0])
	want := map[string]int{"add": 3, "list": 0, "delete": 1}
	n, ok := want[args[0]]
	if !ok {
		return usageError{msg: "usage: logbook " + bindingUsage}
	}
	pos, err := parse(fs, args[1:], n, bindingUsage)
	if err != nil {
		return err
	}
	s, st, err := a.accessStore(c)
	if err != nil {
		return err
	}
	defer func() { _ = s.store.Close() }()
	switch args[0] {
	case "add":
		b := &admin.Binding{OrgID: memory.DefaultOrg, ID: "bnd_" + ulid.Make().String(), Principal: pos[0], Role: admin.Role(pos[1]), Target: pos[2], CreatedAt: s.now().UTC()}
		if err := b.Validate(); err != nil {
			return err
		}
		if err := st.CreateBinding(a.ctx, b); err != nil {
			return err
		}
		if c.json {
			return a.printJSON(b)
		}
		a.println(fmt.Sprintf("added %s: %s is %s on %s", b.ID, b.Principal, b.Role, b.Target))
	case "list":
		bs, err := st.Bindings(a.ctx, memory.DefaultOrg, nil)
		if err != nil {
			return err
		}
		if c.json {
			if bs == nil {
				bs = []*admin.Binding{}
			}
			return a.printJSON(map[string]any{"items": bs})
		}
		for _, b := range bs {
			a.println(fmt.Sprintf("%s  %s  %s  %s", b.ID, b.Principal, b.Role, b.Target))
		}
	case "delete":
		if err := st.DeleteBinding(a.ctx, memory.DefaultOrg, pos[0]); err != nil {
			return err
		}
		a.println("deleted " + pos[0])
	}
	return nil
}
