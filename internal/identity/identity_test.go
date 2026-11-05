package identity_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"runtime"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
	"github.com/raven-clown/logbook/admin"
	"github.com/raven-clown/logbook/internal/config"
	"github.com/raven-clown/logbook/internal/identity"
	"github.com/raven-clown/logbook/memory"
	"github.com/raven-clown/logbook/store/sqlite"
)

var now = time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)

// clock is the resolver's time; tests that move it restore it.
var clock = now

type idp struct {
	srv  *httptest.Server
	key  *rsa.PrivateKey
	down atomic.Bool
	hits atomic.Int32
	gate chan struct{}
}

func newIDP(t *testing.T) *idp {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	p := &idp{key: key}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		p.hits.Add(1)
		if p.gate != nil {
			<-p.gate
		}
		if p.down.Load() {
			http.Error(w, "down", http.StatusServiceUnavailable)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer": p.srv.URL, "jwks_uri": p.srv.URL + "/jwks",
			"authorization_endpoint": p.srv.URL + "/auth", "id_token_signing_alg_values_supported": []string{"RS256"},
		})
	})
	mux.HandleFunc("GET /jwks", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &key.PublicKey, KeyID: "k1", Algorithm: "RS256", Use: "sig"}}})
	})
	p.srv = httptest.NewServer(mux)
	t.Cleanup(p.srv.Close)
	return p
}

func (p *idp) token(t *testing.T, claims map[string]any) string {
	t.Helper()
	return sign(t, p.key, "k1", claims)
}

func sign(t *testing.T, key *rsa.PrivateKey, kid string, claims map[string]any) string {
	t.Helper()
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: key}, (&jose.SignerOptions{}).WithType("JWT").WithHeader("kid", kid))
	if err != nil {
		t.Fatal(err)
	}
	s, err := jwt.Signed(signer).Claims(claims).Serialize()
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func (p *idp) claims(sub string, extra map[string]any) map[string]any {
	c := map[string]any{"iss": p.srv.URL, "aud": "logbook", "sub": sub, "exp": now.Add(time.Hour).Unix(), "iat": now.Add(-time.Minute).Unix()}
	for k, v := range extra {
		c[k] = v
	}
	return c
}

func openStore(t *testing.T) *sqlite.Store {
	t.Helper()
	s, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "logbook.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func resolver(t *testing.T, st admin.Store, p *idp, mutate func(*identity.Options)) *identity.Resolver {
	t.Helper()
	clock = now
	o := identity.Options{Store: st, ProjectMode: "open", Now: func() time.Time { return clock }, OIDC: config.OIDC{Audience: "logbook", TeamsClaim: "groups"}}
	if p != nil {
		o.OIDC.Issuer = p.srv.URL
		o.HTTPClient = p.srv.Client()
	}
	if mutate != nil {
		mutate(&o)
	}
	return identity.New(o)
}

func addKey(t *testing.T, st admin.Store) string {
	const name = "ci"
	t.Helper()
	plain, hash, last4, err := admin.NewKey()
	if err != nil {
		t.Fatal(err)
	}
	if err := st.CreateAPIKey(context.Background(), &admin.APIKey{OrgID: memory.DefaultOrg, ID: "key_" + name, Name: name, Hash: hash, Last4: last4, CreatedAt: now.Add(-time.Hour)}); err != nil {
		t.Fatal(err)
	}
	return plain
}

func bind(t *testing.T, st admin.Store, principal string, role admin.Role, target string) {
	t.Helper()
	b := &admin.Binding{OrgID: memory.DefaultOrg, ID: "bnd_" + principal + string(role) + target, Principal: principal, Role: role, Target: target, CreatedAt: now}
	if err := st.CreateBinding(context.Background(), b); err != nil {
		t.Fatal(err)
	}
}

func wantCode(t *testing.T, err error, code memory.Code) {
	t.Helper()
	if memory.CodeOf(err) != code {
		t.Fatalf("err = %v, want %s", err, code)
	}
}

func TestAPIKey(t *testing.T) {
	st := openStore(t)
	plain := addKey(t, st)
	c, err := resolver(t, st, nil, nil).Authenticate(context.Background(), plain)
	if err != nil {
		t.Fatal(err)
	}
	if c.Principal.Subject != "key:ci" || c.Principal.OrgID != memory.DefaultOrg || c.Has(admin.RoleAdmin) {
		t.Fatalf("caller = %+v", c)
	}
	if !c.Principal.CanRead(memory.Scope{Type: memory.ScopeUser, ID: "key:ci"}) {
		t.Fatal("key cannot read its own user scope")
	}
	k, _ := st.APIKeyByHash(context.Background(), memory.DefaultOrg, admin.HashKey(plain))
	if !k.LastUsedAt.Equal(now) {
		t.Fatalf("last used = %v", k.LastUsedAt)
	}
}

