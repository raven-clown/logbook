// Package outbound sets the headers logbook sends when it calls an embedding
// or extraction endpoint on behalf of a caller.
package outbound

import (
	"context"
	"net/http"
	"strings"

	"github.com/raven-clown/logbook/admin"
	"github.com/raven-clown/logbook/internal/obs"
	"go.opentelemetry.io/otel/propagation"
)

type tokenKey struct{}

// WithToken keeps the caller's bearer token in ctx so a client set to forward
// it can. logbook API keys mean nothing to other services and are never kept.
func WithToken(ctx context.Context, token string) context.Context {
	if token == "" || strings.HasPrefix(token, admin.KeyPrefix) {
		return ctx
	}
	return context.WithValue(ctx, tokenKey{}, token)
}

// Token returns the caller's forwardable token, or "".
func Token(ctx context.Context) string {
	t, _ := ctx.Value(tokenKey{}).(string)
	return t
}

// Apply sets Authorization, X-Request-ID and the W3C trace context on h. With
// forward set and a caller token in ctx, the caller's token is sent; otherwise
// apiKey, when there is one.
func Apply(ctx context.Context, h http.Header, apiKey string, forward bool) {
	switch t := Token(ctx); {
	case forward && t != "":
		h.Set("Authorization", "Bearer "+t)
	case apiKey != "":
		h.Set("Authorization", "Bearer "+apiKey)
	}
	if id := obs.RequestIDFrom(ctx); id != "" {
		h.Set("X-Request-ID", id)
	}
	propagation.TraceContext{}.Inject(ctx, propagation.HeaderCarrier(h))
}
