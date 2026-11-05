// Package admin holds logbook's access data: API keys and role bindings. The
// admin API in milestone 3 adds settings, audit, usage and jobs.
package admin

import (
	"context"
	"regexp"
	"strings"
	"time"

	"github.com/raven-clown/logbook/memory"
)

// Role is what a binding grants.
type Role string

// The roles. admin, viewer and member are shared by every mothership project.
const (
	RoleAdmin    Role = "admin"
	RoleViewer   Role = "viewer"
	RoleMember   Role = "member"
	RoleVerifier Role = "verifier"
)

// APIKey is a static credential for service-to-service calls. Only its hash is stored.
type APIKey struct {
	OrgID      string    `json:"org_id"`
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	Hash       string    `json:"-"`
	Last4      string    `json:"last4"`
	CreatedAt  time.Time `json:"created_at"`
	LastUsedAt time.Time `json:"last_used_at,omitzero"`
	Version    int       `json:"version"`
}

// Binding grants a role on a target to a principal.
type Binding struct {
	OrgID     string    `json:"org_id"`
	ID        string    `json:"id"`
	Principal string    `json:"principal"`
	Role      Role      `json:"role"`
	Target    string    `json:"target"`
	CreatedAt time.Time `json:"created_at"`
	Version   int       `json:"version"`
}

var keyName = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

// ValidKeyName checks an API key name: lower case letters, digits, dot, dash
// and underscore, up to 64 characters.
func ValidKeyName(name string) error {
	if !keyName.MatchString(name) {
		return memory.Errorf(memory.CodeInvalidArgument, "name", "key name %q must be lower case letters, digits, '.', '_' or '-', up to 64 characters", name)
	}
	return nil
}

// ValidPrincipal checks a binding principal: user:<subject>, group:<name> or key:<name>.
func ValidPrincipal(p string) error {
	typ, id, _ := strings.Cut(p, ":")
	var err error
	switch typ {
	case "user":
		_, err = memory.ParseScope("user:" + id)
	case "group":
		_, err = memory.ParseScope("team:" + id)
	case "key":
		err = ValidKeyName(id)
	default:
		err = memory.Errorf(memory.CodeInvalidArgument, "", "unknown type")
	}
	if err != nil {
		return memory.Errorf(memory.CodeInvalidArgument, "principal", "principal %q must be user:<subject>, group:<name> or key:<name>", p)
	}
	return nil
}

// Validate checks the principal, the role and that the role fits the target.
func (b Binding) Validate() error {
	if err := ValidPrincipal(b.Principal); err != nil {
		return err
	}
	switch b.Role {
	case RoleAdmin, RoleViewer:
		if b.Target != "*" {
			return memory.Errorf(memory.CodeInvalidArgument, "target", "the %s role only takes the target *", b.Role)
		}
	case RoleMember, RoleVerifier:
		if b.Target == "project:*" || b.Target == "team:*" {
			return nil
		}
		sc, err := memory.ParseScope(b.Target)
		if err != nil || sc.Type == memory.ScopeUser {
			return memory.Errorf(memory.CodeInvalidArgument, "target", "the %s role takes project:<id>, project:*, team:<id> or team:*, not %q", b.Role, b.Target)
		}
	default:
		return memory.Errorf(memory.CodeInvalidArgument, "role", "role must be admin, viewer, member or verifier, not %q", b.Role)
	}
	return nil
}

// Store keeps API keys and bindings. Backends implement it next to memory.Store
// and prove it with storetest.RunAdmin.
type Store interface {
	// CreateAPIKey returns conflict when the name is taken.
	CreateAPIKey(ctx context.Context, k *APIKey) error
	APIKeyByHash(ctx context.Context, orgID, hash string) (*APIKey, error)
	// ListAPIKeys returns keys oldest first.
	ListAPIKeys(ctx context.Context, orgID string) ([]*APIKey, error)
	DeleteAPIKey(ctx context.Context, orgID, id string) error
	TouchAPIKey(ctx context.Context, orgID, id string, at time.Time) error
	// CreateBinding returns conflict when the same principal, role and target exist.
	CreateBinding(ctx context.Context, b *Binding) error
	// Bindings returns the bindings of principals, or every binding when principals is nil, oldest first.
	Bindings(ctx context.Context, orgID string, principals []string) ([]*Binding, error)
	DeleteBinding(ctx context.Context, orgID, id string) error
}
