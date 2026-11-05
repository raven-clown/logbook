package identity

import (
	"net/http"
	"strings"
)

// Bearer returns the token from an Authorization: Bearer header, or "".
func Bearer(h http.Header) string {
	scheme, token, ok := strings.Cut(h.Get("Authorization"), " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return ""
	}
	return strings.TrimSpace(token)
}

// Middleware authenticates every request and puts the caller in its context.
// fail writes the response when authentication fails.
func Middleware(r *Resolver, fail func(w http.ResponseWriter, req *http.Request, err error)) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			c, err := r.Authenticate(req.Context(), Bearer(req.Header))
			if err != nil {
				fail(w, req, err)
				return
			}
			next.ServeHTTP(w, req.WithContext(WithCaller(req.Context(), c)))
		})
	}
}
