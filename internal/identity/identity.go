// Package identity turns a bearer token into the caller of a request: an
// OIDC JWT or a logbook API key, plus the role bindings stored for it.
package identity

import (
	"context"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/raven-clown/logbook/admin"
	"github.com/raven-clown/logbook/internal/config"
	"github.com/raven-clown/logbook/memory"
)

// Caller is who sent a request and which admin roles they hold.
type Caller struct {
	Principal memory.Principal
	Roles     []admin.Role
}

// Has reports whether the caller holds role.
func (c Caller) Has(r admin.Role) bool {
	return slices.Contains(c.Roles, r)
}

// Options configure a Resolver.
type Options struct {
	Store           admin.Store
	OIDC            config.OIDC
	ProjectMode     string
	BootstrapAdmins []string
	Now             func() time.Time
	HTTPClient      *http.Client
}

// Resolver authenticates tokens.
type Resolver struct {
	o           Options
	mu          sync.Mutex
	oidc        *verifier
	discovering bool
	failedAt    time.Time
	failErr     error
}

// New returns a Resolver. It does not contact the identity provider until the
// first JWT arrives, so logbook starts while the provider is down.
func New(o Options) *Resolver {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.HTTPClient == nil {
		o.HTTPClient = &http.Client{Timeout: 10 * time.Second}
	}
	if o.OIDC.TeamsClaim == "" {
		o.OIDC.TeamsClaim = "groups"
	}
	return &Resolver{o: o}
}

// Local is the identity of the stdio server and the local commands: the
// subject local, an admin.
func Local() Caller {
	return Caller{Principal: memory.Principal{OrgID: memory.DefaultOrg, Subject: localSubject}, Roles: []admin.Role{admin.RoleAdmin}}
}

const localSubject = "local"

// touchEvery limits how often a key's last use is written.
const touchEvery = time.Minute

const touchTimeout = 500 * time.Millisecond

func unauthenticated(format string, args ...any) error {
	return memory.Errorf(memory.CodeUnauthenticated, "", format, args...)
}

// Authenticate checks token and returns the caller. A missing or bad token is
// unauthenticated; an identity provider that can't be reached is unavailable.
func (r *Resolver) Authenticate(ctx context.Context, token string) (Caller, error) {
	if token == "" {
		return Caller{}, unauthenticated("send Authorization: Bearer <token>")
	}
	if strings.HasPrefix(token, admin.KeyPrefix) {
		return r.apiKey(ctx, token)
	}
	if r.o.OIDC.Issuer == "" {
		return Caller{}, unauthenticated("this server accepts logbook API keys only")
	}
	v, err := r.verifier()
	if err != nil {
		return Caller{}, err
	}
	claims, err := v.verify(ctx, token)
	if err != nil {
		return Caller{}, err
	}
	sub, _ := claims["sub"].(string)
	// key: subjects and the local subject belong to other authenticators, and
	// taking them would read their user scopes.
	if sub == "" || sub == localSubject || strings.HasPrefix(sub, "key:") {
		return Caller{}, unauthenticated("the token's subject %q is not usable", sub)
	}
	if _, err := memory.ParseScope("user:" + sub); err != nil {
		return Caller{}, unauthenticated("the token's subject %q is not usable", sub)
	}
	teams := ids(claims[r.o.OIDC.TeamsClaim], memory.ScopeTeam)
	var projects []string
	if r.o.OIDC.ProjectsClaim != "" {
		projects = ids(claims[r.o.OIDC.ProjectsClaim], memory.ScopeProject)
	}
	return r.caller(ctx, sub, "user:"+sub, teams, projects)
}

func (r *Resolver) apiKey(ctx context.Context, token string) (Caller, error) {
	k, err := r.o.Store.APIKeyByHash(ctx, memory.DefaultOrg, admin.HashKey(token))
	if memory.CodeOf(err) == memory.CodeNotFound {
		return Caller{}, unauthenticated("unknown API key")
	}
	if err != nil {
		return Caller{}, err
	}
	if now := r.o.Now(); now.Sub(k.LastUsedAt) >= touchEvery {
		// A busy database must not hold up the request for its whole busy timeout.
		tctx, cancel := context.WithTimeout(ctx, touchTimeout)
		err := r.o.Store.TouchAPIKey(tctx, k.OrgID, k.ID, now)
		cancel()
		if err != nil {
			slog.WarnContext(ctx, "recording API key use failed", "key", k.Name, "err", err)
		}
	}
	name := "key:" + k.Name
	return r.caller(ctx, name, name, nil, nil)
}

// caller applies bindings and bootstrap admins to an authenticated subject.
func (r *Resolver) caller(ctx context.Context, subject, principal string, teams, projects []string) (Caller, error) {
	names := []string{principal}
	for _, t := range teams {
		names = append(names, "group:"+t)
	}
	bindings, err := r.o.Store.Bindings(ctx, memory.DefaultOrg, names)
	if err != nil {
		return Caller{}, err
	}
	c := Caller{Principal: memory.Principal{
		OrgID: memory.DefaultOrg, Subject: subject, Teams: teams, Projects: projects,
		RestrictProjects: r.o.ProjectMode == "restricted",
	}}
	p := &c.Principal
	for _, b := range bindings {
		switch b.Role {
		case admin.RoleAdmin, admin.RoleViewer:
			c.addRole(b.Role)
		case admin.RoleMember:
			switch typ, id, _ := strings.Cut(b.Target, ":"); {
			case b.Target == "team:*":
				p.AllTeams = true
			case b.Target == "project:*":
				p.AllProjects = true
			case typ == string(memory.ScopeTeam) && !slices.Contains(p.Teams, id):
				p.Teams = append(p.Teams, id)
			case typ == string(memory.ScopeProject) && !slices.Contains(p.Projects, id):
				p.Projects = append(p.Projects, id)
			}
		}
	}
	for _, n := range names {
		if slices.Contains(r.o.BootstrapAdmins, n) {
			c.addRole(admin.RoleAdmin)
		}
	}
	return c, nil
}

func (c *Caller) addRole(r admin.Role) {
	if !c.Has(r) {
		c.Roles = append(c.Roles, r)
	}
}

// ids reads a claim that holds a string or a list of strings and keeps the
// entries that are valid scope ids.
func ids(claim any, typ memory.ScopeType) []string {
	var raw []string
	switch v := claim.(type) {
	case string:
		raw = []string{v}
	case []any:
		for _, e := range v {
			if s, ok := e.(string); ok {
				raw = append(raw, s)
			}
		}
	}
	var out []string
	for _, s := range raw {
		if _, err := memory.ParseScope(string(typ) + ":" + s); err == nil && !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	return out
}

type ctxKey struct{}

// WithCaller returns ctx carrying c.
func WithCaller(ctx context.Context, c Caller) context.Context {
	return context.WithValue(ctx, ctxKey{}, c)
}

// FromContext returns the caller in ctx.
func FromContext(ctx context.Context) (Caller, bool) {
	c, ok := ctx.Value(ctxKey{}).(Caller)
	return c, ok
}