func TestUnknownKey(t *testing.T) {
	st := openStore(t)
	r := resolver(t, st, nil, nil)
	for _, tok := range []string{"", "lbk_nope", "eyJhbGciOiJub25lIn0.e30."} {
		_, err := r.Authenticate(context.Background(), tok)
		wantCode(t, err, memory.CodeUnauthenticated)
	}
}

func TestJWT(t *testing.T) {
	p := newIDP(t)
	st := openStore(t)
	r := resolver(t, st, p, nil)
	c, err := r.Authenticate(context.Background(), p.token(t, p.claims("auth0|alice", map[string]any{"groups": []string{"platform", "bad id"}})))
	if err != nil {
		t.Fatal(err)
	}
	if c.Principal.Subject != "auth0|alice" || !slices.Equal(c.Principal.Teams, []string{"platform"}) {
		t.Fatalf("caller = %+v", c.Principal)
	}
	if !c.Principal.CanWrite(memory.Scope{Type: memory.ScopeUser, ID: "auth0|alice"}) {
		t.Fatal("cannot write own user scope")
	}
}

func TestJWTRejected(t *testing.T) {
	p := newIDP(t)
	r := resolver(t, openStore(t), p, nil)
	other, _ := rsa.GenerateKey(rand.Reader, 2048)
	for _, tt := range []struct {
		name  string
		token string
	}{
		{"wrong audience", p.token(t, p.claims("alice", map[string]any{"aud": "someone-else"}))},
		{"wrong issuer", p.token(t, p.claims("alice", map[string]any{"iss": "https://evil.example.com"}))},
		{"expired", p.token(t, p.claims("alice", map[string]any{"exp": now.Add(-time.Hour).Unix()}))},
		{"unknown signing key", sign(t, other, "k1", p.claims("alice", nil))},
		{"key subject", p.token(t, p.claims("key:ci", nil))},
		{"local subject", p.token(t, p.claims("local", nil))},
		{"empty subject", p.token(t, p.claims("", nil))},
		{"bad subject", p.token(t, p.claims("a b", nil))},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := r.Authenticate(context.Background(), tt.token)
			wantCode(t, err, memory.CodeUnauthenticated)
		})
	}
}

func TestJWTWithoutIssuer(t *testing.T) {
	p := newIDP(t)
	r := resolver(t, openStore(t), nil, nil)
	_, err := r.Authenticate(context.Background(), p.token(t, p.claims("alice", nil)))
	wantCode(t, err, memory.CodeUnauthenticated)
}

func TestIssuerDownThenUp(t *testing.T) {
	p := newIDP(t)
	p.down.Store(true)
	st := openStore(t)
	plain := addKey(t, st)
	r := resolver(t, st, p, nil)
	tok := p.token(t, p.claims("alice", nil))
	_, err := r.Authenticate(context.Background(), tok)
	wantCode(t, err, memory.CodeUnavailable)
	if _, err := r.Authenticate(context.Background(), plain); err != nil {
		t.Fatalf("key caller while the issuer is down: %v", err)
	}
	p.down.Store(false)
	_, err = r.Authenticate(context.Background(), tok)
	wantCode(t, err, memory.CodeUnavailable)
	if p.hits.Load() != 1 {
		t.Fatalf("discovery ran %d times within the retry wait", p.hits.Load())
	}
	clock = clock.Add(6 * time.Second)
	if _, err := r.Authenticate(context.Background(), tok); err != nil {
		t.Fatalf("after the issuer came back: %v", err)
	}
}

func TestDiscoveryDoesNotQueue(t *testing.T) {
	p := newIDP(t)
	p.gate = make(chan struct{})
	r := resolver(t, openStore(t), p, nil)
	tok := p.token(t, p.claims("alice", nil))
	first := make(chan error, 1)
	go func() {
		_, err := r.Authenticate(context.Background(), tok)
		first <- err
	}()
	for p.hits.Load() == 0 {
		runtime.Gosched()
	}
	_, err := r.Authenticate(context.Background(), tok)
	wantCode(t, err, memory.CodeUnavailable)
	close(p.gate)
	if err := <-first; err != nil {
		t.Fatalf("the caller that ran discovery: %v", err)
	}
	if _, err := r.Authenticate(context.Background(), tok); err != nil {
		t.Fatalf("after discovery: %v", err)
	}
}

