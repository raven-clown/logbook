package identity

import (
	"context"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/raven-clown/logbook/memory"
)

type verifier struct {
	v *oidc.IDTokenVerifier
}

// verifier runs discovery on first use and again after a failure.
func (r *Resolver) verifier() (*verifier, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.oidc != nil {
		return r.oidc, nil
	}
	ctx := oidc.ClientContext(context.Background(), r.o.HTTPClient)
	p, err := oidc.NewProvider(ctx, r.o.OIDC.Issuer)
	if err != nil {
		return nil, memory.Errorf(memory.CodeUnavailable, "", "the identity provider %s can't be reached: %v", r.o.OIDC.Issuer, err)
	}
	r.oidc = &verifier{v: p.VerifierContext(ctx, &oidc.Config{ClientID: r.o.OIDC.Audience, Now: r.o.Now})}
	return r.oidc, nil
}

func (v *verifier) verify(ctx context.Context, token string) (map[string]any, error) {
	t, err := v.v.Verify(ctx, token)
	if err != nil {
		return nil, unauthenticated("token rejected: %v", err)
	}
	var claims map[string]any
	if err := t.Claims(&claims); err != nil {
		return nil, unauthenticated("token claims can't be read: %v", err)
	}
	return claims, nil
}
