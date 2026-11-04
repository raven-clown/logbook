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

// ParseScope parses "user:<subject>", "project:<id>" or "team:<id>".
func ParseScope(s string) (Scope, error) {
	typ, id, ok := strings.Cut(s, ":")
	if !ok {
		return Scope{}, Errorf(CodeInvalidArgument, "scope", "scope %q must look like type:id", s)
	}
	switch t := ScopeType(typ); t {
	case ScopeUser, ScopeProject, ScopeTeam:
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
	Teams   []string
	Project string
	Channel Channel
	Client  string
}

// CanRead reports whether the principal may read memories in s.
func (p Principal) CanRead(s Scope) bool {
	switch s.Type {
	case ScopeUser:
		return s.ID == p.Subject
	case ScopeProject:
		return true
	case ScopeTeam:
		return slices.Contains(p.Teams, s.ID)
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