func TestClaimShapes(t *testing.T) {
	p := newIDP(t)
	r := resolver(t, openStore(t), p, func(o *identity.Options) {
		o.OIDC.TeamsClaim = "teams"
		o.OIDC.ProjectsClaim = "projects"
		o.ProjectMode = "restricted"
	})
	c, err := r.Authenticate(context.Background(), p.token(t, p.claims("alice", map[string]any{"teams": "ops", "projects": []any{"api", 7, "web"}})))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(c.Principal.Teams, []string{"ops"}) || !slices.Equal(c.Principal.Projects, []string{"api", "web"}) || !c.Principal.RestrictProjects {
		t.Fatalf("principal = %+v", c.Principal)
	}
}

func TestBindingsGrantAccess(t *testing.T) {
	st := openStore(t)
	plain := addKey(t, st)
	bind(t, st, "key:ci", admin.RoleMember, "team:platform")
	bind(t, st, "key:ci", admin.RoleMember, "project:api")
	bind(t, st, "key:ci", admin.RoleMember, "project:*")
	bind(t, st, "key:ci", admin.RoleViewer, "*")
	bind(t, st, "key:other", admin.RoleMember, "team:sales")
	c, err := resolver(t, st, nil, func(o *identity.Options) { o.ProjectMode = "restricted" }).Authenticate(context.Background(), plain)
	if err != nil {
		t.Fatal(err)
	}
	pr := c.Principal
	if !slices.Equal(pr.Teams, []string{"platform"}) || !slices.Equal(pr.Projects, []string{"api"}) || !pr.AllProjects || pr.AllTeams {
		t.Fatalf("principal = %+v", pr)
	}
	if !c.Has(admin.RoleViewer) || c.Has(admin.RoleAdmin) {
		t.Fatalf("roles = %v", c.Roles)
	}
}

func TestGroupBinding(t *testing.T) {
	p := newIDP(t)
	st := openStore(t)
	bind(t, st, "group:platform", admin.RoleMember, "team:*")
	bind(t, st, "group:platform", admin.RoleAdmin, "*")
	c, err := resolver(t, st, p, nil).Authenticate(context.Background(), p.token(t, p.claims("alice", map[string]any{"groups": []string{"platform"}})))
	if err != nil {
		t.Fatal(err)
	}
	if !c.Principal.AllTeams || !c.Has(admin.RoleAdmin) {
		t.Fatalf("caller = %+v", c)
	}
}

func TestBootstrapAdmin(t *testing.T) {
	p := newIDP(t)
	st := openStore(t)
	plain := addKey(t, st)
	r := resolver(t, st, p, func(o *identity.Options) { o.BootstrapAdmins = []string{"user:alice", "key:ci", "group:root"} })
	for _, tok := range []string{
		p.token(t, p.claims("alice", nil)),
		plain,
		p.token(t, p.claims("carol", map[string]any{"groups": []string{"root"}})),
	} {
		c, err := r.Authenticate(context.Background(), tok)
		if err != nil || !c.Has(admin.RoleAdmin) {
			t.Fatalf("caller = %+v, %v", c, err)
		}
	}
	c, _ := r.Authenticate(context.Background(), p.token(t, p.claims("bob", nil)))
	if c.Has(admin.RoleAdmin) {
		t.Fatal("bob is an admin")
	}
}

func TestLocal(t *testing.T) {
	c := identity.Local()
	if c.Principal.Subject != "local" || c.Principal.OrgID != memory.DefaultOrg || !c.Has(admin.RoleAdmin) {
		t.Fatalf("Local() = %+v", c)
	}
}

func TestMiddleware(t *testing.T) {
	st := openStore(t)
	plain := addKey(t, st)
	var failed error
	h := identity.Middleware(resolver(t, st, nil, nil), func(w http.ResponseWriter, _ *http.Request, err error) {
		failed = err
		w.WriteHeader(http.StatusUnauthorized)
	})(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		c, ok := identity.FromContext(r.Context())
		if !ok || c.Principal.Subject != "key:ci" {
			t.Errorf("caller in handler = %+v, %v", c, ok)
		}
	}))
	for _, tt := range []struct {
		header string
		want   int
	}{
		{"", http.StatusUnauthorized},
		{"Basic abc", http.StatusUnauthorized},
		{"Bearer", http.StatusUnauthorized},
		{"Bearer " + plain, http.StatusOK},
		{"bearer  " + plain, http.StatusOK},
	} {
		failed = nil
		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/v1/memories", nil)
		if tt.header != "" {
			req.Header.Set("Authorization", tt.header)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != tt.want {
			t.Fatalf("%q: status %d, want %d", tt.header, rec.Code, tt.want)
		}
		if tt.want != http.StatusOK && memory.CodeOf(failed) != memory.CodeUnauthenticated {
			t.Fatalf("%q: fail got %v", tt.header, failed)
		}
	}
	if _, ok := identity.FromContext(context.Background()); ok {
		t.Fatal("empty context has a caller")
	}
}
