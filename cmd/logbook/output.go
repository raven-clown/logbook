package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode"

	"github.com/raven-clown/logbook/internal/safejson"
	"github.com/raven-clown/logbook/memory"
)

func (a *app) println(s string) {
	_, _ = fmt.Fprintln(a.stdout, safe(s))
}

// safe escapes control and bidirectional formatting characters, so a memory
// written by an agent cannot move the cursor, recolour or reorder a terminal.
func safe(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == '\n' || r == '\t':
			b.WriteRune(r)
		case unicode.IsControl(r):
			_, _ = fmt.Fprintf(&b, `\x%02x`, r)
		case unicode.Is(unicode.Bidi_Control, r):
			_, _ = fmt.Fprintf(&b, `\u%04x`, r)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func (a *app) printJSON(v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	_, err = a.stdout.Write(append(safejson.Escape(b), '\n'))
	return err
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
