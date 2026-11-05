package mcpserver

import (
	"context"
	"net/http"

	"github.com/mark3labs/mcp-go/server"
	"github.com/raven-clown/logbook/internal/httpx"
	"github.com/raven-clown/logbook/internal/identity"
	"github.com/raven-clown/logbook/memory"
)

// HTTPOptions configure the Streamable HTTP handler.
type HTTPOptions struct {
	DefaultProject string
}

type httpInfo struct {
	caller    identity.Caller
	project   string
	userAgent string
}

type httpInfoKey struct{}

// HTTPHandler serves s over Streamable HTTP, without sessions, so any replica
// can answer any request. Requests need a caller from identity in their context.
func HTTPHandler(s *server.MCPServer, o HTTPOptions) http.Handler {
	// Localhost protection is off because every request needs a bearer token,
	// which a rebinding page can't send, and it breaks a reverse proxy on the
	// same host. The GET stream is off because a stateless server has nothing
	// to send on it, and an open one would hold up shutdown.
	streamable := server.NewStreamableHTTPServer(s,
		server.WithStateLess(true),
		server.WithDisableLocalhostProtection(true),
		server.WithDisableStreaming(true),
		server.WithMaxRequestBodyBytes(httpx.MaxBody),
		server.WithHTTPContextFunc(func(ctx context.Context, r *http.Request) context.Context {
			if info, ok := r.Context().Value(httpInfoKey{}).(httpInfo); ok {
				ctx = context.WithValue(ctx, httpInfoKey{}, info)
			}
			return ctx
		}))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, ok := identity.FromContext(r.Context())
		if !ok {
			httpx.WriteError(w, r, memory.Errorf(memory.CodeUnauthenticated, "", "send Authorization: Bearer <token>"))
			return
		}
		project, err := httpx.Project(r, o.DefaultProject)
		if err != nil {
			httpx.WriteError(w, r, err)
			return
		}
		info := httpInfo{caller: c, project: project, userAgent: httpx.ClientName(r)}
		streamable.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), httpInfoKey{}, info)))
	})
}

// HTTPPrincipal is the principal function for New when serving over HTTP.
func HTTPPrincipal(ctx context.Context) memory.Principal {
	info, _ := ctx.Value(httpInfoKey{}).(httpInfo)
	p := info.caller.Principal
	p.Channel = memory.ChannelMCP
	p.Project = info.project
	p.Client = SessionClient(ctx)
	if p.Client == "" {
		p.Client = info.userAgent
	}
	return p
}

// SessionClient returns the client name the MCP client sent when it
// initialised, cleaned, or "".
func SessionClient(ctx context.Context) string {
	if s, ok := server.ClientSessionFromContext(ctx).(server.SessionWithClientInfo); ok {
		return httpx.CleanClientName(s.GetClientInfo().Name)
	}
	return ""
}
