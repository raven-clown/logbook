package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/raven-clown/logbook/memory"
)

func (a *app) println(s string) {
	_, _ = fmt.Fprintln(a.stdout, s)
}

func (a *app) printJSON(v any) error {
	enc := json.NewEncoder(a.stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func (a *app) printMemory(m *memory.Memory, stale bool) {
	status := string(m.Status)
	if stale {
		status += " stale"
	}
	a.println(fmt.Sprintf("%s  %s  %s  %s", m.ID, status, m.Scope, oneLine(m.Content)))
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
