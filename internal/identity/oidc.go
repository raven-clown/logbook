package identity

import (
	"context"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/raven-clown/logbook/memory"
)

type verifier struct {
	v *oidc.IDTokenVerifier
}

// retryAfter is how long a failed discovery is remembered, so callers get
// unavailable at once instead of each waiting on a provider that is down.
const retryAfter = 5 * time.Second

// verifier runs discovery on first use and again after a failure. Only one
// discovery runs at a time; other callers get unavailable meanwhile.
func (r *Resolver) verifier() (*verifier, error) {
	r.mu.Lock()
	switch {
	case r.oidc != nil:
		v := r.oidc
		r.mu.Unlock()
		return v, nil
	case r.discovering:
		r.mu.Unlock()
		return nil, memory.Errorf(memory.CodeUnavailable, "", "contacting the identity provider %s, try again", r.o.OIDC.Issuer)
	case r.failErr != nil && r.o.Now().Sub(r.failedAt) < retryAfter:
		err := r.failErr
		r.mu.Unlock()
		return nil, err
	}
	r.discovering = true
	r.mu.Unlock()

	ctx := oidc.ClientContext(context.Background(), r.o.HTTPClient)
	p, err := oidc.NewProvider(ctx, r.o.OIDC.Issuer)

	r.mu.Lock()
	defer r.mu.Unlock()
	r.discovering = false
	if err != nil {
		r.failedAt = r.o.Now()
		r.failErr = memory.Errorf(memory.CodeUnavailable, "", "the identity provider %s can't be reached: %v", r.o.OIDC.Issuer, err)
		return nil, r.failErr
	}
	r.failErr = nil
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
