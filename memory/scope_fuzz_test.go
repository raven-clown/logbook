package memory

import "testing"

// FuzzParseScope checks that every scope ParseScope accepts prints back to a string that parses to the same scope.
func FuzzParseScope(f *testing.F) {
	for _, s := range []string{"user:alice", "user:auth0|123", "user:https://idp/x", "project:web.app", "team:platform", "team:", "org:x", "user:a b", "project:a:b"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		sc, err := ParseScope(s)
		if err != nil {
			return
		}
		again, err := ParseScope(sc.String())
		if err != nil || again != sc {
			t.Fatalf("ParseScope(%q) = %+v, but its String %q parses to %+v, %v", s, sc, sc.String(), again, err)
		}
	})
}
