package memory

import (
	"regexp"
	"slices"
	"strings"
)

// ScopeType is the kind of owner a memory has.
type ScopeType string

// The scope types.
const (
	ScopeUser    ScopeType = "user"
	ScopeProject ScopeType = "project"
	ScopeTeam    ScopeType = "team"
)

// Scope is the owner of a memory, written as "type:id".
type Scope struct {
	Type ScopeType
	ID   string
}

var scopeID = regexp.MustCompile(`^[A-Za-z0-9._@+-]{1,200}$`)

// userID is wider than scopeID because OIDC subjects are opaque strings such as
// auth0|123 or a URL.
var userID = regexp.MustCompile(`^[^\s\p{C}]+$`)

const maxUserID = 255

// ParseScope parses "user:<subject>", "project:<id>" or "team:<id>".
func ParseScope(s string) (Scope, error) {
	typ, id, ok := strings.Cut(s, ":")
	if !ok {
		return Scope{}, Errorf(CodeInvalidArgument, "scope", "scope %q must look like type:id", s)
	}
	switch t := ScopeType(typ); t {
	case ScopeUser:
		if len(id) > maxUserID || !userID.MatchString(id) {
			return Scope{}, Errorf(CodeInvalidArgument, "scope", "user id %q is not valid", id)
		}
		return Scope{Type: t, ID: id}, nil
	case ScopeProject, ScopeTeam:
		if !scopeID.MatchString(id) {
			return Scope{}, Errorf(CodeInvalidArgument, "scope", "scope id %q is not valid", id)
		}
		return Scope{Type: t, ID: id}, nil
	}
	return Scope{}, Errorf(CodeInvalidArgument, "scope", "unknown scope type %q", typ)
}

// String returns the "type:id" form.
func (s Scope) String() string {
	return string(s.Type) + ":" + s.ID
}

// Principal is the caller of an operation, as identity resolved it.
type Principal struct {
	OrgID   string
	Subject string
	// Teams the principal belongs to, from the token or member bindings.
	Teams []string
	// Projects the principal is a member of. They matter when RestrictProjects is set.
	Projects []string
	// AllTeams and AllProjects come from bindings on team:* and project:*.
	AllTeams, AllProjects bool
	// RestrictProjects is set in restricted project mode.
	RestrictProjects bool
	Project          string
	Channel          Channel
	Client           string
}

// CanRead reports whether the principal may read memories in s.
func (p Principal) CanRead(s Scope) bool {
	switch s.Type {
	case ScopeUser:
		return s.ID == p.Subject
	case ScopeProject:
		return !p.RestrictProjects || p.AllProjects || slices.Contains(p.Projects, s.ID)
	case ScopeTeam:
		return p.AllTeams || slices.Contains(p.Teams, s.ID)
	}
	return false
}

// CanWrite reports whether the principal may write memories in s.
func (p Principal) CanWrite(s Scope) bool {
	return p.CanRead(s)
}

// Author returns the principal as the author of a write.
func (p Principal) Author() Author {
	return Author{Subject: p.Subject, Channel: p.Channel, Client: p.Client}
}
