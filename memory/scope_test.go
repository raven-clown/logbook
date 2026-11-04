package memory_test

import (
	"testing"

	"github.com/raven-clown/logbook/memory"
)

func TestParseScope(t *testing.T) {
	valid := []struct {
		in   string
		want memory.Scope
	}{
		{"user:alice", memory.Scope{Type: memory.ScopeUser, ID: "alice"}},
		{"user:alice@example.com", memory.Scope{Type: memory.ScopeUser, ID: "alice@example.com"}},
		{"project:mothership", memory.Scope{Type: memory.ScopeProject, ID: "mothership"}},
		{"team:platform", memory.Scope{Type: memory.ScopeTeam, ID: "platform"}},
	}
	for _, tt := range valid {
		t.Run(tt.in, func(t *testing.T) {
			got, err := memory.ParseScope(tt.in)
			if err != nil {
				t.Fatalf("ParseScope(%q) error: %v", tt.in, err)
			}
			if got != tt.want {
				t.Fatalf("ParseScope(%q) = %+v, want %+v", tt.in, got, tt.want)
			}
			back, err := memory.ParseScope(got.String())
			if err != nil || back != got {
				t.Fatalf("round trip of %q gave %+v, %v", tt.in, back, err)
			}
		})
	}

	invalid := []string{"", "user", "user:", "org:x", "team: spaced", ":alice"}
	for _, in := range invalid {
		t.Run("invalid "+in, func(t *testing.T) {
			_, err := memory.ParseScope(in)
			if memory.CodeOf(err) != memory.CodeInvalidArgument {
				t.Fatalf("ParseScope(%q) code = %q, want invalid_argument", in, memory.CodeOf(err))
			}
		})
	}
}

func TestPrincipalAccess(t *testing.T) {
	p := memory.Principal{OrgID: memory.DefaultOrg, Subject: "alice", Teams: []string{"platform"}, Channel: memory.ChannelMCP}
	tests := []struct {
		scope string
		want  bool
	}{
		{"user:alice", true},
		{"user:bob", false},
		{"project:anything", true},
		{"team:platform", true},
		{"team:sales", false},
	}
	for _, tt := range tests {
		t.Run(tt.scope, func(t *testing.T) {
			s, err := memory.ParseScope(tt.scope)
			if err != nil {
				t.Fatal(err)
			}
			if got := p.CanRead(s); got != tt.want {
				t.Errorf("CanRead(%s) = %v, want %v", tt.scope, got, tt.want)
			}
			if got := p.CanWrite(s); got != tt.want {
				t.Errorf("CanWrite(%s) = %v, want %v", tt.scope, got, tt.want)
			}
		})
	}
}

func TestPrincipalAuthor(t *testing.T) {
	p := memory.Principal{Subject: "alice", Channel: memory.ChannelCLI, Client: "logbook"}
	want := memory.Author{Subject: "alice", Channel: memory.ChannelCLI, Client: "logbook"}
	if got := p.Author(); got != want {
		t.Fatalf("Author() = %+v, want %+v", got, want)
	}
}

func TestParseKind(t *testing.T) {
	for in, want := range map[string]memory.Kind{
		"":            memory.KindFact,
		"fact":        memory.KindFact,
		"preference":  memory.KindPreference,
		"decision":    memory.KindDecision,
		"procedure":   memory.KindProcedure,
		"observation": memory.KindObservation,
	} {
		got, err := memory.ParseKind(in)
		if err != nil || got != want {
			t.Errorf("ParseKind(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	_, err := memory.ParseKind("note")
	var e *memory.Error
	if !asError(err, &e) || e.Code != memory.CodeInvalidArgument || e.Field != "kind" {
		t.Fatalf("ParseKind(note) error = %v, want invalid_argument on kind", err)
	}
}

func TestStatusActive(t *testing.T) {
	for s, want := range map[memory.Status]bool{
		memory.StatusUnverified: true,
		memory.StatusVerified:   true,
		memory.StatusDisputed:   true,
		memory.StatusSuperseded: false,
		memory.StatusForgotten:  false,
	} {
		if got := s.Active(); got != want {
			t.Errorf("%s.Active() = %v, want %v", s, got, want)
		}
	}
}
